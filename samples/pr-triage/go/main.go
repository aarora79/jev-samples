// pr-triage: sort a repository's pull requests into review tiers, one Jev call
// each.
//
// One binary does both halves. It fetches the pull requests from the GitHub API,
// which can be github.com or a GitHub Enterprise Server host, then asks Jev
// eleven questions about each one and prints the triage. Every input arrives as a
// flag or an environment variable, so a skill or a CI job can hand it everything
// it needs and no config file has to sit next to the binary.
//
// The Python sample one directory up is the canonical implementation. This port
// exists so a machine with no Python can run the same check from one file.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Exit codes, so a caller can tell the outcomes apart without parsing text.
const (
	// exitOK means the run finished and nothing tripped a gate.
	exitOK = 0
	// exitError means the run could not finish: a bad flag, no key, an API that
	// refused, a dataset that would not parse.
	exitError = 1
	// exitGate means the run finished and a gate fired, so a CI job should fail.
	exitGate = 2
)

// jevKeyEnv is where the environment carries the TypeSafe key.
const jevKeyEnv = "TYPESAFE_API_KEY"

// version is the release this binary was built from. build.sh sets it with
// -ldflags "-X main.version=..." and a plain `go build` leaves it as dev, so
// -version always answers.
var version = "dev"

// quiet silences the progress lines when -quiet is set. A skill that parses this
// binary's output wants the report and nothing else.
var quiet bool

// options holds everything the command line and the environment supply.
type options struct {
	// target is `owner/repo` or a URL naming one. Empty when -dataset is set.
	target string
	// dataset replays a dataset file instead of fetching, which needs no GitHub
	// token and costs only the Jev calls.
	dataset string
	// state picks which pull requests to list: open, closed or all.
	state string
	// limit keeps the most recent N. 0 means no limit.
	limit int
	// all clears the limit, which reads better in a command line than -limit 0.
	all bool
	// since keeps pull requests opened on or after a date, given as YYYY-MM-DD or
	// as a number of days to go back.
	since string
	// out is the directory the dataset and the reports land in.
	out string
	// apiBase overrides the GitHub API root, which is how this talks to a GitHub
	// Enterprise Server host.
	apiBase string
	// token is a GitHub token, for callers that would rather pass it than export
	// it.
	token string
	// jevKey is the TypeSafe key, for the same reason.
	jevKey string
	// questions points at a payload file to use instead of the baked-in copy.
	questions string
	// explain prints every question and contribution for one pull request.
	explain int
	// failUnder exits 2 when any pull request lands in a tier at or above this
	// one, so a CI job can fail on a queue that needs attention.
	failOnTier string
	// fetchOnly stops after writing the dataset, so the GitHub half can run
	// somewhere with no TypeSafe key.
	fetchOnly bool
	// quiet silences progress lines.
	quiet bool
	// showVersion prints the release and exits, which install.sh calls to confirm
	// the download landed.
	showVersion bool
}

// main parses the flags and hands off, so the control flow stays readable and
// every step that can fail returns an error to one place.
func main() {
	opts, err := parseFlags()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitError)
	}
	quiet = opts.quiet

	code, err := run(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		// A gate that fired returns its own code alongside the reason it fired, and
		// that code has to survive. Only a failure with no code of its own becomes
		// the generic error exit.
		if code == exitOK {
			code = exitError
		}
	}
	os.Exit(code)
}

// run does the work and returns the exit code.
//
// Keeping the body here rather than in main means every failure travels back as
// an error and one place decides what to print and which code to leave with.
func run(opts options) (int, error) {
	set, specs, err := loadPayload(opts.questions)
	if err != nil {
		return exitError, err
	}

	data, err := gatherDataset(opts)
	if err != nil {
		return exitError, err
	}

	if opts.fetchOnly {
		return exitOK, nil
	}

	key := opts.jevKey
	if strings.TrimSpace(key) == "" {
		key = strings.TrimSpace(os.Getenv(jevKeyEnv))
	}
	if key == "" {
		return exitError, fmt.Errorf(
			"no TypeSafe key: pass -jev-key or set %s", jevKeyEnv,
		)
	}

	return triage(opts, data, set, specs, key)
}

// gatherDataset either replays a dataset file or fetches a fresh one.
//
// A fetch writes the dataset to disk before any Jev call, so a run that dies
// halfway leaves the expensive half of the work behind for a replay.
func gatherDataset(opts options) (dataset, error) {
	if opts.dataset != "" {
		logf("replaying %s", opts.dataset)
		return loadDataset(opts.dataset)
	}

	tgt, err := parseTarget(opts.target, opts.apiBase)
	if err != nil {
		return dataset{}, err
	}
	logf("repository %s through %s", tgt.Repo, tgt.APIBase)

	since, err := parseSince(opts.since)
	if err != nil {
		return dataset{}, err
	}

	token := resolveToken(opts.token)
	data, err := buildDataset(tgt, token, opts.state, opts.limit, since)
	if err != nil {
		return dataset{}, err
	}
	if len(data.PullRequests) == 0 {
		return dataset{}, fmt.Errorf(
			"no %s pull requests matched, so there is nothing to triage", opts.state,
		)
	}

	if err := os.MkdirAll(opts.out, 0o755); err != nil {
		return dataset{}, fmt.Errorf("making %s: %w", opts.out, err)
	}
	path := filepath.Join(
		opts.out,
		fmt.Sprintf("%s-%s.json", repoSlug(tgt.Repo), selectorSlug(opts.state, opts.limit, since)),
	)
	if err := writeJSON(path, data); err != nil {
		return dataset{}, err
	}
	logf("wrote %d pull requests to %s", len(data.PullRequests), path)
	return data, nil
}

// parseSince reads an ISO date, or a plain day count to go back from today.
//
// Both forms return YYYY-MM-DD, because that is what the date comparison in
// fetch.go works on and what the dataset records.
func parseSince(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}

	// A plain number means days. strconv.Atoi fails on anything else, which is how
	// the two forms are told apart.
	if days, err := strconv.Atoi(trimmed); err == nil {
		if days < 0 {
			return "", fmt.Errorf("-since wants a positive number of days, got %q", value)
		}
		// UTC, because the created_at timestamps this gets compared against are
		// UTC. A local midnight would move the cutoff by a day either side.
		cutoff := time.Now().UTC().AddDate(0, 0, -days)
		return cutoff.Format("2006-01-02"), nil
	}

	// Go names date layouts by example, and 2006-01-02 is its way of writing
	// YYYY-MM-DD. Parse fails on a date that does not exist, such as 2026-02-30.
	parsed, err := time.Parse("2006-01-02", trimmed)
	if err != nil {
		return "", fmt.Errorf("-since wants YYYY-MM-DD or a number of days, got %q", value)
	}
	return parsed.Format("2006-01-02"), nil
}

// parseFlags reads the command line and returns the options, or an error the
// caller prints.
func parseFlags() (options, error) {
	var opts options

	flag.StringVar(&opts.dataset, "dataset", "", "triage this dataset file instead of fetching")
	flag.StringVar(&opts.state, "state", "open", "which pull requests to list: open, closed or all")
	flag.IntVar(&opts.limit, "limit", defaultLimit, "keep the most recent N pull requests")
	flag.BoolVar(&opts.all, "all", false, "keep every pull request the other selectors allow")
	flag.StringVar(&opts.since, "since", "", "keep pull requests opened on or after YYYY-MM-DD, or a number of days back")
	flag.StringVar(&opts.out, "out", "data", "directory for the dataset and the reports")
	flag.StringVar(&opts.apiBase, "api-base", "", "GitHub API root, for a GitHub Enterprise Server host")
	flag.StringVar(&opts.token, "token", "", "GitHub token, otherwise GITHUB_TOKEN, GH_TOKEN, GH_ENTERPRISE_TOKEN or the gh CLI")
	flag.StringVar(&opts.jevKey, "jev-key", "", "TypeSafe key, otherwise "+jevKeyEnv)
	flag.StringVar(&opts.questions, "questions", "", "payload file to use instead of the copy baked into this binary")
	flag.IntVar(&opts.explain, "explain", 0, "print every question and contribution for one pull request number")
	flag.StringVar(&opts.failOnTier, "fail-on-tier", "", "exit 2 when any pull request lands in this tier or above: low, medium or high")
	flag.BoolVar(&opts.fetchOnly, "fetch-only", false, "write the dataset and stop, which needs no TypeSafe key")
	flag.BoolVar(&opts.quiet, "quiet", false, "print the report without the progress lines")
	flag.BoolVar(&opts.showVersion, "version", false, "print the version and exit")

	flag.Usage = printUsage
	flag.Parse()

	// Go's flag package stops parsing at the first argument that is not a flag, so
	// `pr-triage apache/airflow -all` would otherwise leave -all sitting unread as
	// a positional argument. Taking the repository and parsing what follows it, for
	// as long as anything is left, lets the flags sit on either side of it, which is
	// the order people type.
	if opts.showVersion {
		fmt.Printf("pr-triage %s\n", version)
		os.Exit(exitOK)
	}

	args := flag.Args()
	for len(args) > 0 {
		if opts.target != "" {
			return opts, fmt.Errorf("expected one repository, got %q as well", args[0])
		}
		opts.target = args[0]
		if err := flag.CommandLine.Parse(args[1:]); err != nil {
			return opts, err
		}
		args = flag.Args()
	}

	// -all is sugar for no limit, and it wins over -limit so the two can appear
	// together without the reader having to know the order.
	if opts.all {
		opts.limit = 0
	}

	switch opts.state {
	case "open", "closed", "all":
	default:
		return opts, fmt.Errorf("-state wants open, closed or all, got %q", opts.state)
	}

	if opts.failOnTier != "" && tierIndex(opts.failOnTier) < 0 {
		return opts, fmt.Errorf("-fail-on-tier wants trivial, low, medium or high, got %q", opts.failOnTier)
	}

	switch {
	case opts.dataset != "" && opts.target != "":
		return opts, fmt.Errorf("pass a repository or -dataset, not both")
	case opts.dataset == "" && opts.target == "":
		return opts, fmt.Errorf("name a repository as owner/repo or a URL, or pass -dataset")
	}

	return opts, nil
}

// printUsage prints the help. flag's default listing covers the flags, and the lines
// here cover what a reader cannot guess: the shapes of a command and where the
// credentials come from.
func printUsage() {
	fmt.Fprint(os.Stderr, `pr-triage: sort a repository's pull requests into review tiers, one Jev call each.

Usage:
  pr-triage [flags] owner/repo
  pr-triage [flags] https://github.com/owner/repo
  pr-triage [flags] https://ghe.example.com/owner/repo
  pr-triage -dataset data/owner-repo-open-all.json

Examples:
  pr-triage apache/airflow                      # the 10 most recent open ones
  pr-triage apache/airflow -all                 # every open one
  pr-triage apache/airflow -since 30            # opened in the last 30 days
  pr-triage apache/airflow -since 2026-08-01    # opened on or after a date
  pr-triage apache/airflow -explain 73713       # one pull request in full
  pr-triage apache/airflow -fetch-only          # build the dataset, skip Jev
  pr-triage apache/airflow -fail-on-tier high   # exit 2 if anything lands high

Credentials, each read from the flag first and the environment second:
  GitHub   -token     GITHUB_TOKEN, GH_TOKEN, GH_ENTERPRISE_TOKEN, or the gh CLI
  TypeSafe -jev-key   TYPESAFE_API_KEY

GitHub Enterprise Server:
  Pass a URL on your host and the API base follows it, or set it outright with
  -api-base https://ghe.example.com/api/v3, or export GITHUB_API_URL.

Flags:
`)
	flag.PrintDefaults()
}

// logf prints one progress line to standard error, so the report on standard
// output stays clean enough to pipe.
func logf(format string, args ...any) {
	if quiet {
		return
	}
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}
