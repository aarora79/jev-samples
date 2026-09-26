// The two reports every run writes: a JSON one for a machine and a markdown one
// for a person.
//
// Both land beside the dataset, named after the repository and the selector, and
// both match what the Python sample writes so either tool can read either file.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// questionReport is one question's answer and what the binary made of it.
type questionReport struct {
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Weight   *float64 `json:"weight"`
	Inverted bool     `json:"inverted"`
	// Returned is Jev's answer, decoded and re-encoded rather than passed through,
	// so the report holds one shape whatever the API adds later.
	Returned answer `json:"returned"`
	// Credit and AddsToLoad are pointers so an unweighted question writes null
	// rather than a 0 that reads like a real judgment.
	Credit     *float64 `json:"credit"`
	AddsToLoad *float64 `json:"adds_to_load"`
}

// pullReport is one pull request in the JSON report.
type pullReport struct {
	Number       int     `json:"number"`
	Title        string  `json:"title"`
	URL          string  `json:"url"`
	Author       string  `json:"author"`
	ChangedFiles int     `json:"changed_files"`
	Additions    int     `json:"additions"`
	Deletions    int     `json:"deletions"`
	ReviewLoad   float64 `json:"review_load"`
	// Consequence and the route come from the second axis: what breaks if this is
	// wrong, rather than how long it takes to read.
	Consequence     float64                   `json:"consequence"`
	ConsequenceFrom string                    `json:"consequence_from"`
	Route           string                    `json:"route"`
	RouteDecidedBy  string                    `json:"route_decided_by"`
	Downgrade       string                    `json:"downgrade"`
	Tier            string                    `json:"tier"`
	SizeFloor       string                    `json:"size_floor"`
	RaisedBySize    bool                      `json:"raised_by_size"`
	DiffCoverage    float64                   `json:"diff_coverage"`
	Drivers         []string                  `json:"drivers"`
	Usage           usage                     `json:"usage"`
	LatencyMS       int64                     `json:"latency_ms"`
	Questions       map[string]questionReport `json:"questions"`
}

// triageReport is the whole JSON report.
type triageReport struct {
	Repo           string         `json:"repo"`
	Selector       selector       `json:"selector"`
	TriagedAt      string         `json:"triaged_at"`
	Model          string         `json:"model"`
	QuestionsAsked int            `json:"questions_asked"`
	TierCounts     map[string]int `json:"tier_counts"`
	// RouteCounts rolls the queue up, so a job can read its shape without walking
	// every entry.
	RouteCounts  map[string]int `json:"route_counts"`
	CostUSD      float64        `json:"cost_usd"`
	PullRequests []pullReport   `json:"pull_requests"`
}

// writeReports writes both files and returns their paths.
func writeReports(
	opts options,
	data dataset,
	results []result,
	specs []spec,
	set settings,
) (string, string, error) {
	if err := os.MkdirAll(opts.out, 0o755); err != nil {
		return "", "", fmt.Errorf("making %s: %w", opts.out, err)
	}

	stem := reportStem(opts, data)
	jsonPath := stem + ".json"
	mdPath := stem + ".md"

	if err := writeJSON(jsonPath, buildReport(data, results, specs, set)); err != nil {
		return "", "", err
	}

	body := strings.Join(markdownLines(data, results, specs, set), "\n") + "\n"
	if err := os.WriteFile(mdPath, []byte(body), 0o644); err != nil {
		return "", "", fmt.Errorf("writing %s: %w", mdPath, err)
	}
	return jsonPath, mdPath, nil
}

// buildReport assembles the JSON report.
func buildReport(data dataset, results []result, specs []spec, set settings) triageReport {
	total := weightedTotal(specs)

	counts := make(map[string]int, len(tiers))
	for _, tier := range tiers {
		counts[tier] = 0
	}
	routeCounts := make(map[string]int, len(routes))
	for _, route := range routes {
		routeCounts[route] = 0
	}
	for _, r := range results {
		counts[r.Tier]++
		routeCounts[r.Route]++
	}

	pulls := make([]pullReport, 0, len(results))
	for _, r := range byLoad(results) {
		questions := make(map[string]questionReport, len(specs))
		for _, s := range specs {
			a := r.Answers[s.ID]
			entry := questionReport{
				Label:    s.Q.Label,
				Type:     s.Q.Type,
				Inverted: s.Q.Invert,
				Returned: a,
			}
			// Go has no way to take the address of a literal, so each value gets a
			// variable first. A weighted question fills all three pointers, and an
			// unweighted one leaves them nil, which encodes as null.
			if s.Q.Weight > 0 {
				weight := s.Q.Weight
				creditValue := round4(credit(s, a))
				adds := round4(contribution(s, a, total))
				entry.Weight = &weight
				entry.Credit = &creditValue
				entry.AddsToLoad = &adds
			}
			questions[s.ID] = entry
		}

		pulls = append(pulls, pullReport{
			Number:          r.Pull.Number,
			Title:           r.Pull.Title,
			URL:             r.Pull.URL,
			Author:          r.Pull.Author,
			ChangedFiles:    r.Pull.ChangedFiles,
			Additions:       r.Pull.Additions,
			Deletions:       r.Pull.Deletions,
			ReviewLoad:      round4(r.Load),
			Consequence:     round4(r.Consequence),
			ConsequenceFrom: r.ConsequenceLabel,
			Route:           r.Route,
			RouteDecidedBy:  r.RouteDecidedBy,
			Downgrade:       r.Downgrade,
			Tier:            r.Tier,
			SizeFloor:       sizeFloor(r.Pull.ChangedFiles, r.Pull.Additions+r.Pull.Deletions),
			RaisedBySize:    r.RaisedBySize,
			DiffCoverage:    round4(r.Coverage),
			Drivers:         r.Drivers,
			Usage:           r.Usage,
			LatencyMS:       r.LatencyMS,
			Questions:       questions,
		})
	}

	return triageReport{
		Repo:           data.Repo,
		Selector:       data.Selector,
		TriagedAt:      time.Now().UTC().Format(time.RFC3339),
		Model:          set.Model,
		QuestionsAsked: len(specs),
		TierCounts:     counts,
		RouteCounts:    routeCounts,
		CostUSD:        round8(costUSD(results, set)),
		PullRequests:   pulls,
	}
}

// markdownLines builds the markdown report: the same table and groups the run
// printed, with links a reader can follow.
func markdownLines(data dataset, results []result, specs []spec, set settings) []string {
	tokens := 0
	for _, r := range results {
		tokens += r.Usage.InputTokens
	}

	lines := []string{
		fmt.Sprintf("# Triage: %s", data.Repo),
		"",
		fmt.Sprintf(
			"%s, %d questions each, one call apiece, on %s with `%s`.",
			plural(len(results), "pull request"), len(specs),
			time.Now().UTC().Format("2 January 2006"), set.Model,
		),
		fmt.Sprintf(
			"%s input tokens, $%.5f at $%v per million.",
			commas(tokens), costUSD(results, set), set.InputUSDPerMillion,
		),
		"",
	}
	lines = append(lines, paddedLines(triageHeader, triageRows(results))...)
	lines = append(lines,
		"",
		fmt.Sprintf(
			"Load is the weighted average of nine questions, 0 to 1. Tier comes from that load, "+
				"raised when size demands it: over %d files or %s lines is high whatever Jev returned.",
			sizeFloors[0].files, commas(sizeFloors[0].lines),
		),
	)
	return append(lines, markdownGroups(results)...)
}

// markdownGroups builds one section per tier, heaviest tier first.
func markdownGroups(results []result) []string {
	var lines []string

	for index := len(tiers) - 1; index >= 0; index-- {
		tier := tiers[index]
		members := make([]result, 0, len(results))
		for _, r := range byLoad(results) {
			if r.Tier == tier {
				members = append(members, r)
			}
		}
		if len(members) == 0 {
			continue
		}

		lines = append(lines,
			"",
			fmt.Sprintf("## %s (%d)", tier, len(members)),
			"",
			tierAdvice[tier],
			"",
		)
		for _, r := range members {
			note := ""
			if r.RaisedBySize {
				note = ", raised by the size floor"
			}
			lines = append(lines, fmt.Sprintf(
				"- [#%d](%s) load %.2f%s: %s",
				r.Pull.Number, r.Pull.URL, r.Load, note, r.Pull.Title,
			))
			lines = append(lines, fmt.Sprintf("  - drivers: %s", driverText(r)))
			if r.Coverage < lowCoverageBelow {
				lines = append(lines, fmt.Sprintf(
					"  - read from %.0f%% of the changed files, so the load is a read on part of the diff",
					r.Coverage*100,
				))
			}
		}
	}
	return lines
}

// round4 and round8 trim a float for the report, so a stored number reads the way
// the tables print it rather than carrying seventeen digits of binary noise.
func round4(value float64) float64 {
	return parseRounded(fmt.Sprintf("%.4f", value))
}

func round8(value float64) float64 {
	return parseRounded(fmt.Sprintf("%.8f", value))
}

// parseRounded turns a formatted number back into a float. Formatting and
// reparsing is the shortest way to round to a fixed number of places in Go, and
// json.Unmarshal is already the tool for reading a number out of text.
func parseRounded(text string) float64 {
	var value float64
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return 0
	}
	return value
}
