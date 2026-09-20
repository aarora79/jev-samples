// Payload loading: the questions, weights, model pin and price.
//
// The binary bakes in questions.yml, so it runs with no config file. Point
// -questions at your own copy to score against different questions or weights,
// and the baked copy stays as the default.
//
// The Python sample one directory up owns the canonical payload, at
// ../questions.yml. Go's embed directive reads files inside its own directory
// only, so it cannot reach up a level, and the questions.yml sitting beside this
// file is a copy. build.sh copies the canonical file in before every build, and
// payload_test.go fails when the two drift apart.
package main

// Go groups imports in one block. embed supplies the filesystem type used below,
// fmt builds the error messages, os reads a payload the caller named on the
// command line, and yaml.v3 parses the file.
import (
	"embed"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// payloadFS carries questions.yml into the binary.
//
// The line below is a compiler directive. The Go compiler reads comments that
// begin with go: and acts on them. It copies the bytes of
// questions.yml into the executable and hands them to payloadFS, an embed.FS,
// which behaves like a tiny read-only filesystem living in memory. The directive
// must sit above the variable it fills, with no blank line between.
// That is why shipping this sample means shipping one file: the questions travel
// inside the binary, and no config file has to sit next to it.
//
//go:embed questions.yml
var payloadFS embed.FS

// payloadName is the path to read inside payloadFS, and the name build.sh and
// payload_test.go both use for the copied file.
const payloadName = "questions.yml"

// question is one entry from the questions map in the payload.
//
// The strings in backticks after each field are struct tags. They tell the YAML
// library which key in the file fills which field, so the YAML key
// max_state_chars can land in a Go field named MaxStateChars. Go exports a field
// whose name starts with a capital letter, and the library reads exported fields
// only, hence the capitals here.
//
// A field the file leaves out keeps its zero value: "" for a string, 0 for a
// number, false for a bool, and nil for a map.
type question struct {
	// Type picks how the sample asks and how it scores the answer: noul for a
	// yes-or-no claim, choice for one option out of a named set, score for a
	// rubric level. loadPayload rejects anything else.
	Type string `yaml:"type"`
	// Label is the short name the reports print. orderedQuestions falls back to
	// the question id when the file gives no label.
	Label string `yaml:"label"`
	// Weight is this question's share of the readiness average. A question with
	// no weight still gets asked and printed, and contributes nothing.
	Weight float64 `yaml:"weight"`
	// Invert flips the meaning of a yes. leaks_secret sets it, because a
	// credential in the document costs readiness instead of earning it.
	Invert bool `yaml:"invert"`
	// Instructions is the question text the sample sends to Jev.
	Instructions string `yaml:"instructions"`
	// Criteria holds the options a choice question picks from, or the rubric
	// levels a score question climbs. The shape differs per type, a map for
	// choice and a list for score, so the field takes any: Go's name for a value
	// of any type at all. The sample hands it straight to the API.
	Criteria any `yaml:"criteria"`
	// Credit says what each choice option is worth toward readiness, keyed by
	// option name. A missing option reads as 0 because that is a Go map's zero
	// value for a float64. Only choice questions use it.
	Credit map[string]float64 `yaml:"credit"`
}

// settings holds everything in the payload except the questions: the model pin,
// the state budget and the input price. loadPayload insists on all three.
type settings struct {
	// Model is the pinned model id, jev-1.13.0. Both SDKs default to jev-latest,
	// which would move answers under thresholds calibrated on an older version.
	Model string `yaml:"model"`
	// MaxStateChars caps the document text the sample sends. Padding costs
	// accuracy as well as money, and each question sees the whole state.
	MaxStateChars int `yaml:"max_state_chars"`
	// InputUSDPerMillion is TypeSafe's published input-token price, which the
	// reports use to cost a run. Output tokens are free.
	InputUSDPerMillion float64 `yaml:"input_usd_per_million"`
}

// spec pairs a question with its id, so the order in the file survives.
//
// Question order in questions.yml is print order, and a Go map hands its keys
// back in a random order every time it is walked. Carrying the id alongside the
// question lets the sample keep them in a slice, which does hold its order.
type spec struct {
	// ID is the key from the file, such as leaks_secret.
	ID string
	// Q is the question itself, embedded by value so a spec is self-contained.
	Q question
}

// wholePayload mirrors the file, keeping the questions as a node so the order
// of the keys survives decoding.
//
// The first three fields decode straight into settings. Questions stops at a
// yaml.Node, the library's raw view of a piece of the document, which records
// the keys in the order the file wrote them. orderedQuestions walks that node.
type wholePayload struct {
	Model              string    `yaml:"model"`
	MaxStateChars      int       `yaml:"max_state_chars"`
	InputUSDPerMillion float64   `yaml:"input_usd_per_million"`
	Questions          yaml.Node `yaml:"questions"`
}

// loadPayload reads the payload from path, or from the baked copy when path is
// empty. It returns the settings and the questions in file order.
//
// Go functions can return several values at once, and this one returns three:
// the settings, the questions, and an error. A caller checks the error first,
// and the two values before it mean nothing when the error is set.
func loadPayload(path string) (settings, []spec, error) {
	// var declares a variable with its zero value, so raw starts as a nil slice
	// and err as nil. Both are declared here because the if and else below each
	// assign them, and a variable made with := inside a block dies with it.
	var raw []byte
	var err error
	// origin names the payload in every error message below, so a reader can tell
	// a broken file they passed in from a broken baked-in copy. := declares and
	// assigns in one step, and Go infers the type, here string.
	origin := "the baked-in " + payloadName

	// Two sources: the file the caller named, or the copy inside the binary. An
	// empty path means the caller asked for no file, so the embedded copy wins.
	if path == "" {
		raw, err = payloadFS.ReadFile(payloadName)
	} else {
		raw, err = os.ReadFile(path)
		origin = path
	}
	// The Go idiom: a function returns an error alongside its result, and the
	// caller tests err != nil right away. %w wraps the original error inside the
	// new one, so a caller further up can still inspect the cause, and the
	// printed message keeps the operating system's reason (file missing,
	// permission denied).
	if err != nil {
		return settings{}, nil, fmt.Errorf("reading %s: %w", origin, err)
	}

	// Unmarshal fills a struct from YAML bytes. &whole passes the address of the
	// struct, a pointer, because the library writes into it rather than returning
	// a copy. Without the &, Unmarshal would fill a throwaway.
	var whole wholePayload
	if err := yaml.Unmarshal(raw, &whole); err != nil {
		return settings{}, nil, fmt.Errorf("parsing %s: %w", origin, err)
	}

	// Copy the three scalar fields across into the type the rest of the sample
	// passes around.
	set := settings{
		Model:              whole.Model,
		MaxStateChars:      whole.MaxStateChars,
		InputUSDPerMillion: whole.InputUSDPerMillion,
	}
	// A switch with no value after it runs the first case whose condition holds,
	// which reads better than three stacked ifs. Each check catches a key the
	// file left out, since a missing key leaves the zero value behind: an empty
	// model would send the request to jev-latest, a zero budget would truncate
	// the document to nothing, and a zero price would report every run as free.
	switch {
	case set.Model == "":
		return set, nil, fmt.Errorf("%s: missing model", origin)
	case set.MaxStateChars <= 0:
		return set, nil, fmt.Errorf("%s: missing max_state_chars", origin)
	case set.InputUSDPerMillion <= 0:
		return set, nil, fmt.Errorf("%s: missing input_usd_per_million", origin)
	}

	// Turn the raw questions node into a slice in file order. origin travels along
	// so a bad question names the payload it came from.
	specs, err := orderedQuestions(whole.Questions, origin)
	if err != nil {
		return set, nil, err
	}
	// A payload with no questions would score nothing and still print a report,
	// so refuse it here.
	if len(specs) == 0 {
		return set, nil, fmt.Errorf("%s: no questions", origin)
	}
	return set, specs, nil
}

// orderedQuestions walks the mapping node so the questions keep the order the
// file wrote them in, which is the order the sample prints them.
//
// Decoding questions into a map[string]question would work and would lose the
// order, because Go randomizes the order a map hands its keys back. A yaml.Node
// keeps the document as written, so walking the node preserves the file's order.
//
// A YAML mapping node stores its children flat and alternating: key, value, key,
// value. So Content[0] is the first question id, Content[1] is that question's
// body, Content[2] is the second id, and so on. The loop below steps two at a
// time for that reason.
func orderedQuestions(node yaml.Node, origin string) ([]spec, error) {
	// Guard the shape before indexing. A payload whose questions key holds a list
	// or a string has no key-value children, and the loop would read nonsense.
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: questions is not a mapping", origin)
	}

	// A slice is a growable view of an array, and make takes a length and a
	// capacity. Length 0 starts it empty, and capacity len(Content)/2 reserves
	// room for the number of questions in the file, so appending never reallocates.
	specs := make([]spec, 0, len(node.Content)/2)
	// i jumps two children per pass: i is the key node, i+1 is the value node.
	// The i+1 < len test stops a malformed mapping with a trailing key from
	// reading past the end.
	for i := 0; i+1 < len(node.Content); i += 2 {
		// A key node's Value is the plain text of the key, here the question id.
		id := node.Content[i].Value
		// Decode turns one child node into a question, applying the struct tags
		// above. Go calls it on the node pointer, and &q hands it a pointer to q
		// so it can write into the caller's variable. An error here means the
		// question's own fields are malformed, such as a weight written as text.
		var q question
		if err := node.Content[i+1].Decode(&q); err != nil {
			return nil, fmt.Errorf("%s: question %s: %w", origin, id, err)
		}
		// The API accepts these three question types. An empty case body means
		// "accept and move on", and anything else falls to default. Catching a
		// typo here beats letting the API reject the request mid-run.
		switch q.Type {
		case "noul", "choice", "score":
		default:
			return nil, fmt.Errorf("%s: question %s has unknown type %q", origin, id, q.Type)
		}
		// A question with no label still needs a name in the report, so fall back
		// to its id.
		if q.Label == "" {
			q.Label = id
		}
		// append returns the grown slice, so the result has to be assigned back.
		specs = append(specs, spec{ID: id, Q: q})
	}
	return specs, nil
}

// weighted returns the specs that carry a weight, in file order.
//
// A question with no weight still gets asked, and still prints its answer, and
// earns nothing toward readiness. weakest_area is the example: it names the next
// thing to fix rather than grading the document, so it stays out of the average.
func weighted(specs []spec) []spec {
	// Capacity len(specs) is the worst case, every question weighted, so this
	// allocates once.
	out := make([]spec, 0, len(specs))
	// range over a slice yields an index and a value. The _ discards the index,
	// which this loop has no use for. s is a copy of each spec.
	for _, s := range specs {
		if s.Q.Weight > 0 {
			out = append(out, s)
		}
	}
	return out
}
