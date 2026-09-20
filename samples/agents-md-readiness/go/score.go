// The Jev call, and the arithmetic on what comes back.
//
// Jev answers questions. It cannot count, and its number comparisons fail, so
// every sum in this tool happens here in Go: each answer turns into credit from
// 0 to 1, and readiness is the weighted average of the credit that the payload
// attaches a weight to.
//
// The file also turns numbers into the words the table prints, which is where the
// deadband below matters.
package main

import (
	"bytes"         // an in-memory buffer, used to hold the request body
	"encoding/json" // turns Go values into JSON and back
	"fmt"
	"math" // Abs and Round, for the band-edge comparison
	"net/http"
	"sort"    // orders the options of a Choice by probability
	"strconv" // parses and formats numbers
	"time"    // the request timeout
)

const (
	// endpoint is the one URL Jev exposes. Every question type goes to it.
	endpoint = "https://api.typesafe.ai/v1/systemone"
	// apiKeyEnv is the environment variable holding the key. fetch.go reads it.
	apiKeyEnv = "TYPESAFE_API_KEY"
	// requestTimeout caps one call. Jev answers in well under a second, so a
	// minute means something else broke: DNS, a proxy, a hung connection.
	requestTimeout = 60 * time.Second
)

// band pairs a floor with the word that names every value at or above it. A
// "band" here is one stripe of the 0 to 1 range: credit of 0.90 sits in the
// stripe that starts at 0.85, so it reads as "strong".
//
// Go has no tuple type, so a two-field struct carries the pair. The slices below
// list the stripes highest floor first, which lets a search return the first
// floor a value clears.
type band struct {
	floor float64 // the lowest value this band covers
	word  string  // what to print for a value inside it
}

// judgements names each stripe of credit for a question where a high number is
// good. Credit is what one answer contributed toward readiness, from 0 to 1, so
// these words read the same way for a Choice, a Score and a Noul.
var judgements = []band{
	{0.85, "strong"},
	{0.60, "adequate"},
	{0.35, "thin"},
	{0.15, "weak"},
	{0.00, "missing"},
}

// judgementsInverted names the same stripes for a question where credit means the
// thing stayed out of the file, `leaks_secret` being the one that does. "Missing"
// would read as praise on a leaked key, so an inverted row gets its own words.
var judgementsInverted = []band{
	{0.85, "clean"},
	{0.60, "probably clean"},
	{0.35, "suspect"},
	{0.15, "likely present"},
	{0.00, "present"},
}

// A Choice and a Score arrive with a confidence, and confidence under this reads
// as a coin toss. A Noul carries no confidence field: its probability is the
// confidence, so a value in the middle of the range gets the same treatment.
const (
	unsureBelow   = 0.50
	unsureBandLow = 0.35
	unsureBandTop = 0.65
)

// Repeat calls on one document moved a credit by up to 0.03, so a value this near
// a band edge can land on either side of it between runs. Within this distance the
// label names both bands instead of picking one, so two runs of the same file
// print readings that agree. The unsure markers widen by the same amount, because
// over-flagging doubt costs a reader nothing and under-flagging it costs trust.
const deadband = 0.02

// answer holds one answer as Jev returns it. All three question types decode into
// this one struct, because the wire tags each answer with its type and leaves the
// fields that do not apply empty.
//
// The strings in backticks are struct tags. They tell encoding/json which JSON key
// feeds which field, so the Go name and the wire name can differ.
type answer struct {
	Type string `json:"type"` // "noul", "choice" or "score"
	// Noul is a probability from 0 to 1 for a yes-or-no statement.
	Noul float64 `json:"noul"`
	// Choice is the label Jev picked out of the options the question listed.
	Choice string `json:"choice"`
	// Score is a rubric level, 0 up to the top level the legend defines.
	Score float64 `json:"score"`
	// Confidence is how sure Jev is of a Choice or a Score. A Noul leaves it 0,
	// since its probability already carries that meaning.
	Confidence float64 `json:"confidence"`
	// Legend maps each rubric level of a Score to the text the question wrote for
	// it, so output can say what level 2 means. Keys arrive as strings.
	Legend map[string]string `json:"legend"`
	// Probabilities is the mass Jev put on each option of a Choice or each level
	// of a Score. A map has no order, so output sorts it before printing.
	Probabilities map[string]float64 `json:"probabilities"`
}

// usage is the token count the API reports for one call. Jev charges for input
// tokens, and report.go prices a run from InputTokens.
type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// apiResponse is the whole body the API returns. Answers is keyed by the question
// id the request used, which is how each answer finds its question again.
type apiResponse struct {
	Model   string            `json:"model"`
	Answers map[string]answer `json:"answers"`
	Usage   usage             `json:"usage"`
}

// wireQuestion is one question in the shape the API expects. The payload file
// carries more per question (a label, a weight, a credit table), and none of that
// belongs on the wire, so this struct is the subset Jev sees.
//
// omitempty on a tag drops the field from the JSON when it holds a zero value: a
// Noul sends no criteria at all, where a Choice sends an object and a Score an
// array.
type wireQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions,omitempty"`
	Criteria     any    `json:"criteria,omitempty"`
}

// ask sends the document and every question to Jev in one HTTP request, and
// returns the decoded answers, the raw bytes (which -verbose prints), how many
// milliseconds the round trip took, and an error.
//
// Go functions return several values at once, and the caller names each one. The
// error is last by convention, and a caller checks it before trusting the rest.
//
// One call carries all seventeen questions. That is the point of the pattern: the
// document is the expensive part of the request, so asking one more question about
// a document already on the wire costs a few tokens and no extra round trip.
func ask(key string, set settings, specs []spec, doc document) (apiResponse, []byte, int, error) {
	// Cut the document to the state budget from questions.yml. Slicing a string in
	// Go counts bytes, and text[:n] keeps the first n of them. A long document
	// costs accuracy as well as money, so the budget is a deliberate ceiling.
	text := doc.Text
	if len(text) > set.MaxStateChars {
		text = text[:set.MaxStateChars]
	}

	// Build the questions the wire wants, keyed by id. make with a size hint
	// allocates room for every question once instead of growing the map as it
	// fills. Order does not matter here: the response comes back keyed by the
	// same ids, and print order comes from the payload file.
	questions := make(map[string]wireQuestion, len(specs))
	for _, s := range specs {
		questions[s.ID] = wireQuestion{
			Type:         s.Q.Type,
			Instructions: s.Q.Instructions,
			Criteria:     s.Q.Criteria,
		}
	}

	// The state is an object with named fields, so Jev knows where one part ends
	// and the next begins. The filename travels with the text, which stops a
	// CLAUDE.md from being read as an AGENTS.md.
	//
	// Treat the document as untrusted input, the same way you treat a prompt: a
	// file that argues for its own completeness moves these answers. map[string]any
	// means "keys are strings, values are anything", which is how Go expresses a
	// mixed JSON object.
	payload := map[string]any{
		"model": set.Model,
		"state": map[string]string{
			"filename":           doc.Name,
			"agent_instructions": text,
		},
		"questions": questions,
	}

	// Marshal turns the Go value into JSON bytes. The err != nil check after every
	// fallible call is the Go idiom: no exceptions, so each error travels back to
	// the caller with context wrapped around it by %w.
	body, err := json.Marshal(payload)
	if err != nil {
		return apiResponse{}, nil, 0, fmt.Errorf("encoding the request: %w", err)
	}

	// bytes.NewReader hands the request a stream to read the body from.
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return apiResponse{}, nil, 0, err
	}
	// Auth is a bearer token, and the body is JSON. Nothing logs this header: the
	// key never leaves this function.
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")

	// &http.Client{...} builds a client and takes its address, so the pointer the
	// Do method wants is right there. The timeout covers the whole call.
	client := &http.Client{Timeout: requestTimeout}
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return apiResponse{}, nil, 0, fmt.Errorf("calling %s: %w", endpoint, err)
	}
	// defer runs this line when the function returns, whichever path it takes.
	// Closing the body releases the connection; skipping it leaks one per call.
	defer response.Body.Close()

	// Measure latency around the network call only, so the number in the output
	// means the round trip rather than the whole program.
	raw, err := readAll(response)
	latency := int(time.Since(started).Milliseconds())
	if err != nil {
		return apiResponse{}, nil, latency, err
	}
	// Any status other than 200 carries a message worth showing: a 401 for a bad
	// key, a 400 for a malformed question. trim keeps the message short.
	if response.StatusCode != http.StatusOK {
		return apiResponse{}, raw, latency, fmt.Errorf("%s returned %d: %s", endpoint, response.StatusCode, trim(raw))
	}

	// Unmarshal fills the struct through a pointer, hence &decoded. Fields the
	// response omits keep their zero values.
	var decoded apiResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return apiResponse{}, raw, latency, fmt.Errorf("decoding the response: %w", err)
	}
	// Return the decoded answers, the raw bytes for -verbose, and the latency.
	return decoded, raw, latency, nil
}

// credit turns one answer into what it contributed toward readiness, from 0 to 1.
// Three question types answer in three shapes, and this function is where they
// become one comparable number.
func credit(q question, a answer) float64 {
	switch q.Type {
	case "noul":
		// A Noul is already 0 to 1. An inverted question earns credit for the
		// absence of the thing, so a 0.98 probability that the file leaks a key
		// earns 0.02.
		if q.Invert {
			return 1 - a.Noul
		}
		return a.Noul
	case "score":
		// A Score is a rubric level, so divide by the top level to get a fraction.
		// The legend lists every level including 0, hence len - 1 for the top.
		top := float64(len(a.Legend) - 1)
		// Guard the division: a one-entry legend would divide by zero, which in Go
		// floating point yields +Inf rather than a panic, and +Inf would poison
		// readiness.
		if top <= 0 {
			return 0
		}
		return a.Score / top
	default:
		// A Choice has no natural number, so questions.yml maps each option to the
		// credit it earns. Reading a key a map lacks returns the zero value, which
		// is 0.0 here, so an unlisted option earns nothing.
		return q.Credit[a.Choice]
	}
}

// readinessOf reduces every weighted answer to one number from 0 to 1.
//
// It is a weighted average: each credit counts for the weight its question
// carries, and the sum divides by the weight actually present rather than by 1.0.
// That means editing one weight in questions.yml needs no rebalancing of the rest,
// and a question carrying no weight (weakest_area) stays out of the arithmetic.
func readinessOf(specs []spec, answers map[string]answer) float64 {
	// var declares both as 0. Go zeroes every variable it creates.
	var total, earned float64
	for _, s := range weighted(specs) {
		total += s.Q.Weight
		earned += s.Q.Weight * credit(s.Q, answers[s.ID])
	}
	// No weighted question means nothing to average, and dividing by zero here
	// would produce NaN.
	if total == 0 {
		return 0
	}
	return earned / total
}

// bandWord names the band a value falls in, or names both bands when the value
// sits on the edge between them.
//
// Why the edge case exists: Jev is a statistical model, so asking it the same
// question twice about the same document returns numbers that differ a little.
// A credit of 0.851 and a credit of 0.849 straddle the 0.85 floor, and a reader
// who runs the tool twice would see "strong" once and "adequate" once. Inside the
// deadband the label says "adequate to strong" both times, which is the honest
// reading of a number that close to a cut.
//
// bands must arrive highest floor first, with a bottom band whose floor is 0.
func bandWord(value float64, bands []band) string {
	for i, b := range bands {
		// The bottom band's floor is the bottom of the whole range, so no edge
		// sits beneath it and it never produces a compound word.
		if b.floor <= 0 {
			continue
		}
		// Rounding the distance keeps a value exactly one deadband away inside its
		// own band. Binary floating point stores 0.85 and 0.83 as tiny
		// approximations, so 0.85 - 0.83 comes out as 0.020000000000000018, which
		// would fail a bare <= 0.02 comparison.
		if math.Round(math.Abs(value-b.floor)*1e6)/1e6 <= deadband {
			// bands[i+1] is the band below this one, and it exists because the
			// floor above is greater than 0.
			return bands[i+1].word + " to " + b.word
		}
	}

	// No edge nearby, so return the first band whose floor the value clears. The
	// slice runs highest floor first, which makes the first match the right one.
	for _, b := range bands {
		if value >= b.floor {
			return b.word
		}
	}

	// A value below every floor cannot happen while the bottom floor is 0, and
	// returning the bottom word keeps the function total rather than panicking.
	return bands[len(bands)-1].word
}

// judgementOf reads one answer as a word, and appends "unsure" to the ones Jev
// hedged on. The word describes the credit, so it answers "how did this check go"
// rather than "what number came back".
func judgementOf(q question, a answer) string {
	// An inverted question earns credit for the absence of something, so it needs
	// the other set of words.
	bands := judgements
	if q.Invert {
		bands = judgementsInverted
	}

	word := bandWord(credit(q, a), bands)

	// Both markers widen by the deadband, so a confidence wobbling around the cut
	// reads the same way on every run. Over-flagging doubt costs a reader nothing.
	unsure := a.Confidence < unsureBelow+deadband
	if q.Type == "noul" {
		unsure = a.Noul >= unsureBandLow-deadband && a.Noul <= unsureBandTop+deadband
	}
	if unsure {
		return word + ", unsure"
	}
	return word
}

// returned says what Jev sent back for one question, in Jev's own terms rather
// than the tool's. The table prints this beside the judgement so a reader can see
// the raw number the word came from.
//
// The %.2f style verbs are format directives: %.2f prints a float with two
// decimals, %d an integer, %s a string.
func returned(a answer) string {
	switch a.Type {
	case "noul":
		return fmt.Sprintf("%.2f", a.Noul)
	case "score":
		return fmt.Sprintf("%.1f / %d, confidence %.2f", a.Score, len(a.Legend)-1, a.Confidence)
	default:
		return fmt.Sprintf("%s, confidence %.2f", a.Choice, a.Confidence)
	}
}

// describeChoice lists every option and the probability Jev gave it, best first.
// Seeing the runners-up matters: a 0.51 winner over a 0.49 second place is a
// different fact from a 0.99 winner, and both print as the same Choice.
func describeChoice(a answer) string {
	// A type declared inside a function is local to it. This one exists to hold a
	// label and its probability together while they are sorted, since a Go map has
	// no order and cannot be sorted in place.
	type pair struct {
		label string
		value float64
	}
	// make([]pair, 0, n) creates an empty slice with room for n items, so append
	// never has to grow it.
	pairs := make([]pair, 0, len(a.Probabilities))
	for label, value := range a.Probabilities {
		pairs = append(pairs, pair{label, value})
	}
	// sort.Slice takes a function that answers "does item i belong before item j".
	// Highest probability first, and ties break alphabetically so two runs of the
	// same document print the same order.
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].value != pairs[j].value {
			return pairs[i].value > pairs[j].value
		}
		return pairs[i].label < pairs[j].label
	})

	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, fmt.Sprintf("%s %.2f", p.label, p.value))
	}
	return join(parts, ", ")
}

// describeScore names the rubric level a score sits closest to, quoting Jev's own
// legend rather than wording this tool invented. A score of 1.7 out of 2 means
// little on its own; "nearest level 2" plus the text of level 2 means something.
func describeScore(a answer) string {
	top := len(a.Legend) - 1
	// Adding 0.5 before truncating to an int rounds to the nearest level: Go throws
	// away the fraction on a conversion, so 1.7 + 0.5 becomes 2 and 1.2 + 0.5
	// becomes 1.
	nearest := int(a.Score + 0.5)
	// A score sitting at the very top rounds past the last level, so clamp it.
	if nearest > top {
		nearest = top
	}
	// Legend keys arrive as strings on the wire, so the int becomes a string again
	// to look one up. %q wraps the text in quotes.
	return fmt.Sprintf("nearest level %d %q", nearest, a.Legend[strconv.Itoa(nearest)])
}

// costUSD prices one call from its input tokens, using the price in questions.yml.
// Jev counts output tokens and charges nothing for them, so they stay out of this.
// The underscores in 1_000_000 are digit separators Go ignores.
func costUSD(u usage, set settings) float64 {
	return float64(u.InputTokens) * set.InputUSDPerMillion / 1_000_000
}
