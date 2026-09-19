// The JSON report, in the same shape the Python sample writes, so one jq query
// reads either.
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// reportQuestion is one question as the report records it: what Jev returned,
// beside what the payload weighted it at and what this run made of it.
type reportQuestion struct {
	Label     string   `json:"label"`
	Type      string   `json:"type"`
	Weight    *float64 `json:"weight"`
	Inverted  bool     `json:"inverted"`
	Returned  answer   `json:"returned"`
	Credit    *float64 `json:"credit"`
	Judgement *string  `json:"judgement"`
}

// report is a scored run.
type report struct {
	Filename       string                    `json:"filename"`
	Source         string                    `json:"source"`
	Found          bool                      `json:"found"`
	Looked         []string                  `json:"looked"`
	Model          string                    `json:"model"`
	Usage          usage                     `json:"usage"`
	LatencyMS      int                       `json:"latency_ms"`
	CostUSD        float64                   `json:"cost_usd"`
	QuestionsAsked int                       `json:"questions_asked"`
	Readiness      float64                   `json:"readiness"`
	Questions      map[string]reportQuestion `json:"questions"`
}

// missing is a run that found no file. Readiness is null rather than zero,
// because zero would say the document failed every check.
type missing struct {
	Filename  *string           `json:"filename"`
	Source    string            `json:"source"`
	Found     bool              `json:"found"`
	Looked    []string          `json:"looked"`
	Model     *string           `json:"model"`
	Usage     *usage            `json:"usage"`
	Readiness *float64          `json:"readiness"`
	Questions map[string]string `json:"questions"`
}

var slugPattern = regexp.MustCompile(`[^a-z0-9]+`)

// slug reduces a name to lowercase words joined by hyphens.
func slug(text string) string {
	return strings.Trim(slugPattern.ReplaceAllString(strings.ToLower(text), "-"), "-")
}

// reportPath names the file after the repo and the document that was read. A raw
// GitHub URL gives owner and repo; a local path gives the directory the file sits
// in, which is the repo for a checkout.
func reportPath(dir string, doc document) string {
	project := "local"
	if strings.HasPrefix(doc.Source, rawPrefix) {
		parts := strings.Split(strings.TrimPrefix(doc.Source, rawPrefix), "/")
		if len(parts) >= 2 {
			project = parts[0] + "-" + parts[1]
		}
	} else if parent := filepath.Base(filepath.Dir(doc.Source)); parent != "" && parent != "." && parent != string(filepath.Separator) {
		project = parent
	}

	suffix := "not-found"
	if doc.Found {
		suffix = slug(doc.Name)
	}
	return filepath.Join(dir, fmt.Sprintf("%s-%s.json", slug(project), suffix))
}

// round4 keeps the report readable without pretending to more precision than the
// model gives.
func round4(value float64) float64 {
	return math.Round(value*10000) / 10000
}

// scoredReport builds the report for a run that scored a document.
func scoredReport(doc document, specs []spec, res apiResponse, ready float64, latency int, set settings) report {
	questions := make(map[string]reportQuestion, len(specs))
	for _, s := range specs {
		a := res.Answers[s.ID]
		entry := reportQuestion{
			Label:    s.Q.Label,
			Type:     s.Q.Type,
			Inverted: s.Q.Invert,
			Returned: a,
		}
		if s.Q.Weight > 0 {
			weight := s.Q.Weight
			got := round4(credit(s.Q, a))
			word := judgementOf(s.Q, a)
			entry.Weight = &weight
			entry.Credit = &got
			entry.Judgement = &word
		}
		questions[s.ID] = entry
	}

	return report{
		Filename:       doc.Name,
		Source:         doc.Source,
		Found:          true,
		Looked:         doc.Looked,
		Model:          res.Model,
		Usage:          res.Usage,
		LatencyMS:      latency,
		CostUSD:        math.Round(costUSD(res.Usage, set)*1e8) / 1e8,
		QuestionsAsked: len(specs),
		Readiness:      round4(ready),
		Questions:      questions,
	}
}

// writeJSON writes one report into dir, creating it when needed, and returns the
// path it wrote.
func writeJSON(dir string, doc document, body any) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}

	encoded, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding the report: %w", err)
	}

	path := reportPath(dir, doc)
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}

// missingReport builds the report for a repo or directory holding neither name.
func missingReport(doc document) missing {
	return missing{
		Source:    doc.Source,
		Found:     false,
		Looked:    doc.Looked,
		Questions: map[string]string{},
	}
}
