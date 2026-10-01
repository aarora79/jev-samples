// The triage run and everything it prints: the table, the groups, the totals and
// the per-question detail.
//
// The padded tables are valid markdown, so the same text reads in a terminal and
// pastes into a pull request.
package main

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
)

// summaryHeader names the columns of the summary table: how the whole queue came
// out, one row per outcome. It covers the pre-triage states as well as the routes,
// so the counts account for every pull request fetched rather than only the ones
// that reached Jev.
var summaryHeader = []string{"Outcome", "Count", "Pull requests"}

// rereviewHeader names the columns of the re-review table: who owes a second look.
// A reviewer asked for changes and the author has pushed since, so the pull request
// is back in that reviewer's court and nobody else can clear it.
var rereviewHeader = []string{"Reviewer", "Waiting", "Pull requests"}

// summaryNumbersShown is how many numbers the summary names per row before it stops
// listing them. A queue with ninety pull requests on one route would otherwise
// print a cell nobody reads.
const summaryNumbersShown = 12

// triageHeader names the columns of the one-row-per-pull-request table. Number and
// title come first, because those are what a reader scans for, then the two scores,
// then what produced the consequence, then the route it bought.
var triageHeader = []string{
	"PR", "Title", "Files", "Lines", "Kind", "Effort", "Cons", "Cons from", "Route",
}

// maxTitleChars cuts a title so the table stays inside a terminal.
const maxTitleChars = 44

// lowCoverageBelow is the coverage that earns a line saying how much of the diff
// Jev read. Above it, the reading covers enough of the change to stand on its own.
const lowCoverageBelow = 0.70

// preTriageStates are checked in this order. Each one is terminal: the pull request
// is not in a reviewable condition, so no Jev call happens and no route is computed.
//
// pending-author-rework sits above the check states because a reviewer has already
// read the diff and named work to do, which is a more specific answer than a red
// build: the branch is waiting on its author either way, and saying so points at
// the person who can move it.
var preTriageStates = []string{"draft", "pending-author-rework", "ci-failing", "ci-pending"}

// stateAdvice says what each state means for a reader, and who it is waiting on.
var stateAdvice = map[string]string{
	"draft": "the author is still working: nothing to review, and nothing to decide",
	"pending-author-rework": "a reviewer asked for changes and the author has not pushed since: " +
		"the next move belongs to the author, not to another reviewer",
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
	// RouteIfAnswered is set only on the one state a Jev call decides, so a reader
	// knows what the change will need once its author answers.
	RouteIfAnswered string
}

// awaitingAuthor says whether a reviewer asked for changes that the author has not
// answered.
//
// A change request is answered by pushing a commit, so the test is whether the
// review was submitted against the commit the branch still points at. The fetcher
// records each reviewer's latest verdict with the commit it judged, so this needs
// no extra API call and no clock arithmetic.
//
// Datasets built before reviews were fetched carry no reviews, and those read as
// not waiting rather than failing.
func awaitingAuthor(pull pullRequest) (bool, string) {
	if pull.HeadSHA == "" {
		return false, ""
	}

	var names []string
	for _, verdict := range pull.Reviews.Latest {
		if verdict.State == "CHANGES_REQUESTED" && verdict.CommitID == pull.HeadSHA {
			who := verdict.User
			if who == "" {
				who = "a reviewer"
			}
			names = append(names, who)
		}
	}
	if len(names) == 0 {
		return false, ""
	}

	return true, fmt.Sprintf("changes requested by %s, and no commits since", strings.Join(names, ", "))
}

// templateRepeats is how many pull requests a comment has to repeat across before it
// counts as a template rather than a reviewer. Two is enough: a person writing the
// same hundred characters twice in one queue is rarer than a bot posting the same
// report everywhere.
const templateRepeats = 2

// templatePrefixChars is how much of a comment to fingerprint. Long enough that two
// reviewers raising the same concern in their own words stay distinct, short enough
// that a template with a pull request number in its footer still matches itself.
const templatePrefixChars = 120

// commentAsksFloor is how sure Jev has to be that a comment asks the author for
// something before the pull request moves to pending-author-rework.
//
// Measured over two runs of twenty-two pull requests, the answers come back bimodal:
// 0.09 to 0.45 where nothing is asked, 0.53 to 0.97 where something is, and nothing
// between. This sits in that gap, where it also reads as more likely than not. A
// comment landing inside the gap is a genuinely borderline comment, and the output
// marks those rather than pretending they settled.
const commentAsksFloor = 0.50

// nearTheFloor says whether a comment answer sits close enough to the floor to flip
// between runs. The same deadband every other threshold here uses.
func nearTheFloor(probability float64) bool {
	return math.Abs(probability-commentAsksFloor) <= deadband+1e-9
}

// markTemplatedComments marks the comments that repeat across the queue, which no
// reviewer wrote.
//
// The same reasoning as the shared-failure check. Nobody reading this change would
// write the same hundred characters on five others, so a comment that repeats came
// from a machine. codecov-commenter posts as an ordinary user rather than an app, so
// no account type catches it, and its text begins "Please install", which reads as a
// request to anybody including a model.
//
// Measured on twenty-two open pull requests: one account posted the same comment on
// six of them, and every comment a person wrote was unique. Dropping these also keeps
// a two-kilobyte coverage report out of six states.
func markTemplatedComments(pulls []pullRequest) {
	type fingerprint struct {
		user   string
		prefix string
	}

	seen := map[fingerprint][]int{}
	for index, pull := range pulls {
		body := strings.Join(strings.Fields(pull.LastComment.Body), " ")
		if body == "" {
			continue
		}
		if len(body) > templatePrefixChars {
			body = body[:templatePrefixChars]
		}
		key := fingerprint{user: pull.LastComment.User, prefix: body}
		seen[key] = append(seen[key], index)
	}

	for _, indexes := range seen {
		if len(indexes) < templateRepeats {
			continue
		}
		for _, index := range indexes {
			pulls[index].LastComment.Templated = true
		}
	}
}

// commentSendsBack says whether Jev read the unanswered comment as a request to the
// author.
//
// Rules cannot do this one. A reviewer who asks a question in a comment instead of
// submitting a change request leaves nothing in the reviews API, and a timestamp
// cannot tell that question from a coverage report or from a reviewer describing
// their own work. So this pull request reaches Jev, pays for the call it was going to
// make anyway, and the answer decides.
//
// The guard matters: when the state carried no comment the question has nothing to
// read, so the answer is ignored rather than trusted.
func commentSendsBack(r result) (bool, string) {
	comment := unansweredComment(r.Pull)
	if comment.CreatedAt == "" {
		return false, ""
	}

	probability := r.Answers["comment_awaits_author"].Noul
	if probability < commentAsksFloor {
		return false, ""
	}

	who := comment.User
	if who == "" {
		who = "a reviewer"
	}
	near := ""
	if nearTheFloor(probability) {
		near = " and close enough to the cut to read either way"
	}
	return true, fmt.Sprintf(
		"%s asked the author for something in a comment, and no commits since "+
			"(Jev read it at %.2f%s)",
		who, probability, near,
	)
}

// sendBackOnComments moves the pull requests Jev says are waiting on their authors
// out of the routes.
//
// These cost a Jev call, unlike the states rules settle, and the route they earned
// travels with them so a reader knows what the change will need once the author
// answers.
func sendBackOnComments(results []result, skipped []notReviewable) ([]result, []notReviewable) {
	kept := make([]result, 0, len(results))
	moved := 0
	for _, r := range results {
		sendBack, reason := commentSendsBack(r)
		if !sendBack {
			kept = append(kept, r)
			continue
		}
		moved++
		skipped = append(skipped, notReviewable{
			Pull:            r.Pull,
			State:           "pending-author-rework",
			Reason:          reason,
			RouteIfAnswered: r.Route,
		})
	}
	if moved > 0 {
		logf("%d moved to pending-author-rework on a comment Jev read", moved)
	}
	return kept, skipped
}

// wholeQueue puts the routed and the skipped pull requests back together, which is
// what the re-review table reads: a reviewer is owed a look whether or not the pull
// request also stopped at pre-triage.
func wholeQueue(results []result, skipped []notReviewable) []pullRequest {
	queue := make([]pullRequest, 0, len(results)+len(skipped))
	for _, r := range results {
		queue = append(queue, r.Pull)
	}
	for _, entry := range skipped {
		queue = append(queue, entry.Pull)
	}
	return queue
}

// awaitingReviewer names the reviewers who asked for changes the author has since
// answered.
//
// The mirror of awaitingAuthor, off the same two fields. A change request judged
// against an older commit than the head means the author has pushed, so the pull
// request is back with that reviewer and nobody else can clear it.
//
// Only CHANGES_REQUESTED counts. A COMMENTED review does not block a merge and
// never reaches the dataset, and an approval or a dismissal owes nothing.
func awaitingReviewer(pull pullRequest) []string {
	if pull.HeadSHA == "" {
		return nil
	}

	var owed []string
	for _, verdict := range pull.Reviews.Latest {
		if verdict.State != "CHANGES_REQUESTED" || verdict.CommitID == pull.HeadSHA {
			continue
		}
		who := verdict.User
		if who == "" {
			who = "a reviewer"
		}
		owed = append(owed, who)
	}
	return owed
}

// reviewerLoad is one reviewer and the pull requests waiting on them.
type reviewerLoad struct {
	Reviewer string
	Numbers  []int
}

// awaitingReviewerMap groups the pull requests owing a second look by the reviewer
// who owes it, busiest reviewer first.
//
// Ties fall back to the login, so two runs over one dataset print the same order.
// Both the table and the JSON report read this, so the grouping lives in one place.
func awaitingReviewerMap(pulls []pullRequest) []reviewerLoad {
	index := map[string]int{}
	var loads []reviewerLoad
	for _, pull := range pulls {
		for _, reviewer := range awaitingReviewer(pull) {
			at, found := index[reviewer]
			if !found {
				index[reviewer] = len(loads)
				loads = append(loads, reviewerLoad{Reviewer: reviewer})
				at = len(loads) - 1
			}
			loads[at].Numbers = append(loads[at].Numbers, pull.Number)
		}
	}

	sort.SliceStable(loads, func(i, j int) bool {
		if len(loads[i].Numbers) != len(loads[j].Numbers) {
			return len(loads[i].Numbers) > len(loads[j].Numbers)
		}
		return loads[i].Reviewer < loads[j].Reviewer
	})

	// Oldest pull request first inside each row, which is the order a reviewer should
	// work through, and it makes the row independent of the order the caller passed.
	for index := range loads {
		sort.Ints(loads[index].Numbers)
	}
	return loads
}

// rereviewRows builds the re-review table, one row per reviewer who owes a look.
func rereviewRows(pulls []pullRequest) [][]string {
	loads := awaitingReviewerMap(pulls)
	rows := make([][]string, 0, len(loads))
	for _, load := range loads {
		rows = append(rows, []string{
			load.Reviewer, fmt.Sprintf("%d", len(load.Numbers)), summaryNumbersOf(load.Numbers),
		})
	}
	return rows
}

// rereviewNote says what the re-review table means, and which of its rows cannot
// move yet.
//
// A pull request can be waiting on a reviewer and held up by its own build at the
// same time. Saying so stops a reviewer opening something that cannot merge
// whatever they decide.
func rereviewNote(pulls []pullRequest, skipped []notReviewable) []string {
	held := map[int]bool{}
	for _, entry := range skipped {
		held[entry.Pull.Number] = true
	}

	var blocked []int
	for _, pull := range pulls {
		if len(awaitingReviewer(pull)) > 0 && held[pull.Number] {
			blocked = append(blocked, pull.Number)
		}
	}

	lines := []string{
		"",
		"Each of these asked for changes and the author has pushed since, so the pull " +
			"request is back with that reviewer, oldest first.",
	}
	if len(blocked) > 0 {
		sort.Sort(sort.Reverse(sort.IntSlice(blocked)))
		named := make([]string, 0, len(blocked))
		for _, number := range blocked {
			named = append(named, fmt.Sprintf("#%d", number))
		}
		lines = append(lines, fmt.Sprintf(
			"%d of them cannot merge yet whatever the reviewer decides, being held up by a "+
				"state above: %s.",
			len(blocked), strings.Join(named, ", "),
		))
	}
	return lines
}

// printRereviewTable prints the re-review table, or says nobody is owed one.
func printRereviewTable(pulls []pullRequest, skipped []notReviewable) {
	rows := rereviewRows(pulls)
	if len(rows) == 0 {
		fmt.Println("No reviewer is owed a second look: every change request is unanswered.")
		return
	}

	printPadded(rereviewHeader, rows)
	for _, line := range rereviewNote(pulls, skipped) {
		fmt.Println(line)
	}
}

// preTriageState says whether a pull request is in no condition to be triaged.
//
// Skipping these is the cheapest thing this binary does: it costs no Jev call at
// all. Measured on eighteen apache/airflow pull requests, ten were in one of these
// states.
func preTriageState(pull pullRequest) (string, string) {
	if pull.Draft {
		return "draft", "marked draft by the author"
	}

	// A switch cannot bind the reason this returns, so this one reads on its own.
	if waiting, reason := awaitingAuthor(pull); waiting {
		return "pending-author-rework", reason
	}

	switch {
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

	// The tally above counts every failing name in the queue, since a name that
	// breaks elsewhere is evidence wherever it appears. Only ci-failing entries get
	// marked, because a pull request held up by a reviewer is waiting on its author
	// whatever its checks are doing, and a note about flaky jobs would read as the
	// reason it stopped.
	for index := range skipped {
		if skipped[index].State != "ci-failing" {
			continue
		}
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

	fmt.Printf("\n### Not reviewable yet: %d of %d\n\n", len(skipped), total)
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
			if entry.RouteIfAnswered != "" {
				fmt.Printf("          once answered it needs: %s\n", entry.RouteIfAnswered)
			}
			fmt.Printf("          %s\n", entry.Pull.Title)
			fmt.Printf("          %s\n", entry.Pull.URL)
		}
		fmt.Println()
	}

	if float64(len(skipped))/float64(total) >= 0.5 {
		fmt.Printf(
			"%.0f%% of this queue cannot be reviewed as it stands, which is the thing to fix "+
				"before anybody reads a route.\n",
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
	// Mark the templated comments before anything copies a pull request. A
	// pullRequest is a value in Go, so the loop below hands out copies, and a mark
	// written afterwards would never reach them.
	markTemplatedComments(data.PullRequests)

	// Pre-triage next, so a draft or a red branch costs nothing.
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

	// The one state a rule cannot settle, so it is decided after the call rather than
	// before it.
	// asked is everything that cost a call. results is what still carries a route
	// after the comment question sent some back, so the run is priced on asked and the
	// routes are printed from results.
	asked := results
	results, skipped = sendBackOnComments(results, skipped)
	fromComment := false
	for _, entry := range skipped {
		if entry.RouteIfAnswered != "" {
			fromComment = true
			break
		}
	}

	fmt.Printf("\n## Triage: %s, %s\n\n", data.Repo, plural(len(data.PullRequests), "pull request"))
	printPadded(summaryHeader, summaryRows(results, skipped))
	for _, line := range summaryNote(len(skipped) > 0, len(results) > 0, fromComment) {
		fmt.Println(line)
	}

	fmt.Printf("\n### Waiting on a reviewer\n\n")
	printRereviewTable(data.PullRequests, skipped)

	switch {
	case len(results) > 0:
		fmt.Printf("\n### The %s that reached Jev\n\n", plural(len(results), "pull request"))
		printPadded(triageHeader, triageRows(results))
		fmt.Printf(
			"\nEffort is the weighted mean of nine questions, 0 to 1, and consequence is the max "+
				"of four. Effort is raised when size demands it: over %d files or %d lines cannot "+
				"be trivial, over %d files or %d lines is high whatever Jev returned.\n",
			sizeFloors[len(sizeFloors)-1].files, sizeFloors[len(sizeFloors)-1].lines,
			sizeFloors[0].files, sizeFloors[0].lines,
		)
	case len(asked) > 0:
		fmt.Println("\nEverything that reached Jev went back to an author, so there is no route " +
			"to report. The calls still happened, and the cost below counts them.")
	default:
		fmt.Println("\nNothing reached Jev, so there is no route to report and nothing was spent.")
	}

	printNotReviewable(skipped, len(data.PullRequests))
	printGroups(results)

	if opts.explain != 0 {
		printDetail(asked, specs, opts.explain)
	}

	// A run that asked anything has a bill and a report, even when every answer sent
	// its pull request back.
	if len(asked) == 0 {
		return exitOK, nil
	}

	printTotals(asked, specs, set)

	jsonPath, mdPath, err := writeReports(opts, data, results, specs, set, skipped, asked)
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
			trimTitle(r.Pull.Title),
			fmt.Sprintf("%d", r.Pull.ChangedFiles),
			fmt.Sprintf("+%d/-%d", r.Pull.Additions, r.Pull.Deletions),
			r.Answers["change_kind"].Choice,
			loadLabel(r),
			fmt.Sprintf("%.2f", r.Consequence),
			r.ConsequenceLabel,
			routeLabel(r),
		})
	}
	return rows
}

// summaryNumbers names the pull requests in one summary row, stopping before the
// cell is unreadable.
func summaryNumbers(pulls []pullRequest) string {
	numbers := make([]int, 0, len(pulls))
	for _, pull := range pulls {
		numbers = append(numbers, pull.Number)
	}
	return summaryNumbersOf(numbers)
}

// summaryNumbersOf is the same for a row already reduced to numbers, which is what
// the re-review table carries.
func summaryNumbersOf(numbers []int) string {
	named := make([]string, 0, len(numbers))
	for _, number := range numbers {
		named = append(named, fmt.Sprintf("#%d", number))
	}
	if len(named) <= summaryNumbersShown {
		return strings.Join(named, ", ")
	}
	return fmt.Sprintf(
		"%s, and %d more",
		strings.Join(named[:summaryNumbersShown], ", "), len(named)-summaryNumbersShown,
	)
}

// summaryRows builds the summary table: every outcome the queue produced, with its
// members.
//
// The rows run in pipeline order, so the pre-triage states come before the routes
// they short-circuit. Empty outcomes are dropped rather than printed as zeros.
func summaryRows(results []result, skipped []notReviewable) [][]string {
	var rows [][]string

	for _, state := range preTriageStates {
		var pulls []pullRequest
		for _, entry := range skipped {
			if entry.State == state {
				pulls = append(pulls, entry.Pull)
			}
		}
		if len(pulls) > 0 {
			rows = append(rows, []string{
				state, fmt.Sprintf("%d", len(pulls)), summaryNumbers(pulls),
			})
		}
	}

	for _, route := range routes {
		var pulls []pullRequest
		for _, r := range byConsequence(results) {
			if r.Route == route {
				pulls = append(pulls, r.Pull)
			}
		}
		if len(pulls) > 0 {
			rows = append(rows, []string{
				route, fmt.Sprintf("%d", len(pulls)), summaryNumbers(pulls),
			})
		}
	}

	return rows
}

// summaryNote says what separates the two kinds of summary row. Both the terminal
// and the markdown report print it, so the wording lives in one place.
//
// A queue can be all states, all routes, or both, and the note has to be true of
// whichever table it sits under.
func summaryNote(anySkipped bool, anyRouted bool, anyFromComment bool) []string {
	settled := "Plain rules settle those before any model call."
	if anyFromComment {
		settled = "Rules settle most of those before any model call, and a comment Jev read " +
			"can add to pending-author-rework after one."
	}

	switch {
	case anySkipped && anyRouted:
		return []string{
			"",
			"The state rows come first. " + settled +
				" Each route below them names what would be enough to merge.",
		}
	case anySkipped:
		return []string{"", "These never reached a route. " + settled}
	case anyRouted:
		return []string{"", "Each route names what would be enough to merge that pull request."}
	}
	return nil
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
