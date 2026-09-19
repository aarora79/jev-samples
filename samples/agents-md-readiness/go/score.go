// The Jev call, and the arithmetic on what comes back.
//
// Jev cannot do arithmetic, so readiness is computed here: each answer turns
// into credit from 0 to 1, and readiness is the weighted average of the credit
// the payload attaches a weight to.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"
)

const (
	endpoint       = "https://api.typesafe.ai/v1/systemone"
	apiKeyEnv      = "TYPESAFE_API_KEY"
	requestTimeout = 60 * time.Second
)

// judgement bands, highest floor first.
var judgements = []struct {
	floor float64
	word  string
}{
	{0.85, "strong"},
	{0.60, "adequate"},
	{0.35, "thin"},
	{0.15, "weak"},
	{0.00, "missing"},
}

// judgementsInverted reads the same bands for a question where credit means the
// thing stayed out of the file. "Missing" would read as praise on a leaked key.
var judgementsInverted = []struct {
	floor float64
	word  string
}{
	{0.85, "clean"},
	{0.60, "probably clean"},
	{0.35, "suspect"},
	{0.15, "likely present"},
	{0.00, "present"},
}

const (
	unsureBelow   = 0.50
	unsureBandLow = 0.35
	unsureBandTop = 0.65
)

// answer covers all three primitives, since the wire tags them by type.
type answer struct {
	Type          string             `json:"type"`
	Noul          float64            `json:"noul"`
	Choice        string             `json:"choice"`
	Score         float64            `json:"score"`
	Confidence    float64            `json:"confidence"`
	Legend        map[string]string  `json:"legend"`
	Probabilities map[string]float64 `json:"probabilities"`
}

// usage is the token count the API reports.
type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// apiResponse is the body the API returns.
type apiResponse struct {
	Model   string            `json:"model"`
	Answers map[string]answer `json:"answers"`
	Usage   usage             `json:"usage"`
}

// wireQuestion is one question as the API expects it.
type wireQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions,omitempty"`
	Criteria     any    `json:"criteria,omitempty"`
}

// ask sends the state and every question in one request.
func ask(key string, set settings, specs []spec, doc document) (apiResponse, []byte, int, error) {
	text := doc.Text
	if len(text) > set.MaxStateChars {
		text = text[:set.MaxStateChars]
	}

	questions := make(map[string]wireQuestion, len(specs))
	for _, s := range specs {
		questions[s.ID] = wireQuestion{
			Type:         s.Q.Type,
			Instructions: s.Q.Instructions,
			Criteria:     s.Q.Criteria,
		}
	}

	// The filename travels in the state, so a CLAUDE.md is not read as an
	// AGENTS.md. Treat the document as untrusted: a file that argues for its own
	// completeness moves these answers.
	payload := map[string]any{
		"model": set.Model,
		"state": map[string]string{
			"filename":           doc.Name,
			"agent_instructions": text,
		},
		"questions": questions,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return apiResponse{}, nil, 0, fmt.Errorf("encoding the request: %w", err)
	}

	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return apiResponse{}, nil, 0, err
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: requestTimeout}
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return apiResponse{}, nil, 0, fmt.Errorf("calling %s: %w", endpoint, err)
	}
	defer response.Body.Close()

	raw, err := readAll(response)
	latency := int(time.Since(started).Milliseconds())
	if err != nil {
		return apiResponse{}, nil, latency, err
	}
	if response.StatusCode != http.StatusOK {
		return apiResponse{}, raw, latency, fmt.Errorf("%s returned %d: %s", endpoint, response.StatusCode, trim(raw))
	}

	var decoded apiResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return apiResponse{}, raw, latency, fmt.Errorf("decoding the response: %w", err)
	}
	return decoded, raw, latency, nil
}

// credit turns one answer into what it contributed toward readiness, 0 to 1.
func credit(q question, a answer) float64 {
	switch q.Type {
	case "noul":
		if q.Invert {
			return 1 - a.Noul
		}
		return a.Noul
	case "score":
		top := float64(len(a.Legend) - 1)
		if top <= 0 {
			return 0
		}
		return a.Score / top
	default:
		return q.Credit[a.Choice]
	}
}

// readinessOf weights every weighted answer into one number, 0 to 1. Dividing by
// the weights actually present means an edited weight needs no rebalancing.
func readinessOf(specs []spec, answers map[string]answer) float64 {
	var total, earned float64
	for _, s := range weighted(specs) {
		total += s.Q.Weight
		earned += s.Q.Weight * credit(s.Q, answers[s.ID])
	}
	if total == 0 {
		return 0
	}
	return earned / total
}

// judgementOf reads one answer as a word, flagging the ones Jev hedged on.
func judgementOf(q question, a answer) string {
	bands := judgements
	if q.Invert {
		bands = judgementsInverted
	}

	got := credit(q, a)
	word := bands[len(bands)-1].word
	for _, band := range bands {
		if got >= band.floor {
			word = band.word
			break
		}
	}

	unsure := a.Confidence < unsureBelow
	if q.Type == "noul" {
		unsure = a.Noul >= unsureBandLow && a.Noul <= unsureBandTop
	}
	if unsure {
		return word + ", unsure"
	}
	return word
}

// returned says what Jev sent back for one question, in its own terms.
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
func describeChoice(a answer) string {
	type pair struct {
		label string
		value float64
	}
	pairs := make([]pair, 0, len(a.Probabilities))
	for label, value := range a.Probabilities {
		pairs = append(pairs, pair{label, value})
	}
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

// describeScore names the rubric level a score sits closest to, using Jev's own
// legend.
func describeScore(a answer) string {
	top := len(a.Legend) - 1
	nearest := int(a.Score + 0.5)
	if nearest > top {
		nearest = top
	}
	return fmt.Sprintf("nearest level %d %q", nearest, a.Legend[strconv.Itoa(nearest)])
}

// costUSD prices one call from its input tokens.
func costUSD(u usage, set settings) float64 {
	return float64(u.InputTokens) * set.InputUSDPerMillion / 1_000_000
}
