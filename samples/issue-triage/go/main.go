// issue-triage: read a repository's open issues and say which to pick up today.
//
// One static binary, no runtime, no per-repository setup. It reads the issues
// from GitHub, asks Jev eight questions about each body, hands the answers and
// the facts GitHub knows back to Jev as a summary, and groups the result into
// four buckets: today, this week, this month, when time permits.
//
// The split between the two calls is the design. Stage one only ever asks
// things an issue body can settle, like whether there is a way to reproduce the
// problem. Stage two never sees the body, only a fact table, so the decision
// rests on the facts rather than on how persuasively the issue was written.
//
// Exit codes: 0 scored, 1 error.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// version is stamped at build time by build.sh, through the linker.
var version = "dev"

const usageText = `issue-triage: which issues to pick up today.

Usage:
  issue-triage <owner/repo> [flags]
  issue-triage -version

It reads open issues by default. Point it at closed ones with -state closed to
sanity-check the buckets against work whose outcome you already know.

Needs a GitHub token (GITHUB_TOKEN, GH_TOKEN, or the gh CLI) and a Jev key
(TYPESAFE_API_KEY, or a .env file beside the binary or above it).

Flags:
`

// options is every flag, in one struct so main reads as control flow.
type options struct {
	repo      string
	state     string
	limit     int
	sample    int
	seed      int64
	workers   int
	questions string
	dataDir   string
	stdout    bool
	explain   int
	version   bool
}

// parseFlags declares every flag and returns them together.
func parseFlags() options {
	var opt options
	flag.StringVar(&opt.state, "state", "OPEN",
		"Which issues to read: OPEN or CLOSED")
	flag.IntVar(&opt.limit, "limit", defaultLimit,
		"Read at most this many issues, most recently updated first (0 reads all)")
	flag.IntVar(&opt.sample, "sample", 0,
		"After reading, score a random sample of this many, for a spot check")
	flag.Int64Var(&opt.seed, "seed", 7,
		"Seed for -sample, so a spot check repeats")
	flag.IntVar(&opt.workers, "workers", defaultWorkers,
		"How many issues to score at once")
	flag.StringVar(&opt.questions, "questions", "",
		"Path to a questions.yml, overriding the copy baked into this binary")
	flag.StringVar(&opt.dataDir, "data", "data",
		"Where to write the markdown report")
	flag.BoolVar(&opt.stdout, "stdout", false,
		"Print the report instead of writing it to a file")
	flag.IntVar(&opt.explain, "explain", 0,
		"Print everything behind one issue's bucket, by issue number, and exit")
	flag.BoolVar(&opt.version, "version", false, "Print the version and exit")

	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), usageText)
		flag.PrintDefaults()
	}
	// Go's flag package stops parsing at the first argument that is not a flag,
	// so `issue-triage owner/repo -data out` would take the repository and drop
	// -data in silence. Pulling the repository out first means both orders work,
	// and the natural one is to name the repository before the flags.
	repo, rest := splitRepo(os.Args[1:], valueFlagsOf(flag.CommandLine))
	opt.repo = repo
	if err := flag.CommandLine.Parse(rest); err != nil {
		os.Exit(2)
	}
	return opt
}

// splitRepo separates the repository argument from the flags.
//
// It returns the first bare `owner/repo` token and everything else in order. A
// token following a flag that takes a value is skipped, so `-data a/b` does not
// get mistaken for a repository.
//
// takesValue is passed in rather than read from the global flag set, because
// reading it would make this silently depend on running after every flag is
// declared. A reordering would then break argument handling with no test able
// to see it.
func splitRepo(
	args []string,
	takesValue func(string) bool,
) (string, []string) {
	repo := ""
	rest := make([]string, 0, len(args))

	for index := 0; index < len(args); index++ {
		arg := args[index]
		if strings.HasPrefix(arg, "-") {
			rest = append(rest, arg)
			// A flag written as -name=value carries its value already. One written
			// as -name value takes the next token, which must not be read as the
			// repository.
			if !strings.Contains(arg, "=") && takesValue(strings.TrimLeft(arg, "-")) &&
				index+1 < len(args) {
				index++
				rest = append(rest, args[index])
			}
			continue
		}
		if repo == "" && strings.Contains(arg, "/") {
			repo = arg
			continue
		}
		rest = append(rest, arg)
	}
	return repo, rest
}

// valueFlagsOf returns a predicate saying whether a flag name takes a value.
//
// Built from the flag set itself, so adding a flag cannot break argument
// splitting. A bool flag is the only kind that does not take a following
// argument.
func valueFlagsOf(set *flag.FlagSet) func(string) bool {
	wanted := map[string]bool{}
	set.VisitAll(func(f *flag.Flag) {
		asBool, ok := f.Value.(interface{ IsBoolFlag() bool })
		wanted[f.Name] = !ok || !asBool.IsBoolFlag()
	})
	return func(name string) bool { return wanted[name] }
}

// main parses the flags and dispatches.
func main() {
	opt := parseFlags()

	if opt.version {
		fmt.Printf("issue-triage %s\n", version)
		return
	}
	if opt.repo == "" {
		flag.Usage()
		fail("name a repository to triage, as owner/repo")
	}
	switch opt.state {
	case "OPEN", "CLOSED":
	default:
		fail(fmt.Sprintf("-state is %q, and has to be OPEN or CLOSED", opt.state))
	}

	if err := run(opt); err != nil {
		fail(err.Error())
	}
}

// fail prints to standard error and exits non-zero.
//
// Standard error rather than standard output, so a caller piping the report
// into a file still sees what went wrong.
func fail(message string) {
	fmt.Fprintln(os.Stderr, "issue-triage: "+message)
	os.Exit(1)
}

// run is the whole flow: load, fetch, score, bucket, report.
func run(opt options) error {
	started := time.Now()

	p, err := loadPayload(opt.questions)
	if err != nil {
		return err
	}

	issues, total, err := fetchIssues(opt.repo, opt.state, opt.limit)
	if err != nil {
		return err
	}
	if len(issues) == 0 {
		return fmt.Errorf("%s has no %s issues", opt.repo, lower(opt.state))
	}
	if opt.explain > 0 {
		return explainOne(opt, p, issues)
	}
	if opt.sample > 0 && opt.sample < len(issues) {
		issues = sampleOf(issues, opt.sample, opt.seed)
		fmt.Fprintf(os.Stderr, "issue-triage: sampled %d of %d with seed %d\n",
			len(issues), total, opt.seed)
	}

	key, source, err := apiKey()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr,
		"issue-triage %s: %d issues, %d questions then 1 decision each, key from %s\n",
		version, len(issues), len(p.Stage1), source,
	)

	now := time.Now().UTC()
	results := runAll(key, p, issues, now, opt.workers, progressTo(os.Stderr))
	fmt.Fprintln(os.Stderr)

	judged := make([]judgment, 0, len(results))
	var failures []string
	tokens := 0
	for _, r := range results {
		if r.Err != nil {
			failures = append(failures, r.Err.Error())
			continue
		}
		judged = append(judged, r.Judgment)
		tokens += r.Judgment.InputTokens
	}
	if len(judged) == 0 {
		return fmt.Errorf("every issue failed to score, first: %s",
			firstOr(failures, "no issues"))
	}
	reportFailures(failures)
	printDistribution(judged)

	in := reportInput{
		Repo:        opt.repo,
		State:       opt.state,
		TotalIssues: total,
		Scored:      len(judged),
		Failed:      len(failures),
		InputTokens: tokens,
		CostUSD:     float64(tokens) * p.Set.InputUSDPerMillion / 1e6,
		Model:       p.Set.Model,
		RanAt:       now,
		Elapsed:     time.Since(started),
	}
	buckets := bucket(judged)

	if opt.stdout {
		fmt.Print(renderMarkdown(in, buckets))
		return nil
	}
	path, err := writeMarkdown(in, buckets, opt.dataDir)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "issue-triage: wrote %s\n", path)
	return nil
}

// explainOne prints everything behind one issue's bucket.
//
// The report says which signal put an issue where, in three short phrases. This
// prints the whole of it: every stage-one answer, the exact digest stage two
// read, and the score that came back. It is what to reach for when a bucket
// looks wrong and the reasons column is not enough to say why.
func explainOne(opt options, p payload, issues []issue) error {
	var found *issue
	for index := range issues {
		if issues[index].Number == opt.explain {
			found = &issues[index]
			break
		}
	}
	if found == nil {
		return fmt.Errorf(
			"no %s issue #%d in %s. It may be in the other state, or past -limit",
			lower(opt.state), opt.explain, opt.repo,
		)
	}

	key, _, err := apiKey()
	if err != nil {
		return err
	}
	j, err := triage(key, p, *found, time.Now().UTC())
	if err != nil {
		return err
	}

	fmt.Printf("#%d  %s\n\n", j.Issue.Number, j.Issue.Title)

	fmt.Println("stage 1, asked about the issue text")
	for _, s := range p.Stage1 {
		if a, ok := j.Stage1[s.ID]; ok {
			fmt.Printf("  %-22s %s\n", s.Q.Label, renderAnswer(a))
		}
	}

	fmt.Println("\nstage 2 read exactly this, and no body")
	for _, line := range strings.Split(strings.TrimRight(j.Digest, "\n"), "\n") {
		fmt.Printf("  %s\n", line)
	}

	fmt.Printf("\nstage 2 answered %s %.2f of %d, which is %q\n",
		p.Decision.ID, j.Horizon, len(horizons)-1, horizons[j.HorizonIndex])
	if j.Undecided {
		fmt.Printf("  that sits within %.2f of a bucket edge, so it may move on a rerun\n", deadband)
	}
	fmt.Printf("  reasons the report would print: %s\n", joinWith(reasons(j), ", "))
	fmt.Printf("  %d input tokens across both calls, %d ms\n", j.InputTokens, j.LatencyMS)
	return nil
}

// printDistribution shows where the horizon scores landed.
//
// Four rubric levels give four buckets by rounding, which only works while the
// scores actually spread across the levels. If a real queue comes back bunched
// inside one level, rounding dumps everything into one bucket and the cuts need
// revisiting, so the shape goes to standard error on every run.
func printDistribution(judged []judgment) {
	counts := make([]int, len(horizons))
	undecided := 0
	for _, j := range judged {
		counts[j.HorizonIndex]++
		if j.Undecided {
			undecided++
		}
	}

	fmt.Fprint(os.Stderr, "issue-triage: horizon scores landed ")
	parts := make([]string, 0, len(horizons))
	for index, count := range counts {
		parts = append(parts, fmt.Sprintf("%s %d", lower(horizons[index]), count))
	}
	fmt.Fprintln(os.Stderr, joinWith(parts, ", "))

	if undecided > 0 {
		fmt.Fprintf(os.Stderr,
			"issue-triage: %d score(s) sit within %.2f of a bucket edge and may move on a rerun\n",
			undecided, deadband)
	}
	for index, count := range counts {
		if len(judged) >= 10 && count >= (len(judged)*3)/4 {
			fmt.Fprintf(os.Stderr,
				"issue-triage: %d of %d scored into one bucket (%s), so rounding is not "+
					"separating this queue and the rubric wording is worth a look\n",
				count, len(judged), lower(horizons[index]))
		}
	}
}

// reportFailures prints the issues that did not come back.
//
// A run that scored 104 of 106 is still useful, and saying so beats exiting on
// the first timeout. Identical errors collapse, because one expired key produces
// a hundred copies of one message.
func reportFailures(failures []string) {
	if len(failures) == 0 {
		return
	}
	counts := map[string]int{}
	for _, message := range failures {
		counts[message]++
	}
	fmt.Fprintf(os.Stderr, "%d issue(s) failed:\n", len(failures))
	for _, message := range sortedKeys(counts) {
		fmt.Fprintf(os.Stderr, "  %dx %s\n", counts[message], trimTo(message, 160))
	}
}

// progressTo returns a callback that keeps one line up to date on a stream.
//
// The carriage return rewrites the line rather than scrolling a hundred of them,
// and it goes to standard error so it never lands in piped output.
func progressTo(stream *os.File) func(int, int) {
	return func(done, total int) {
		fmt.Fprintf(stream, "\r  scored %d of %d", done, total)
	}
}

// firstOr returns the first element, or a fallback when there is none.
func firstOr(values []string, fallback string) string {
	if len(values) == 0 {
		return fallback
	}
	return values[0]
}

// lower lowercases a string without pulling in the whole strings package at
// every call site.
func lower(text string) string {
	out := []rune(text)
	for index, char := range out {
		if char >= 'A' && char <= 'Z' {
			out[index] = char + 32
		}
	}
	return string(out)
}

// joinWith joins strings with a separator.
func joinWith(parts []string, separator string) string {
	out := ""
	for index, part := range parts {
		if index > 0 {
			out += separator
		}
		out += part
	}
	return out
}
