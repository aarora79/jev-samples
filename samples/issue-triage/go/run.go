// The key, sampling, and running many issues at once.
package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// keyEnv is where the Jev API key lives.
	keyEnv = "TYPESAFE_API_KEY"
	// defaultWorkers is how many issues to score at once. Each one is two calls
	// of about 160 ms, so eight in flight turns a 106-issue queue into well under
	// a minute without opening enough sockets to look like an attack.
	defaultWorkers = 8
)

// envFiles are the places a key file might sit, nearest first.
//
// The binary may run from anywhere, so these walk up from the working
// directory. Four levels reaches a repository root from inside
// `samples/<name>/go/`, which is where it sits when built from source.
var envFiles = []string{".env", "../.env", "../../.env", "../../../.env"}

// apiKey finds the key, preferring the environment over any file.
//
// It returns the key and where it came from. The caller logs the path and never
// the key.
func apiKey() (string, string, error) {
	if value := strings.TrimSpace(os.Getenv(keyEnv)); value != "" {
		return value, "the " + keyEnv + " environment variable", nil
	}

	for _, name := range envFiles {
		value, err := keyFromFile(name)
		if err != nil {
			return "", "", err
		}
		if value != "" {
			absolute, absErr := filepath.Abs(name)
			if absErr != nil {
				absolute = name
			}
			return value, absolute, nil
		}
	}
	return "", "", fmt.Errorf(
		"%s is not set, and no key sits in %s.\n  Export it, or put it in a .env file",
		keyEnv, strings.Join(envFiles, ", "),
	)
}

// keyFromFile reads the key out of one .env file.
//
// Handles the two shapes a key file takes in practice: `NAME=value` and
// `export NAME=value`, with or without quotes. A missing file is not an error,
// because the caller is walking a list of candidates.
func keyFromFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading %s: %w", path, err)
	}

	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		name, value, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(name) != keyEnv {
			continue
		}
		return strings.Trim(strings.TrimSpace(value), `"'`), nil
	}
	return "", nil
}

// sampleOf picks n issues at random, seeded so a spot check repeats.
//
// Seeded on purpose: an unreproducible sample cannot be compared against the
// same sample after a question is reworded, which is the whole point of
// sampling.
func sampleOf(issues []issue, n int, seed int64) []issue {
	// #nosec G404 - choosing a sample for a report, not a secret
	shuffler := rand.New(rand.NewSource(seed))
	picked := make([]issue, len(issues))
	copy(picked, issues)
	shuffler.Shuffle(len(picked), func(i, j int) {
		picked[i], picked[j] = picked[j], picked[i]
	})
	return picked[:n]
}

// scored is one issue's result, or the error that stopped it.
type scored struct {
	Judgment judgment
	Err      error
}

// runAll triages every issue, up to workers at a time.
//
// Results come back in the order the issues went in, whatever order the calls
// finished in, so two runs over one queue produce the same report.
func runAll(
	key string,
	p payload,
	issues []issue,
	now time.Time,
	workers int,
	progress func(int, int),
) []scored {
	if workers < 1 {
		workers = 1
	}
	out := make([]scored, len(issues))

	// A buffered channel used as a counting semaphore: a send takes a slot and a
	// receive gives it back, so at most `workers` goroutines are past the send at
	// any moment. WaitGroup then waits for all of them to finish.
	slots := make(chan struct{}, workers)
	var wait sync.WaitGroup
	var mu sync.Mutex
	done := 0

	for index := range issues {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			slots <- struct{}{}
			defer func() { <-slots }()

			j, err := triage(key, p, issues[i], now)
			out[i] = scored{Judgment: j, Err: err}

			// The counter is shared, so it needs the lock. Everything else each
			// goroutine touches is its own slot in out, which needs none.
			mu.Lock()
			done++
			if progress != nil {
				progress(done, len(issues))
			}
			mu.Unlock()
		}(index)
	}

	wait.Wait()
	return out
}
