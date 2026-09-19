// Command agents-md-readiness scores an AGENTS.md or CLAUDE.md against the
// agents.md format in one Jev call.
//
// It is the Python sample beside it, compiled for a CI job that wants a
// readiness gate without installing Python, uv or an SDK. The binary bakes in
// questions.yml and talks to the Jev HTTP API directly, so a download is the
// whole install.
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

// Exit codes. A gate failure differs from a usage error, so CI can tell a thin
// AGENTS.md from a broken invocation.
const (
	exitOK    = 0
	exitError = 1
	exitGate  = 2
)

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

type options struct {
	target           string
	failUnder        float64
	failOnCredential bool
	reportDir        string
	questionsPath    string
	verbose          bool
}

func main() {
	log.SetFlags(0)

	opts := options{}
	flag.Float64Var(&opts.failUnder, "fail-under", 0, "exit 2 when readiness falls below this, or when no file is found")
	flag.BoolVar(&opts.failOnCredential, "fail-on-credential", false, "exit 2 when the credential check reads suspect or worse")
	flag.StringVar(&opts.reportDir, "json", "", "write the JSON report into this directory")
	flag.StringVar(&opts.questionsPath, "questions", "", "read the payload from this file instead of the baked-in copy")
	flag.BoolVar(&opts.verbose, "verbose", false, "print the raw JSON the API returned")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Usage = printUsage
	flag.Parse()

	if *showVersion {
		fmt.Printf("agents-md-readiness %s\n", version)
		return
	}
	if flag.NArg() > 0 {
		opts.target = flag.Arg(0)
	}

	code, err := run(opts)
	if err != nil {
		log.Printf("%v", err)
	}
	os.Exit(code)
}

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

func run(opts options) (int, error) {
	set, specs, err := loadPayload(opts.questionsPath)
	if err != nil {
		return exitError, err
	}

	doc, err := loadDocument(opts.target)
	if err != nil {
		return exitError, err
	}

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

	key, from, err := apiKey()
	if err != nil {
		return exitError, err
	}
	if from != "environment" {
		log.Printf("read %s from %s", apiKeyEnv, from)
	}

	res, raw, latency, err := ask(key, set, specs, doc)
	if err != nil {
		return exitError, err
	}

	if opts.verbose {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, raw, "", "  "); err == nil {
			fmt.Printf("\nRaw response:\n%s\n", pretty.String())
		}
	}

	ready := readinessOf(specs, res.Answers)
	printAnswers(doc, specs, res, ready, latency, set)

	if err := writeIfAsked(opts.reportDir, doc, scoredReport(doc, specs, res, ready, latency, set)); err != nil {
		return exitError, err
	}

	return gate(opts, specs, res.Answers, ready)
}

// writeIfAsked writes the report when -json named a directory, and says where.
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
func gate(opts options, specs []spec, answers map[string]answer, ready float64) (int, error) {
	if opts.failOnCredential {
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
	if opts.failUnder > 0 && ready < opts.failUnder {
		return exitGate, fmt.Errorf("readiness %.2f is under the %.2f bar", ready, opts.failUnder)
	}
	return exitOK, nil
}
