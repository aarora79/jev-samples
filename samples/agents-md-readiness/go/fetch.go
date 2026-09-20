// Reading the document under test, and finding the API key.
//
// Two jobs live here. The first turns whatever the user typed on the command line into
// document text: a local path, an https URL, a GitHub repo root, or nothing at all, which
// means "read the instruction file in the directory I am standing in". The second finds the
// Jev API key.
//
// Nothing in this file talks to Jev. score.go does that, and it receives a document this
// file already resolved to text.
package main

// Go lists every package a file uses. These all ship with Go itself, so the sample needs no
// third-party HTTP client and no dotenv library.
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
	// rawPrefix is the host that serves file bytes for a GitHub repo. github.com wraps a
	// file in an HTML page, so the sample rewrites a web URL into a raw one and gets the
	// markdown instead of the page around it.
	rawPrefix = "https://raw.githubusercontent.com/"
	// fetchTimeout caps one HTTP GET. Without it a stalled server holds the whole run open.
	// Go durations carry their unit in the type, so 20 * time.Second is a value, not an int.
	fetchTimeout = 20 * time.Second
	// notFound is HTTP 404. For this sample a 404 means "that repo does not carry this
	// name", which is a step in the search, so loadDocument walks on to the next candidate.
	notFound = 404
	// envParentLevels is how far up from the working directory to look for a
	// .env file, which covers a CI checkout inside a couple of wrappers.
	envParentLevels = 5
)

// defaultTargets are the two names an instruction file goes by, tried in order
// when the target names a directory or a GitHub repo rather than one file.
//
// AGENTS.md comes first because agents.md is the vendor-neutral convention, so a repo
// carrying both names gets scored on the file every agent reads. CLAUDE.md covers a repo
// that adopted the Anthropic name and stopped there.
//
// The empty square brackets make this a slice, a growable view over an array. An array in Go
// has its length fixed in its type, and a slice carries a length that can change.
var defaultTargets = []string{"AGENTS.md", "CLAUDE.md"}

// document is one instruction file, or the absence of one.
//
// A struct groups named fields under one type. Each field here starts with a capital letter,
// which is how Go marks a name as exported, and which also lets encoding/json see the field
// when output.go writes the report.
type document struct {
	// Name is the file name on its own, AGENTS.md or CLAUDE.md, for the report header.
	Name string
	// Source is the single place the text came from: an absolute path or a raw URL. When the
	// search came up empty it holds the last place that was tried.
	Source string
	// Text is the whole file. score.go sends it to Jev as state.
	Text string
	// Found is false when the target holds no instruction file. The run scores that as
	// nothing and keeps its exit code for real errors.
	Found bool
	// Looked records every path or URL the search touched. A run that finds nothing prints
	// the list, so the reader sees where to put the file. A hit records one entry, and a
	// repo root with neither name records both candidates.
	Looked []string
}

// loadDocument reads the file named by target: a path, an https URL, a GitHub
// repo root, or the working directory when target is empty.
//
// A repo or directory holding neither name comes back with Found false, which
// scores nothing rather than failing, so a loop over many repos keeps running.
//
// The two results after the parameter list are Go's multiple return values: the document and
// an error. A caller checks the error first, and reads the document when the error is nil.
func loadDocument(target string) (document, error) {
	// Empty target means the working directory. Try each default name and keep the first one
	// that exists. os.Stat asks the operating system about a path and hands back an error
	// when the path is absent, so err == nil is the test for "this file is here". The blank
	// identifier _ throws away the file information itself, which this check does not need.
	if target == "" {
		for _, name := range defaultTargets {
			if _, err := os.Stat(name); err == nil {
				target = name
				break
			}
		}
		// Still empty, so the directory holds neither name. Report the absolute paths that
		// were tried and return Found false, the zero value of a bool in a fresh struct.
		// make with a capacity of len(defaultTargets) sizes the slice once, so append below
		// never has to copy it into a bigger array.
		if target == "" {
			looked := make([]string, 0, len(defaultTargets))
			for _, name := range defaultTargets {
				abs, _ := filepath.Abs(name)
				looked = append(looked, abs)
			}
			return document{Source: looked[len(looked)-1], Looked: looked}, nil
		}
	}

	// Refuse plain http rather than fetching it. A document pulled in the clear can be
	// rewritten in flight by anyone on the path, and this document decides a score.
	if strings.HasPrefix(target, "http://") {
		return document{}, errors.New("https only: point it at the https:// URL instead")
	}

	// No https prefix, so treat the target as a path on this machine. os.ReadFile pulls the
	// whole file into memory as bytes, and string(text) copies those bytes into a string.
	//
	// A named path that does not exist is a typo the user can fix, so it stops the run. That
	// is the opposite of a repo holding neither default name, which is a finding: the user
	// asked a real question about a real repo, and the answer is "no instruction file".
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

	// An https target, so fetch the raw URLs in order and take the first one that answers.
	// fetch returns three values, and the switch sorts them: a transport failure stops the
	// run, a 404 moves to the next candidate, any other non-200 stops the run because the
	// score would otherwise rest on a document nobody read.
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
		// The file name is whatever follows the last slash in the URL.
		return document{
			Name:   candidate[strings.LastIndex(candidate, "/")+1:],
			Source: candidate,
			Text:   text,
			Found:  true,
			Looked: []string{candidate},
		}, nil
	}
	// Every candidate answered 404, so the repo carries neither name. Found stays false.
	return document{Source: candidates[len(candidates)-1], Looked: candidates}, nil
}

// rawCandidates lists the raw URLs worth trying for one GitHub web URL: one for
// a file page, one per default name for a repo root.
func rawCandidates(target string) []string {
	// Some other host, so pass the URL through and fetch it as given.
	if !strings.Contains(target, "github.com") {
		return []string{target}
	}

	// A /blob/ URL is the page GitHub shows for one file, so the user already picked the
	// file. Swapping the host and dropping /blob/ turns
	// github.com/owner/repo/blob/main/AGENTS.md into
	// raw.githubusercontent.com/owner/repo/main/AGENTS.md. Both Replace calls pass 1 as the
	// count, so a repo or branch named blob further along the path survives untouched. One
	// file page yields exactly one candidate.
	if strings.Contains(target, "/blob/") {
		raw := strings.Replace(target, "github.com", "raw.githubusercontent.com", 1)
		return []string{strings.Replace(raw, "/blob/", "/", 1)}
	}

	// A repo root splits into five pieces: "https", "", "github.com", owner, repo. TrimSuffix
	// drops a trailing slash first, so both spellings of the root split the same way. HEAD
	// stands in for the default branch, which saves asking GitHub whether it is main or
	// master. One repo root yields one candidate per default name.
	parts := strings.Split(strings.TrimSuffix(target, "/"), "/")
	if len(parts) == 5 { // https://github.com/owner/repo
		owner, repo := parts[3], parts[4]
		out := make([]string, 0, len(defaultTargets))
		for _, name := range defaultTargets {
			out = append(out, fmt.Sprintf("%s%s/%s/HEAD/%s", rawPrefix, owner, repo, name))
		}
		return out
	}
	// A deeper github.com URL that is not a /blob/ page, a tree listing for example. Try it
	// as given and let the status code say what happened.
	return []string{target}
}

// fetch gets one URL, returning its body and status.
//
// The status comes back beside the error so the caller can tell a missing file from a broken
// one: fetch reports a non-200 as a status with a nil error, and reserves the error for a
// request that never produced a response.
func fetch(target string) (string, int, error) {
	// Catch a malformed URL here rather than inside the HTTP client.
	if _, err := url.Parse(target); err != nil {
		return "", 0, fmt.Errorf("bad URL %s: %w", target, err)
	}

	// & takes the address of the new Client, so client is a pointer. The timeout covers the
	// whole request, connection through body, and fires as an error from Get or the read.
	//
	// %w wraps the underlying error into the new message, which keeps the cause available to
	// errors.Is and errors.As while the text explains which URL failed.
	client := &http.Client{Timeout: fetchTimeout}
	response, err := client.Get(target)
	if err != nil {
		return "", 0, fmt.Errorf("fetching %s: %w", target, err)
	}
	// defer runs this call when fetch returns, whichever return it takes. An unclosed body
	// leaks its connection, and a loop over many repos would leak one per URL.
	defer response.Body.Close()

	// A 404 or any other non-200 carries no document worth reading, so hand the status back
	// and let loadDocument decide whether to walk on or stop.
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
//
// The environment wins so one export overrides a checked-out .env without editing a file,
// which is what CI does. The second return value is the source, and main logs that path. The
// path is safe to print, and the key never is.
func apiKey() (string, string, error) {
	if key := os.Getenv(apiKeyEnv); key != "" {
		return key, "environment", nil
	}

	// Walk up from the working directory looking for .env, so running a sample from its own
	// folder still finds the key at the repo root. filepath.Join builds the path with the
	// separator this operating system uses.
	dir, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	for range envParentLevels {
		candidate := filepath.Join(dir, ".env")
		if key := keyFromFile(candidate); key != "" {
			return key, candidate, nil
		}
		// filepath.Dir of the filesystem root returns the root again, so this comparison
		// stops the walk instead of spinning on "/" until the count runs out.
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
//
// An unreadable or absent file returns the empty string, which apiKey reads as "keep
// walking up". No error travels out of here, because a missing .env is the normal case.
func keyFromFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		// TrimSpace clears indentation and the \r a Windows-written file leaves behind.
		// TrimPrefix removes "export " when it is there and leaves the line alone when it is
		// not, so both spellings of a .env line parse.
		line = strings.TrimPrefix(strings.TrimSpace(line), "export ")
		// Cut splits on the first = only, which keeps a = inside the value intact. found is
		// false on a line with no =, a comment or a blank line for example.
		name, value, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(name) != apiKeyEnv {
			continue
		}
		// Trim strips any mix of the quote characters from both ends, so "key", 'key' and a
		// bare key all yield the same string.
		return strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return ""
}
