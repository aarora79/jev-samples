// Command agents-md-readiness scores an AGENTS.md or CLAUDE.md against the
// agents.md format in one Jev call.
//
// It is the Python sample beside it, compiled for a CI job that wants a
// readiness gate without installing Python, uv or an SDK. The binary bakes in
// questions.yml and talks to the Jev HTTP API directly, so a download is the
// whole install.
//
// Downloading that one file is the install: mark it executable and run it. The
// questions, the weights, the model pin and the HTTP client sit inside the
// binary, so a CI image picks up the check in one line, and nothing on the
// machine has to match a version.
//
//	agents-md-readiness                                   # ./AGENTS.md or ./CLAUDE.md
//	agents-md-readiness path/to/CLAUDE.md                 # any local file
//	agents-md-readiness https://github.com/apache/airflow # a GitHub repo
//	agents-md-readiness -fail-under 0.8 AGENTS.md         # exit 2 under the bar
//	agents-md-readiness -questions ours.yml AGENTS.md     # your own questions
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

// Exit codes. A CI job reads the number this program exits with and decides what
// to do: 0 means the run finished and the next step goes ahead, 1 means the run
// itself broke before it could judge anything (no key, no network, a malformed
// questions.yml), and 2 means a document was judged and came in under the bar the
// caller set. A gate failure differs from a usage error because each one asks for
// different work: 2 asks for a better AGENTS.md, 1 asks for a fixed pipeline.
//
// Go has no enum type, so a const block of named integers stands in. The names
// carry the meaning at the return sites, where a bare 2 would carry none.
const (
	exitOK    = 0
	exitError = 1
	exitGate  = 2
)

// version is what -version prints. A release build overwrites it by passing
// -ldflags "-X main.version=0.1.0" to the compiler, which rewrites this string
// inside the finished binary, so the built artifact reports its own version while
// a plain `go build` keeps saying "dev". The -X flag only reaches a string
// variable at package level, which is why this stays a var.
var version = "dev"

// options holds everything the flags collect. Gathering them in one struct lets
// main hand the whole set to run as a single value.
//
// target is the path or URL to score, and empty means "look in the working
// directory". failUnder is the readiness bar, 0 to 1. Go fills a field nobody
// sets with its zero value, so failUnder of 0 means the gate is off.
// failOnCredential turns a suspected credential in the document into a gate
// failure. reportDir is the directory to write the JSON report into, empty
// meaning the caller asked for no report. questionsPath names a questions.yml to
// read instead of the baked-in copy, empty meaning the baked-in copy. verbose
// prints the raw JSON body the API sent back.
type options struct {
	target           string
	failUnder        float64
	failOnCredential bool
	reportDir        string
	questionsPath    string
	verbose          bool
}

// main registers the flags, picks up the positional target, and turns what run
// reports into the process exit code. Go starts every program by calling it.
func main() {
	// log writes to stderr. Clearing the flags drops the date and time prefix Go
	// adds by default, so one failure reads as one plain line in a CI log.
	log.SetFlags(0)

	// Each flag.XxxVar call registers one flag and takes the address of the
	// variable to fill, which is what the & means: the flag package writes back
	// through that pointer while it parses the command line. Their arguments run
	// flag name, default, help line, so -fail-under defaults to 0, the value that
	// leaves the gate off.
	//
	// flag.Bool has no Var in its name, so it allocates the variable itself and
	// hands back a pointer to it. That is why showVersion is read as *showVersion
	// below: the * follows the pointer to the value it points at.
	opts := options{}
	flag.Float64Var(&opts.failUnder, "fail-under", 0, "exit 2 when readiness falls below this, or when no file is found")
	flag.BoolVar(&opts.failOnCredential, "fail-on-credential", false, "exit 2 when the credential check reads suspect or worse")
	flag.StringVar(&opts.reportDir, "json", "", "write the JSON report into this directory")
	flag.StringVar(&opts.questionsPath, "questions", "", "read the payload from this file instead of the baked-in copy")
	flag.BoolVar(&opts.verbose, "verbose", false, "print the raw JSON the API returned")
	showVersion := flag.Bool("version", false, "print the version and exit")

	// flag.Usage is the function the flag package calls for -h and for a flag it
	// does not recognise, so pointing it at printUsage puts this sample's own help
	// text and examples there. flag.Parse then reads the command line and fills in
	// every variable registered above, which is why every read of opts and
	// showVersion sits below it.
	flag.Usage = printUsage
	flag.Parse()

	// -version answers and stops here, so asking a binary what it is needs no key,
	// no network and no document.
	if *showVersion {
		fmt.Printf("agents-md-readiness %s\n", version)
		return
	}

	// Whatever survives flag parsing is positional. flag.NArg counts those
	// leftovers and flag.Arg(0) is the first one, so an invocation with no
	// argument leaves opts.target empty and run looks in the working directory.
	if flag.NArg() > 0 {
		opts.target = flag.Arg(0)
	}

	// run hands back two values, an exit code and an error. Go has no exceptions:
	// a function reports trouble by returning an error next to its result, and the
	// caller compares that error against nil. Both matter here, since a gate
	// failure carries a code and a reason to print.
	//
	// os.Exit sets the status a CI job reads. Returning from main always exits 0,
	// which would bury a gate failure, so main ends the process itself.
	code, err := run(opts)
	if err != nil {
		log.Printf("%v", err)
	}
	os.Exit(code)
}

// printUsage prints the help text. flag.Usage points at it, so -h, -help and a
// misspelled flag all arrive here.
//
// It writes to os.Stderr, the stream a program uses for messages about itself,
// which keeps help text out of a pipe that expects the report. The text sits in a
// raw string literal, quoted with backticks instead of double quotes: a raw
// literal keeps every newline and every space as typed, so the layout below is
// the layout a reader gets, with no \n escapes in the way. flag.PrintDefaults
// fills the flag list into the gap between the two halves, from what main
// registered.
func printUsage() {
	fmt.Fprintf(os.Stderr, `agents-md-readiness %s

Score an AGENTS.md or CLAUDE.md against the agents.md format in one Jev call.

Usage:
  agents-md-readiness [flags] [path | https URL | GitHub repo URL]

With no argument it reads ./AGENTS.md, then ./CLAUDE.md. A GitHub repo root tries
the same two names. A path or a GitHub file page reads whatever it names. A repo
holding neither scores nothing and exits 0, or exits 2 under -fail-under.

Flags:
`, version)
	flag.PrintDefaults()
	fmt.Fprintf(os.Stderr, `
The key comes from %s, or from a .env file in the working directory or one
of its parents. The questions, weights, model pin and price are baked in; pass
-questions to score against your own copy of questions.yml.

Exit codes: 0 scored, 1 error, 2 gate failed.
`, apiKeyEnv)
}

// run does the work and reports the exit code it earned, leaving main as the only
// place that ends the process.
//
// The order matters. The payload loads first, so a typo in questions.yml fails
// before the program spends a network call. The document comes next, so a repo
// holding neither name costs no tokens and needs no key. The key follows, then
// one API call, then the arithmetic on what came back, then the printing, then
// the JSON report, and the gate last, because the gate reads numbers every
// earlier step produced.
func run(opts options) (int, error) {
	// set carries the model pin, the state budget and the price; specs carries the
	// questions in the order the file lists them, which is also print order. Go
	// returns several values at once, and := declares all three names here.
	set, specs, err := loadPayload(opts.questionsPath)
	if err != nil {
		return exitError, err
	}

	// loadDocument takes any shape of target: a local path, an https URL, a GitHub
	// repo root, a GitHub file page, or the working directory when target is empty.
	doc, err := loadDocument(opts.target)
	if err != nil {
		return exitError, err
	}

	// Found false means the target was reachable and held neither AGENTS.md nor
	// CLAUDE.md. That counts as a finding, so the sample prints it, writes the
	// report when asked, and returns before it needs a key or spends a token. A
	// caller that set -fail-under asked for a missing file to fail the job, so that
	// caller gets exit 2; everyone else gets 0, which keeps a loop over many repos
	// running.
	if !doc.Found {
		printMissing(doc)
		if err := writeIfAsked(opts.reportDir, doc, missingReport(doc)); err != nil {
			return exitError, err
		}
		if opts.failUnder > 0 {
			return exitGate, errors.New("no AGENTS.md or CLAUDE.md to score")
		}
		return exitOK, nil
	}

	// run reaches for the key only now, once there is a document worth paying for.
	key, from, err := apiKey()
	if err != nil {
		return exitError, err
	}

	// Naming the source helps someone who exported one key and has another sitting
	// in a .env file. The path a key came from is safe to log; the key never is.
	if from != "environment" {
		log.Printf("read %s from %s", apiKeyEnv, from)
	}

	// One request carries the document and every question. res holds the parsed
	// answers, raw the bytes as they arrived, latency the round trip in
	// milliseconds.
	res, raw, latency, err := ask(key, set, specs, doc)
	if err != nil {
		return exitError, err
	}

	// -verbose reprints the body with indentation. json.Indent fills the buffer
	// whose address it is handed, and a failure to indent leaves the run alone,
	// since a pretty copy of the response is a convenience.
	if opts.verbose {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, raw, "", "  "); err == nil {
			fmt.Printf("\nRaw response:\n%s\n", pretty.String())
		}
	}

	// Jev cannot do arithmetic, so the weighted average happens here, in code a
	// reviewer can read, and the printing works off that number.
	ready := readinessOf(specs, res.Answers)
	printAnswers(doc, specs, res, ready, latency, set)

	// The report goes out before the gate runs, so a job that fails still leaves
	// the numbers behind for whoever has to answer for them.
	if err := writeIfAsked(opts.reportDir, doc, scoredReport(doc, specs, res, ready, latency, set)); err != nil {
		return exitError, err
	}

	// The gate has the last word, choosing between exit 0 and exit 2.
	return gate(opts, specs, res.Answers, ready)
}

// writeIfAsked writes the report when -json named a directory, and says where.
//
// An empty dir means the user asked for no report, so the function returns nil,
// Go's way of saying nothing went wrong, and the caller carries on. body is
// typed any, the empty interface that accepts a value of any type, because a
// scored run and a missing file write two different shapes.
func writeIfAsked(dir string, doc document, body any) error {
	if dir == "" {
		return nil
	}
	path, err := writeJSON(dir, doc, body)
	if err != nil {
		return err
	}
	fmt.Printf("Report: %s\n", path)
	return nil
}

// gate turns the answers into an exit code, so a pull request job can fail on a
// thin file or a leaked credential.
//
// The credential check runs first. It walks the specs looking for an inverted
// question that carries weight, the one asking whether a secret leaked into the
// file, and compares the judgement word rather than the raw probability, so the
// exit code and the printed verdict can never disagree. strings.HasPrefix does
// the comparing because a credit near a band edge prints both bands, "likely
// present to suspect" for one, and a prefix still catches that.
//
// gate checks the readiness bar second, so a run that trips both reports the
// credential, the finding that needs the faster answer. answers is a map from
// question id to answer, and reading a map at a key it lacks hands back the zero
// value instead of failing, so a question the API skipped scores nothing here
// rather than stopping the gate.
func gate(opts options, specs []spec, answers map[string]answer, ready float64) (int, error) {
	if opts.failOnCredential {
		// Only an inverted question with weight guards a credential: inverted
		// means the file earns credit by keeping the thing out, and a weight of 0
		// means the payload asked the question for information alone.
		// range yields an index and a value, and _ discards the index.
		for _, s := range specs {
			if !s.Q.Invert || s.Q.Weight == 0 {
				continue
			}
			word := judgementOf(s.Q, answers[s.ID])
			if strings.HasPrefix(word, "present") || strings.HasPrefix(word, "likely present") || strings.HasPrefix(word, "suspect") {
				return exitGate, fmt.Errorf("%s reads %q", s.Q.Label, word)
			}
		}
	}

	// The bar comes second, so a credential finding owns the exit code when both
	// checks trip. A failUnder of 0 leaves this check out of the run.
	if opts.failUnder > 0 && ready < opts.failUnder {
		return exitGate, fmt.Errorf("readiness %.2f is under the %.2f bar", ready, opts.failUnder)
	}
	return exitOK, nil
}
