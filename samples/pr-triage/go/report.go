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

// notReviewableJSON is one pull request that never reached Jev, as the report
// stores it.
type notReviewableJSON struct {
	Number        int    `json:"number"`
	Title         string `json:"title"`
	URL           string `json:"url"`
	State         string `json:"state"`
	Reason        string `json:"reason"`
	SharedFailure bool   `json:"shared_failure"`
	// RouteIfAnswered is set only on the one state a Jev call decides.
	RouteIfAnswered string       `json:"route_if_answered,omitempty"`
	Checks          checkSummary `json:"checks"`
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
	RouteCounts map[string]int `json:"route_counts"`
	// StateCounts covers the whole queue, so a job can see how much of it was even
	// reviewable before any routing happened.
	StateCounts map[string]int `json:"state_counts"`
	// AwaitingReviewer is who owes a second look, keyed by reviewer.
	AwaitingReviewer map[string][]int    `json:"awaiting_reviewer"`
	NotReviewable    []notReviewableJSON `json:"not_reviewable"`
	CostUSD          float64             `json:"cost_usd"`
	PullRequests     []pullReport        `json:"pull_requests"`
}

// writeReports writes both files and returns their paths.
func writeReports(
	opts options,
	data dataset,
	results []result,
	specs []spec,
	set settings,
	skipped []notReviewable,
	asked []result,
) (string, string, error) {
	if err := os.MkdirAll(opts.out, 0o755); err != nil {
		return "", "", fmt.Errorf("making %s: %w", opts.out, err)
	}

	stem := reportStem(opts, data)
	jsonPath := stem + ".json"
	mdPath := stem + ".md"

	report := buildReport(data, results, specs, set, skipped)
	// Priced on the calls that happened, which is more than the routes left when the
	// comment question sent some back.
	report.CostUSD = round8(costUSD(asked, set))
	if err := writeJSON(jsonPath, report); err != nil {
		return "", "", err
	}

	body := strings.Join(markdownLines(data, results, specs, set, skipped, asked), "\n") + "\n"
	if err := os.WriteFile(mdPath, []byte(body), 0o644); err != nil {
		return "", "", fmt.Errorf("writing %s: %w", mdPath, err)
	}
	return jsonPath, mdPath, nil
}

// awaitingReviewerJSON reduces the grouping to a plain map for the report. Go
// marshals map keys in sorted order, so the file stays stable between runs even
// though the table orders by load.
func awaitingReviewerJSON(pulls []pullRequest) map[string][]int {
	out := map[string][]int{}
	for _, load := range awaitingReviewerMap(pulls) {
		out[load.Reviewer] = load.Numbers
	}
	return out
}

// buildReport assembles the JSON report.
func buildReport(data dataset, results []result, specs []spec, set settings, skipped []notReviewable) triageReport {
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

	stateCounts := map[string]int{"reviewable": len(results)}
	for _, state := range preTriageStates {
		stateCounts[state] = 0
	}
	notReviewable := make([]notReviewableJSON, 0, len(skipped))
	for _, entry := range skipped {
		stateCounts[entry.State]++
		notReviewable = append(notReviewable, notReviewableJSON{
			Number:          entry.Pull.Number,
			Title:           entry.Pull.Title,
			URL:             entry.Pull.URL,
			State:           entry.State,
			Reason:          entry.Reason,
			SharedFailure:   entry.Shared,
			RouteIfAnswered: entry.RouteIfAnswered,
			Checks:          entry.Pull.Checks,
		})
	}

	return triageReport{
		Repo:             data.Repo,
		Selector:         data.Selector,
		TriagedAt:        time.Now().UTC().Format(time.RFC3339),
		Model:            set.Model,
		QuestionsAsked:   len(specs),
		TierCounts:       counts,
		RouteCounts:      routeCounts,
		StateCounts:      stateCounts,
		AwaitingReviewer: awaitingReviewerJSON(wholeQueue(results, skipped)),
		NotReviewable:    notReviewable,
		CostUSD:          round8(costUSD(results, set)),
		PullRequests:     pulls,
	}
}

// markdownLines builds the markdown report: a summary table, a detail table, then
// the groups, with links a reader can follow.
//
// The summary answers how the queue came out, the detail table answers what each
// pull request scored, and the groups carry the reasoning and the links.
func markdownLines(
	data dataset,
	results []result,
	specs []spec,
	set settings,
	skipped []notReviewable,
	asked []result,
) []string {
	// Everything that cost a call, which is more than what still carries a route.
	tokens := 0
	for _, r := range asked {
		tokens += r.Usage.InputTokens
	}
	total := len(results) + len(skipped)

	lines := []string{
		fmt.Sprintf("# Triage: %s", data.Repo),
		"",
		fmt.Sprintf(
			"%s, of which %d reached Jev at %d questions each, one call apiece, on %s with `%s`.",
			plural(total, "pull request"), len(asked), len(specs),
			time.Now().UTC().Format("2 January 2006"), set.Model,
		),
		fmt.Sprintf(
			"%s input tokens, $%.5f at $%v per million.",
			commas(tokens), costUSD(asked, set), set.InputUSDPerMillion,
		),
		"",
		"## Summary",
		"",
	}
	lines = append(lines, paddedLines(summaryHeader, summaryRows(results, skipped))...)
	fromComment := false
	for _, entry := range skipped {
		if entry.RouteIfAnswered != "" {
			fromComment = true
			break
		}
	}
	lines = append(lines, summaryNote(len(skipped) > 0, len(results) > 0, fromComment)...)

	lines = append(lines, "", "## Waiting on a reviewer", "")
	queue := wholeQueue(results, skipped)
	if rows := rereviewRows(queue); len(rows) > 0 {
		lines = append(lines, paddedLines(rereviewHeader, rows)...)
		lines = append(lines, rereviewNote(queue, skipped)...)
	} else {
		lines = append(lines,
			"No reviewer is owed a second look: every change request is unanswered.")
	}

	if len(results) > 0 {
		lines = append(lines,
			"",
			fmt.Sprintf("## The %s that reached Jev", plural(len(results), "pull request")),
			"",
		)
		lines = append(lines, paddedLines(triageHeader, triageRows(results))...)
		lines = append(lines,
			"",
			fmt.Sprintf(
				"Effort is the weighted mean of nine questions, 0 to 1, and consequence is the "+
					"max of four. `Cons from` names the question that produced the consequence, "+
					"which is what set the route. Effort is raised when size demands it: over %d "+
					"files or %s lines is high whatever Jev returned.",
				sizeFloors[0].files, commas(sizeFloors[0].lines),
			),
		)
	}

	lines = append(lines, markdownNotReviewable(skipped)...)
	return append(lines, markdownGroups(results)...)
}

// markdownNotReviewable builds the section for the pull requests that never reached
// Jev, so the markdown accounts for the whole queue rather than the routed part.
func markdownNotReviewable(skipped []notReviewable) []string {
	if len(skipped) == 0 {
		return nil
	}

	lines := []string{"", fmt.Sprintf("## Not reviewable yet (%d)", len(skipped))}
	for _, state := range preTriageStates {
		members := make([]notReviewable, 0, len(skipped))
		shared := 0
		for _, entry := range skipped {
			if entry.State == state {
				members = append(members, entry)
				if entry.Shared {
					shared++
				}
			}
		}
		if len(members) == 0 {
			continue
		}

		lines = append(lines,
			"",
			fmt.Sprintf("### %s (%d)", state, len(members)),
			"",
			stateAdvice[state],
			"",
		)
		if shared > 0 {
			lines = append(lines, fmt.Sprintf(
				"%d of these fail only on a check that fails elsewhere too, so they are waiting "+
					"on the checks rather than on their authors.",
				shared,
			), "")
		}
		for _, entry := range members {
			lines = append(lines, fmt.Sprintf(
				"- [#%d](%s) %s", entry.Pull.Number, entry.Pull.URL, entry.Pull.Title,
			))
			lines = append(lines, fmt.Sprintf("  - %s", entry.Reason))
			if entry.RouteIfAnswered != "" {
				lines = append(lines, fmt.Sprintf(
					"  - once answered it needs: %s", entry.RouteIfAnswered))
			}
		}
	}
	return lines
}

// markdownGroups builds one section per route, most consequential route first.
func markdownGroups(results []result) []string {
	var lines []string

	for index := len(routes) - 1; index >= 0; index-- {
		route := routes[index]
		members := make([]result, 0, len(results))
		for _, r := range byConsequence(results) {
			if r.Route == route {
				members = append(members, r)
			}
		}
		if len(members) == 0 {
			continue
		}

		lines = append(lines,
			"",
			fmt.Sprintf("## %s (%d)", route, len(members)),
			"",
			routeAdvice[route],
			"",
		)
		for _, r := range members {
			note := ""
			if r.RaisedBySize {
				note = ", raised by the size floor"
			}
			lines = append(lines, fmt.Sprintf(
				"- [#%d](%s) %s", r.Pull.Number, r.Pull.URL, r.Pull.Title,
			))
			lines = append(lines, fmt.Sprintf(
				"  - consequence %.2f from %s, effort %.2f%s",
				r.Consequence, r.ConsequenceLabel, r.Load, note,
			))
			lines = append(lines, fmt.Sprintf("  - drivers: %s", driverText(r)))
			if r.Downgrade != "" {
				lines = append(lines, fmt.Sprintf("  - would drop a route with: %s", r.Downgrade))
			}
			if r.Coverage < lowCoverageBelow {
				lines = append(lines, fmt.Sprintf(
					"  - read from %.0f%% of the changed files, so both numbers read part of the diff",
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
