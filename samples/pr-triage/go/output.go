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
	"PR", "Files", "Lines", "Kind", "Load", "Cons", "Route", "Title",
}

// maxTitleChars cuts a title so the table stays inside a terminal.
const maxTitleChars = 58

// lowCoverageBelow is the coverage that earns a line saying how much of the diff
// Jev read. Above it, the reading covers enough of the change to stand on its own.
const lowCoverageBelow = 0.70

// preTriageStates are checked in this order. Each one is terminal: the pull request
// is not in a reviewable condition, so no Jev call happens and no route is computed.
var preTriageStates = []string{"draft", "ci-failing", "ci-pending"}

// stateAdvice says what each state means for a reader, and who it is waiting on.
var stateAdvice = map[string]string{
	"draft":      "the author is still working: nothing to review, and nothing to decide",
	"ci-failing": "the branch cannot merge until the checks pass, so review waits on that",
	"ci-pending": "checks are still running: come back when they land",
}

// notReviewable is one pull request that never reached Jev.
type notReviewable struct {
	Pull   pullRequest
	State  string
	Reason string
	// Shared marks a failure whose check names also fail on other pull requests,
	// which points at the checks rather than at this branch.
	Shared bool
}

// preTriageState says whether a pull request is in no condition to be triaged.
//
// Skipping these is the cheapest thing this binary does: it costs no Jev call at
// all. Measured on eighteen apache/airflow pull requests, ten were in one of these
// states.
func preTriageState(pull pullRequest) (string, string) {
	switch {
	case pull.Draft:
		return "draft", "marked draft by the author"
	case len(pull.Checks.Failing) > 0:
		named := fmt.Sprintf("%d checks", len(pull.Checks.Failing))
		if len(pull.Checks.Failing) == 1 {
			named = pull.Checks.Failing[0]
		}
		return "ci-failing", fmt.Sprintf(
			"%d of %d checks failing: %s", len(pull.Checks.Failing), pull.Checks.Total, named,
		)
	case pull.Checks.Pending > 0:
		return "ci-pending", fmt.Sprintf(
			"%d of %d checks still running", pull.Checks.Pending, pull.Checks.Total,
		)
	}
	return "", ""
}

// markSharedFailures says which failures belong to the branch and which belong to
// the checks.
//
// A check name that fails on several unrelated pull requests is the check being
// broken, not each change breaking it: a boto3 version bump cannot break Postgres
// serialization. It costs no extra API call, because the names are already in the
// dataset.
func markSharedFailures(skipped []notReviewable) {
	seen := map[string]int{}
	for _, entry := range skipped {
		for _, name := range entry.Pull.Checks.Failing {
			seen[name]++
		}
	}

	for index := range skipped {
		failing := skipped[index].Pull.Checks.Failing
		if len(failing) == 0 {
			continue
		}
		shared := true
		for _, name := range failing {
			if seen[name] < 2 {
				shared = false
				break
			}
		}
		if shared {
			skipped[index].Shared = true
			skipped[index].Reason += ", which also fails on other pull requests"
		}
	}
}

// printNotReviewable prints the pull requests that never reached Jev, and why.
//
// A queue that is mostly ci-failing is saying something more useful than any
// routing could: review capacity is not the bottleneck.
func printNotReviewable(skipped []notReviewable, total int) {
	if len(skipped) == 0 {
		return
	}

	fmt.Printf("\n## Not reviewable yet: %d of %d\n\n", len(skipped), total)
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

		fmt.Printf("%s  (%d)  -> %s\n", strings.ToUpper(state), len(members), stateAdvice[state])
		if shared > 0 {
			fmt.Printf(
				"          %d of these fail only on a check that fails elsewhere too, "+
					"so they are waiting on the checks rather than on their authors\n",
				shared,
			)
		}
		for _, entry := range members {
			fmt.Printf("  #%-6d %s\n", entry.Pull.Number, entry.Reason)
			fmt.Printf("          %s\n", entry.Pull.Title)
			fmt.Printf("          %s\n", entry.Pull.URL)
		}
		fmt.Println()
	}

	if float64(len(skipped))/float64(total) >= 0.5 {
		fmt.Printf(
			"%.0f%% of this queue cannot be reviewed as it stands. Fix that before reading "+
				"anything into the routes below.\n",
			100*float64(len(skipped))/float64(total),
		)
	}
}

// triage asks Jev about every pull request in the dataset, prints the triage, and
// writes the two reports.
//
// It returns the exit code, so -fail-on-tier can turn a queue that needs attention
// into a failed CI job.
func triage(opts options, data dataset, set settings, specs []spec, key string) (int, error) {
	// Pre-triage first, so a draft or a red branch costs nothing.
	var skipped []notReviewable
	var reviewable []pullRequest
	for _, pull := range data.PullRequests {
		state, reason := preTriageState(pull)
		if state != "" {
			skipped = append(skipped, notReviewable{Pull: pull, State: state, Reason: reason})
			continue
		}
		reviewable = append(reviewable, pull)
	}
	if len(skipped) > 0 {
		markSharedFailures(skipped)
		logf("%d of %d are not reviewable yet, so they skip Jev", len(skipped), len(data.PullRequests))
	}

	results := make([]result, 0, len(reviewable))
	for _, pull := range reviewable {
		r, err := ask(key, set, specs, data.Repo, pull)
		if err != nil {
			return exitError, err
		}
		logf("#%d: load %.2f, tier %s, %d ms", pull.Number, r.Load, r.Tier, r.LatencyMS)
		results = append(results, r)
	}

	printNotReviewable(skipped, len(data.PullRequests))
	fmt.Printf("\n## Triage: %s, %s\n\n", data.Repo, plural(len(results), "reviewable pull request"))
	if len(results) == 0 {
		fmt.Println("Nothing reached Jev, so there is no route to report.")
		return exitOK, nil
	}
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

	jsonPath, mdPath, err := writeReports(opts, data, results, specs, set, skipped)
	if err != nil {
		return exitError, err
	}
	fmt.Printf("Report: %s\nMarkdown: %s\n", jsonPath, mdPath)

	return gate(opts, results)
}

// gate turns the results into an exit code, so a pull request job can fail on a
// queue holding work it wants flagged.
func gate(opts options, results []result) (int, error) {
	// The route gate reads first, because the route is what a queue acts on.
	if opts.failOnRoute != "" {
		bar := routeIndex(opts.failOnRoute)
		for _, r := range byConsequence(results) {
			if routeIndex(r.Route) >= bar {
				return exitGate, fmt.Errorf(
					"#%d needs %s, consequence %.2f from %s, at or above the %s bar",
					r.Pull.Number, r.Route, r.Consequence, r.ConsequenceLabel, opts.failOnRoute,
				)
			}
		}
	}

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

// loadLabel prints the effort with the same two marks, since the table now shows
// the load where it used to show the tier.
func loadLabel(r result) string {
	text := fmt.Sprintf("%.2f", r.Load)
	switch {
	case r.RaisedBySize:
		return text + " (size)"
	case nearACut(r.Load):
		return text + " (on a cut)"
	default:
		return text
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
			loadLabel(r),
			fmt.Sprintf("%.2f", r.Consequence),
			routeLabel(r),
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

// routeLabel names the route, marking a consequence that sits on a cut so a reader
// treats it as either of the two routes it straddles.
func routeLabel(r result) string {
	if nearARouteCut(r.Consequence) {
		return r.Route + " (on a cut)"
	}
	return r.Route
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

// printGroups prints the pull requests grouped by route, with what each route asks
// for and the one signal that would drop it a route.
//
// The route is the thing a reader acts on, so it sets the grouping. The tier is
// still in the report, as the effort half of the picture.
func printGroups(results []result) {
	// Most expensive route first, because that is the work a reader has to plan for.
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

		fmt.Printf("\n%s  (%d)  -> %s\n", strings.ToUpper(route), len(members), routeAdvice[route])
		for _, r := range members {
			note := ""
			if r.RaisedBySize {
				note = "  [size floor]"
			}
			fmt.Printf(
				"  #%-6d consequence %.2f (%s), effort %.2f%s  %s\n",
				r.Pull.Number, r.Consequence, r.ConsequenceLabel, r.Load, note, r.Pull.Title,
			)
			fmt.Printf("          drivers: %s\n", driverText(r))
			if r.Downgrade != "" {
				fmt.Printf("          would drop a route with: %s\n", r.Downgrade)
			}
			if r.Coverage < lowCoverageBelow {
				fmt.Printf(
					"          read from %.0f%% of the changed files, so both numbers read part of the diff\n",
					r.Coverage*100,
				)
			}
			fmt.Printf("          %s\n", r.Pull.URL)
		}
	}
}

// byConsequence returns the results most consequential first, without disturbing
// the caller's slice.
func byConsequence(results []result) []result {
	ordered := make([]result, len(results))
	copy(ordered, results)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Consequence > ordered[j].Consequence
	})
	return ordered
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
		"\n%s, %d questions each, one call apiece. %s input tokens, %s ms total, "+
			"%d ms per call on average, $%.5f at $%v per million input tokens.\n",
		plural(calls, "pull request"), len(specs), commas(tokens), commas(int(latency)),
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

// plural counts a noun, adding an s only when there is more than one of them, so
// a single pull request does not read as "1 pull requests".
func plural(count int, noun string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, noun)
	}
	return fmt.Sprintf("%d %ss", count, noun)
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
	name := fmt.Sprintf("%s-%s-triage", repoSlug(data.Repo), datasetSlug(data.Selector))
	return filepath.Join(opts.out, name)
}
