// Reading the document under test, and finding the API key.
package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	rawPrefix    = "https://raw.githubusercontent.com/"
	fetchTimeout = 20 * time.Second
	notFound     = 404
	// envParentLevels is how far up from the working directory to look for a
	// .env file, which covers a CI checkout inside a couple of wrappers.
	envParentLevels = 5
)

// defaultTargets are the two names an instruction file goes by, tried in order
// when the target names a directory or a GitHub repo rather than one file.
var defaultTargets = []string{"AGENTS.md", "CLAUDE.md"}

// document is one instruction file, or the absence of one.
type document struct {
	Name   string
	Source string
	Text   string
	Found  bool
	Looked []string
}

// loadDocument reads the file named by target: a path, an https URL, a GitHub
// repo root, or the working directory when target is empty.
//
// A repo or directory holding neither name comes back with Found false, which
// scores nothing rather than failing, so a loop over many repos keeps running.
func loadDocument(target string) (document, error) {
	if target == "" {
		for _, name := range defaultTargets {
			if _, err := os.Stat(name); err == nil {
				target = name
				break
			}
		}
		if target == "" {
			looked := make([]string, 0, len(defaultTargets))
			for _, name := range defaultTargets {
				abs, _ := filepath.Abs(name)
				looked = append(looked, abs)
			}
			return document{Source: looked[len(looked)-1], Looked: looked}, nil
		}
	}

	if strings.HasPrefix(target, "http://") {
		return document{}, errors.New("https only: point it at the https:// URL instead")
	}

	if !strings.HasPrefix(target, "https://") {
		text, err := os.ReadFile(target)
		if err != nil {
			return document{}, fmt.Errorf("no file at %s", target)
		}
		abs, _ := filepath.Abs(target)
		return document{
			Name:   filepath.Base(target),
			Source: abs,
			Text:   string(text),
			Found:  true,
			Looked: []string{abs},
		}, nil
	}

	candidates := rawCandidates(target)
	for _, candidate := range candidates {
		text, code, err := fetch(candidate)
		switch {
		case err != nil:
			return document{}, err
		case code == notFound:
			continue
		case code != http.StatusOK:
			return document{}, fmt.Errorf("%s returned %d", candidate, code)
		}
		return document{
			Name:   candidate[strings.LastIndex(candidate, "/")+1:],
			Source: candidate,
			Text:   text,
			Found:  true,
			Looked: []string{candidate},
		}, nil
	}
	return document{Source: candidates[len(candidates)-1], Looked: candidates}, nil
}

// rawCandidates lists the raw URLs worth trying for one GitHub web URL: one for
// a file page, one per default name for a repo root.
func rawCandidates(target string) []string {
	if !strings.Contains(target, "github.com") {
		return []string{target}
	}

	if strings.Contains(target, "/blob/") {
		raw := strings.Replace(target, "github.com", "raw.githubusercontent.com", 1)
		return []string{strings.Replace(raw, "/blob/", "/", 1)}
	}

	parts := strings.Split(strings.TrimSuffix(target, "/"), "/")
	if len(parts) == 5 { // https://github.com/owner/repo
		owner, repo := parts[3], parts[4]
		out := make([]string, 0, len(defaultTargets))
		for _, name := range defaultTargets {
			out = append(out, fmt.Sprintf("%s%s/%s/HEAD/%s", rawPrefix, owner, repo, name))
		}
		return out
	}
	return []string{target}
}

// fetch gets one URL, returning its body and status.
func fetch(target string) (string, int, error) {
	if _, err := url.Parse(target); err != nil {
		return "", 0, fmt.Errorf("bad URL %s: %w", target, err)
	}

	client := &http.Client{Timeout: fetchTimeout}
	response, err := client.Get(target)
	if err != nil {
		return "", 0, fmt.Errorf("fetching %s: %w", target, err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return "", response.StatusCode, nil
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", response.StatusCode, fmt.Errorf("reading %s: %w", target, err)
	}
	return string(body), response.StatusCode, nil
}

// apiKey returns the key and where it came from: the environment first, then a
// .env file in the working directory or its parents.
func apiKey() (string, string, error) {
	if key := os.Getenv(apiKeyEnv); key != "" {
		return key, "environment", nil
	}

	dir, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	for range envParentLevels {
		candidate := filepath.Join(dir, ".env")
		if key := keyFromFile(candidate); key != "" {
			return key, candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", "", fmt.Errorf("%s is not set, and no .env here or above sets it", apiKeyEnv)
}

// keyFromFile pulls the key out of one .env file, taking NAME=value and
// export NAME=value, with or without quotes.
func keyFromFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimPrefix(strings.TrimSpace(line), "export ")
		name, value, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(name) != apiKeyEnv {
			continue
		}
		return strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return ""
}
