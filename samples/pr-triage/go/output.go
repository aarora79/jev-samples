// The triage run and everything it prints: the table, the groups, the totals and
// the per-question detail.
//
// The padded tables are valid markdown, so the same text reads in a terminal and
// pastes into a pull request.
package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// triageHeader names the columns of the one-row-per-pull-request table.
var triageHeader = []string{
	"PR", "Files", "Lines", "Kind", "Load", "Tier", "Review focus", "Title",
}

// maxTitleChars cuts a title so the table stays inside a terminal.
const maxTitleChars = 58

// lowCoverageBelow is the coverage that earns a line saying how much of the diff
// Jev read. Above it, the reading covers enough of the change to stand on its own.
const lowCoverageBelow = 0.70

// triage asks Jev about every pull request in the dataset, prints the triage, and
// writes the two reports.
//
// It returns the exit code, so -fail-on-tier can turn a queue that needs attention
// into a failed CI job.
func triage(opts options, data dataset, set settings, specs []spec, key string) (int, error) {
	results := make([]result, 0, len(data.PullRequests))
	for _, pull := range data.PullRequests {
		r, err := ask(key, set, specs, data.Repo, pull)
		if err != nil {
			return exitError, err
		}
		logf("#%d: load %.2f, tier %s, %d ms", pull.Number, r.Load, r.Tier, r.LatencyMS)
		results = append(results, r)
	}

	fmt.Printf("\n## Triage: %s, %d pull requests\n\n", data.Repo, len(results))
	printPadded(triageHeader, triageRows(results))
	fmt.Printf(
		"\nLoad is the weighted average of nine questions, 0 to 1. Tier comes from that load, "+
			"raised when size demands it: over %d files or %d lines cannot be trivial, over %d "+
			"files or %d lines is high whatever Jev returned.\n",
		sizeFloors[len(sizeFloors)-1].files, sizeFloors[len(sizeFloors)-1].lines,
		sizeFloors[0].files, sizeFloors[0].lines,
	)
	printGroups(results)

	if opts.explain != 0 {
		printDetail(results, specs, opts.explain)
	}

	printTotals(results, specs, set)

	jsonPath, mdPath, err := writeReports(opts, data, results, specs, set)
	if err != nil {
		return exitError, err
	}
	fmt.Printf("Report: %s\nMarkdown: %s\n", jsonPath, mdPath)

	return gate(opts, results)
}

// gate turns the results into an exit code, so a pull request job can fail on a
// queue holding work it wants flagged.
func gate(opts options, results []result) (int, error) {
	if opts.failOnTier == "" {
		return exitOK, nil
	}

	bar := tierIndex(opts.failOnTier)
	for _, r := range byLoad(results) {
		if tierIndex(r.Tier) >= bar {
			return exitGate, fmt.Errorf(
				"#%d lands in %s at load %.2f, at or above the %s bar",
				r.Pull.Number, r.Tier, r.Load, opts.failOnTier,
			)
		}
	}
	return exitOK, nil
}

// byLoad returns the results heaviest first, without disturbing the caller's
// slice.
func byLoad(results []result) []result {
	ordered := make([]result, len(results))
	copy(ordered, results)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Load > ordered[j].Load })
	return ordered
}

// tierLabel names the tier, marking the two cases a reader should not take at
// face value: a tier the size floor raised, and a load sitting on a cut.
func tierLabel(r result) string {
	switch {
	case r.RaisedBySize:
		return r.Tier + " (size)"
	case nearACut(r.Load):
		return r.Tier + " (on a cut)"
	default:
		return r.Tier
	}
}

// triageRows builds one row per pull request, heaviest first.
func triageRows(results []result) [][]string {
	rows := make([][]string, 0, len(results))
	for _, r := range byLoad(results) {
		rows = append(rows, []string{
			fmt.Sprintf("#%d", r.Pull.Number),
			fmt.Sprintf("%d", r.Pull.ChangedFiles),
			fmt.Sprintf("+%d/-%d", r.Pull.Additions, r.Pull.Deletions),
			r.Answers["change_kind"].Choice,
			fmt.Sprintf("%.2f", r.Load),
			tierLabel(r),
			r.Answers["review_focus"].Choice,
			trimTitle(r.Pull.Title),
		})
	}
	return rows
}

// trimTitle cuts a title to the column width, ending in an ellipsis so a reader
// can tell a cut title from a short one.
func trimTitle(title string) string {
	if len(title) <= maxTitleChars {
		return title
	}
	return title[:maxTitleChars-3] + "..."
}

// paddedLines builds one table padded to its widest cell per column.
//
// The padding makes it readable in a terminal, and the pipes keep it valid
// markdown, so the same lines go to the screen and into the markdown report.
func paddedLines(header []string, rows [][]string) []string {
	widths := make([]int, len(header))
	for index, cell := range header {
		widths[index] = len(cell)
	}
	for _, row := range rows {
		for index, cell := range row {
			if index < len(widths) && len(cell) > widths[index] {
				widths[index] = len(cell)
			}
		}
	}

	line := func(cells []string) string {
		padded := make([]string, len(cells))
		for index, cell := range cells {
			padded[index] = cell + strings.Repeat(" ", widths[index]-len(cell))
		}
		return "| " + strings.Join(padded, " | ") + " |"
	}

	rule := make([]string, len(widths))
	for index, width := range widths {
		rule[index] = strings.Repeat("-", width)
	}

	out := []string{line(header), "| " + strings.Join(rule, " | ") + " |"}
	for _, row := range rows {
		out = append(out, line(row))
	}
	return out
}

// printPadded prints one padded table.
func printPadded(header []string, rows [][]string) {
	for _, text := range paddedLines(header, rows) {
		fmt.Println(text)
	}
}

// printGroups prints the pull requests grouped by tier, with the advice for each
// tier and the questions that drove each load.
func printGroups(results []result) {
	// Heaviest tier first, because that is the work a reader has to plan for.
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

		fmt.Printf("\n%s  (%d)  -> %s\n", strings.ToUpper(tier), len(members), tierAdvice[tier])
		for _, r := range members {
			note := ""
			if r.RaisedBySize {
				note = "  [size floor]"
			}
			fmt.Printf("  #%-6d load %.2f%s  %s\n", r.Pull.Number, r.Load, note, r.Pull.Title)
			fmt.Printf("          %s\n", driverText(r))
			if r.Coverage < lowCoverageBelow {
				fmt.Printf(
					"          read from %.0f%% of the changed files, so the load is a read on part of the diff\n",
					r.Coverage*100,
				)
			}
			fmt.Printf("          %s\n", r.Pull.URL)
		}
	}
}

// driverText names the drivers, or says that nothing cleared the floor.
func driverText(r result) string {
	if len(r.Drivers) == 0 {
		return "nothing above the driver floor"
	}
	return strings.Join(r.Drivers, ", ")
}

// printDetail prints every question for one pull request, and what each one
// contributed.
func printDetail(results []result, specs []spec, number int) {
	var found *result
	for index := range results {
		if results[index].Pull.Number == number {
			found = &results[index]
			break
		}
	}
	if found == nil {
		fmt.Printf("\n#%d is not in this dataset, so there is nothing to explain.\n", number)
		return
	}

	total := weightedTotal(specs)
	header := []string{"Key", "Label", "Type", "Jev returned", "Weight", "Credit", "Adds to load"}
	rows := make([][]string, 0, len(specs))
	for _, s := range specs {
		a := found.Answers[s.ID]
		weight, creditCell, adds := "", "", ""
		if s.Q.Weight > 0 {
			weight = fmt.Sprintf("%.2f", s.Q.Weight)
			creditCell = fmt.Sprintf("%.2f", credit(s, a))
			adds = fmt.Sprintf("%.4f", contribution(s, a, total))
		} else {
			adds = "routes the read"
		}
		label := s.Q.Label
		if s.Q.Invert {
			label += " (inverted)"
		}
		rows = append(rows, []string{"`" + s.ID + "`", label, s.Q.Type, returned(a), weight, creditCell, adds})
	}

	fmt.Printf("\n### #%d in full: %s\n\n", found.Pull.Number, found.Pull.Title)
	printPadded(header, rows)
	fmt.Printf(
		"\n**Load %.4f**, the weighted average of the credit column over %.2f of weight. Tier %s.\n",
		found.Load, total, tierLabel(*found),
	)
}

// returned says what Jev sent back for one question, in that question's own
// terms.
func returned(a answer) string {
	switch a.Type {
	case "noul":
		return fmt.Sprintf("%.2f", a.Noul)
	case "score":
		return fmt.Sprintf("%.2f / %d, confidence %.2f", a.Score, len(a.Legend)-1, a.Confidence)
	default:
		return fmt.Sprintf("%s, confidence %.2f", a.Choice, a.Confidence)
	}
}

// printTotals prints what the run cost and how long it took.
func printTotals(results []result, specs []spec, set settings) {
	tokens, latency, thin := 0, int64(0), 0
	for _, r := range results {
		tokens += r.Usage.InputTokens
		latency += r.LatencyMS
		if r.Coverage < lowCoverageBelow {
			thin++
		}
	}
	calls := len(results)

	fmt.Printf(
		"\n%d pull requests, %d questions each, one call apiece. %s input tokens, %s ms total, "+
			"%d ms per call on average, $%.5f at $%v per million input tokens.\n",
		calls, len(specs), commas(tokens), commas(int(latency)),
		latency/int64(max(calls, 1)), costUSD(results, set), set.InputUSDPerMillion,
	)
	if thin > 0 {
		fmt.Printf(
			"%d of %d had patches too large to send whole, so Jev read part of the diff and the "+
				"paths of the rest. Those are the ones the size floor guards.\n",
			thin, calls,
		)
	}
}

// commas groups a number's digits, because a token count reads better as 183,426
// than as 183426. Go's fmt has no thousands flag.
func commas(value int) string {
	text := fmt.Sprintf("%d", value)
	if len(text) <= 3 {
		return text
	}

	var out strings.Builder
	lead := len(text) % 3
	if lead > 0 {
		out.WriteString(text[:lead])
	}
	for index := lead; index < len(text); index += 3 {
		if out.Len() > 0 {
			out.WriteString(",")
		}
		out.WriteString(text[index : index+3])
	}
	return out.String()
}

// reportStem names both reports after the repository and the selector, matching
// what the Python sample writes.
func reportStem(opts options, data dataset) string {
	state := data.Selector.State
	if state == "" {
		state = "open"
	}
	limit := 0
	if data.Selector.Limit != nil {
		limit = *data.Selector.Limit
	}
	since := ""
	if data.Selector.Since != nil {
		since = *data.Selector.Since
	}
	name := fmt.Sprintf("%s-%s-triage", repoSlug(data.Repo), selectorSlug(state, limit, since))
	return filepath.Join(opts.out, name)
}
