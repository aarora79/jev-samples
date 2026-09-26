// The GitHub half: turn a repository name into a dataset of pull requests.
//
// This half talks to the GitHub REST API and never to Jev, so it runs with a
// GitHub token and no TypeSafe key. It works against github.com and against a
// GitHub Enterprise Server host, because every endpoint is built from one base
// URL that the caller can set.
//
// The dataset it writes is the same JSON the Python sample writes, field for
// field, so a dataset built by either tool replays through either tool.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultAPIBase is github.com's API. A GitHub Enterprise Server host puts its
	// API under /api/v3 instead, which is why the base is a variable rather than a
	// path glued together at each call site.
	defaultAPIBase = "https://api.github.com"
	// ghesAPIPath is the suffix a GHES host serves the v3 API from.
	ghesAPIPath = "/api/v3"
	// apiVersion is the version header GitHub asks callers to pin. Without it the
	// API is free to change shape under this binary.
	apiVersion = "2022-11-28"
	// userAgent identifies this tool in GitHub's logs, which the API requires.
	userAgent = "jev-samples-pr-triage"
	// perPage is GitHub's maximum, and so the fewest round trips per repository.
	perPage = 100
	// maxPages stops the walk rather than paginating through a repository with
	// 4,000 open pull requests. -all means all of them up to this.
	maxPages = 20
	// fetchTimeout caps one API call.
	fetchTimeout = 30 * time.Second
	// ghTokenTimeout caps the shell out to the gh CLI, which can sit waiting on a
	// keyring prompt.
	ghTokenTimeout = 10 * time.Second
	// maxPatchChars cuts one file's patch as it lands in the dataset. A generated
	// lockfile diff runs to hundreds of thousands of characters and would dominate
	// the file on disk. The triage half applies its own, smaller budget when it
	// builds the state it sends.
	maxPatchChars = 40000
	// defaultLimit is how many recent pull requests to take when the caller names
	// no selector.
	defaultLimit = 10
)

// tokenEnvNames lists where the environment carries a GitHub token, in the order
// checked. GH_ENTERPRISE_TOKEN is the one a GHES user sets, and gh reads the same
// name, so a machine already talking to an enterprise host needs no new variable.
var tokenEnvNames = []string{"GITHUB_TOKEN", "GH_TOKEN", "GH_ENTERPRISE_TOKEN"}

// apiBaseEnvNames lists where the environment carries the API base. GitHub
// Actions sets GITHUB_API_URL on both github.com and GHES, so a workflow needs no
// flag at all.
var apiBaseEnvNames = []string{"GITHUB_API_URL", "GH_API_URL"}

// repoPattern matches `owner/repo`, which is what the API path wants and what gh
// prints. MustCompile panics on a bad pattern at startup rather than returning an
// error at the first call, which is what you want for a constant pattern.
var repoPattern = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)

// slugPattern collapses everything that is not a letter or a digit into one dash,
// so a repository name becomes a filename.
var slugPattern = regexp.MustCompile(`[^a-z0-9]+`)

// changedFile is one file in a pull request, as the dataset stores it.
//
// The json tags name the keys, and they match the Python sample's dataset exactly
// so the two tools read each other's files.
type changedFile struct {
	Path string `json:"path"`
	// PreviousPath is set when GitHub reports the file as renamed. It is a pointer
	// so it can be null in JSON, which is what the Python writes for a file that
	// was not renamed.
	PreviousPath   *string `json:"previous_path"`
	Status         string  `json:"status"`
	Additions      int     `json:"additions"`
	Deletions      int     `json:"deletions"`
	Patch          string  `json:"patch"`
	PatchTruncated bool    `json:"patch_truncated"`
	HasPatch       bool    `json:"has_patch"`
}

// pullRequest is one pull request, as the dataset stores it.
type pullRequest struct {
	Number       int           `json:"number"`
	Title        string        `json:"title"`
	Body         string        `json:"body"`
	Author       string        `json:"author"`
	CreatedAt    string        `json:"created_at"`
	UpdatedAt    string        `json:"updated_at"`
	Draft        bool          `json:"draft"`
	Labels       []string      `json:"labels"`
	Base         string        `json:"base"`
	URL          string        `json:"url"`
	ChangedFiles int           `json:"changed_files"`
	Additions    int           `json:"additions"`
	Deletions    int           `json:"deletions"`
	Files        []changedFile `json:"files"`
	// FilesTruncated says whether pagination cut the file list short, which GitHub
	// also does at 3,000 files.
	FilesTruncated bool `json:"files_truncated"`
}

// selector records which pull requests a dataset holds, so a reader can tell a
// ten-most-recent dataset from an everything-open one.
type selector struct {
	State string `json:"state"`
	// Limit is a pointer so it can be null, which means "no limit".
	Limit *int `json:"limit"`
	// Since is a pointer for the same reason: null means no date cutoff.
	Since *string `json:"since"`
	// Pull names the single pull request this dataset holds, when a caller passed
	// its URL. omitempty leaves the key out for a dataset that holds a queue, so a
	// dataset written by the Python sample keeps the shape it always had.
	Pull *int `json:"pull,omitempty"`
}

// dataset is the whole file: which repository, when, which selector, and the pull
// requests themselves.
type dataset struct {
	Repo         string        `json:"repo"`
	FetchedAt    string        `json:"fetched_at"`
	Selector     selector      `json:"selector"`
	PullRequests []pullRequest `json:"pull_requests"`
}

// target names a repository, the API that serves it, and optionally one pull
// request inside it.
type target struct {
	// Repo is `owner/repo`.
	Repo string
	// APIBase is the root every endpoint hangs off, with no trailing slash.
	APIBase string
	// Number is one pull request to triage on its own, or 0 for the whole queue.
	// A URL pasted from a browser carries it, as in
	// https://github.com/owner/repo/pull/1803.
	Number int
}

// parseTarget reads `owner/repo` out of whatever the caller passed, and works out
// which API serves it.
//
// It accepts the bare form and a full URL, so a pasted browser address works. A
// URL on a host other than github.com is taken as a GitHub Enterprise Server
// address, and its API base becomes https://that-host/api/v3. An explicit
// -api-base always wins, because a GHES install can serve its API from somewhere
// else entirely.
func parseTarget(raw, apiBaseFlag string) (target, error) {
	cleaned := strings.TrimRight(strings.TrimSpace(raw), "/")
	base := ""
	number := 0

	// A URL carries its own host, which is the only hint available about whether
	// this is github.com or an enterprise install.
	if strings.HasPrefix(cleaned, "https://") || strings.HasPrefix(cleaned, "http://") {
		parsed, err := url.Parse(cleaned)
		if err != nil {
			return target{}, fmt.Errorf("could not read %q as a URL: %w", raw, err)
		}
		// Path is /owner/repo, possibly with more after it, so the first two
		// non-empty segments are the repository.
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return target{}, fmt.Errorf("expected owner/repo in the URL, got %q", raw)
		}
		cleaned = parts[0] + "/" + parts[1]

		// A browser URL for one pull request reads /owner/repo/pull/1803. GitHub
		// serves the API under /pulls, and people paste either, so accept both.
		if len(parts) >= 4 && (parts[2] == "pull" || parts[2] == "pulls") {
			parsedNumber, err := strconv.Atoi(parts[3])
			if err != nil || parsedNumber <= 0 {
				return target{}, fmt.Errorf("expected a pull request number in %q", raw)
			}
			number = parsedNumber
		}

		if parsed.Host == "github.com" || parsed.Host == "www.github.com" {
			base = defaultAPIBase
		} else {
			base = parsed.Scheme + "://" + parsed.Host + ghesAPIPath
		}
	}

	if !repoPattern.MatchString(cleaned) {
		return target{}, fmt.Errorf("expected owner/repo, got %q", raw)
	}

	// Precedence: the flag, then what the URL implied, then the environment, then
	// github.com. The flag wins so a caller can always override a wrong guess.
	switch {
	case apiBaseFlag != "":
		base = apiBaseFlag
	case base != "":
	default:
		base = apiBaseFromEnv()
	}
	return target{Repo: cleaned, APIBase: strings.TrimRight(base, "/"), Number: number}, nil
}

// apiBaseFromEnv reads the API base out of the environment, falling back to
// github.com.
func apiBaseFromEnv() string {
	for _, name := range apiBaseEnvNames {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}
	return defaultAPIBase
}

// resolveToken finds a GitHub token: the flag, then the environment, then the gh
// CLI. It returns an empty string when there is none, which means calling the API
// unauthenticated at sixty requests an hour.
func resolveToken(flagValue string) string {
	if strings.TrimSpace(flagValue) != "" {
		return strings.TrimSpace(flagValue)
	}
	for _, name := range tokenEnvNames {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			logf("read a GitHub token from %s", name)
			return value
		}
	}
	return tokenFromGHCLI()
}

// tokenFromGHCLI asks the gh CLI for the token it already holds.
//
// exec.Command runs a program rather than a shell, so nothing here goes through
// shell quoting and no caller input reaches the command line.
func tokenFromGHCLI() string {
	// A context with a deadline stops a gh that sits waiting on a keyring prompt.
	ctx, cancel := context.WithTimeout(context.Background(), ghTokenTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	if err != nil {
		logf("no token from the gh CLI, so calling GitHub unauthenticated at 60 requests an hour")
		return ""
	}
	token := strings.TrimSpace(string(out))
	if token != "" {
		logf("read a GitHub token from the gh CLI")
	}
	return token
}

// getJSON fetches one API URL and decodes the body into out.
//
// out is a pointer to the value to fill, which is how Go's json package writes
// into a caller's variable.
func getJSON(endpoint, token string, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("building a request for %s: %w", endpoint, err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", apiVersion)
	request.Header.Set("User-Agent", userAgent)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("calling %s: %w", endpoint, err)
	}
	// defer runs when this function returns, whichever path it takes, so the body
	// closes even on the error returns below.
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("reading the response from %s: %w", endpoint, err)
	}

	// Each of these statuses has one likely cause worth naming, because the raw
	// message from GitHub does not say which of them applies.
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return fmt.Errorf(
			"GitHub returned 404 for %s\n  check the repository name, and that this token can see it",
			endpoint,
		)
	case http.StatusUnauthorized, http.StatusForbidden:
		if response.Header.Get("x-ratelimit-remaining") == "0" {
			return fmt.Errorf(
				"GitHub rate limit exhausted. Set a token with -token or GITHUB_TOKEN, or wait for the window to reset",
			)
		}
		return fmt.Errorf(
			"GitHub returned %d for %s\n  the token is missing, expired, or lacks access",
			response.StatusCode, endpoint,
		)
	default:
		return fmt.Errorf("GitHub returned %d for %s", response.StatusCode, endpoint)
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("parsing the response from %s: %w", endpoint, err)
	}
	return nil
}

// listEntry is the part of the pulls list endpoint this binary reads. The
// endpoint returns far more, and naming only these fields keeps the shape of the
// dataset in one place.
type listEntry struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Draft     bool   `json:"draft"`
	HTMLURL   string `json:"html_url"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

// listPulls lists pull requests newest first, stopping as soon as the selector is
// met.
//
// The pulls endpoint takes no date filter, so the date cut happens here. Newest
// first means the first pull request older than the cutoff ends the walk.
func listPulls(tgt target, token, state string, limit int, since string) ([]listEntry, error) {
	var kept []listEntry

	for page := 1; page <= maxPages; page++ {
		query := url.Values{}
		query.Set("state", state)
		query.Set("sort", "created")
		query.Set("direction", "desc")
		query.Set("per_page", strconv.Itoa(perPage))
		query.Set("page", strconv.Itoa(page))

		endpoint := fmt.Sprintf("%s/repos/%s/pulls?%s", tgt.APIBase, tgt.Repo, query.Encode())
		logf("listing %s pull requests for %s, page %d", state, tgt.Repo, page)

		var batch []listEntry
		if err := getJSON(endpoint, token, &batch); err != nil {
			return nil, err
		}

		for _, entry := range batch {
			if !openedOnOrAfter(entry.CreatedAt, since) {
				logf("reached #%d, opened before the cutoff, so stopping", entry.Number)
				return kept, nil
			}
			kept = append(kept, entry)
			if limit > 0 && len(kept) >= limit {
				return kept, nil
			}
		}

		if len(batch) < perPage { // the last page
			return kept, nil
		}
	}

	logf("stopped at %d pages, so the dataset may be short", maxPages)
	return kept, nil
}

// openedOnOrAfter says whether a pull request was opened on or after the cutoff.
// An empty cutoff accepts everything.
func openedOnOrAfter(createdAt, since string) bool {
	if since == "" {
		return true
	}
	// created_at is RFC 3339, and the first ten characters are the date. Comparing
	// two YYYY-MM-DD strings compares the dates, because the format sorts.
	if len(createdAt) < 10 {
		return true
	}
	return createdAt[:10] >= since
}

// apiFile is the part of the files endpoint this binary reads.
type apiFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename"`
	Status           string `json:"status"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Patch            string `json:"patch"`
}

// pullFiles fetches the changed files for one pull request, with their patches.
//
// It returns the files and whether pagination cut the list short.
func pullFiles(tgt target, token string, number int) ([]changedFile, bool, error) {
	var files []changedFile

	for page := 1; page <= maxPages; page++ {
		query := url.Values{}
		query.Set("per_page", strconv.Itoa(perPage))
		query.Set("page", strconv.Itoa(page))

		endpoint := fmt.Sprintf(
			"%s/repos/%s/pulls/%d/files?%s", tgt.APIBase, tgt.Repo, number, query.Encode(),
		)

		var batch []apiFile
		if err := getJSON(endpoint, token, &batch); err != nil {
			return nil, false, err
		}

		for _, entry := range batch {
			// A binary file, or one GitHub declined to diff, carries no patch.
			patch := entry.Patch
			truncated := len(patch) > maxPatchChars
			if truncated {
				patch = patch[:maxPatchChars]
			}

			// A renamed file gets a previous path, and everything else gets null, so
			// the field is a pointer and stays nil unless GitHub filled it.
			var previous *string
			if entry.PreviousFilename != "" {
				name := entry.PreviousFilename
				previous = &name
			}

			files = append(files, changedFile{
				Path:           entry.Filename,
				PreviousPath:   previous,
				Status:         entry.Status,
				Additions:      entry.Additions,
				Deletions:      entry.Deletions,
				Patch:          patch,
				PatchTruncated: truncated,
				HasPatch:       entry.Patch != "",
			})
		}

		if len(batch) < perPage {
			return files, false, nil
		}
	}

	return files, true, nil
}

// pullDetail carries the three counts the list endpoint leaves out.
type pullDetail struct {
	ChangedFiles int `json:"changed_files"`
	Additions    int `json:"additions"`
	Deletions    int `json:"deletions"`
}

// pullRecord turns one list entry into a dataset record, fetching what it lacks.
//
// The list endpoint carries no line counts, so this reads the pull request itself
// for changed_files, additions and deletions, then its files.
func pullRecord(tgt target, token string, entry listEntry) (pullRequest, error) {
	logf("fetching #%d: %s", entry.Number, trimTo(entry.Title, 60))

	var detail pullDetail
	endpoint := fmt.Sprintf("%s/repos/%s/pulls/%d", tgt.APIBase, tgt.Repo, entry.Number)
	if err := getJSON(endpoint, token, &detail); err != nil {
		return pullRequest{}, err
	}

	files, truncated, err := pullFiles(tgt, token, entry.Number)
	if err != nil {
		return pullRequest{}, err
	}

	// A JSON array with no elements decodes to a nil slice, which marshals back as
	// null rather than []. Building the slice with make keeps the dataset shaped
	// like the Python one even for a pull request with no labels.
	labels := make([]string, 0, len(entry.Labels))
	for _, label := range entry.Labels {
		labels = append(labels, label.Name)
	}

	return pullRequest{
		Number:         entry.Number,
		Title:          entry.Title,
		Body:           entry.Body,
		Author:         entry.User.Login,
		CreatedAt:      entry.CreatedAt,
		UpdatedAt:      entry.UpdatedAt,
		Draft:          entry.Draft,
		Labels:         labels,
		Base:           entry.Base.Ref,
		URL:            entry.HTMLURL,
		ChangedFiles:   detail.ChangedFiles,
		Additions:      detail.Additions,
		Deletions:      detail.Deletions,
		Files:          files,
		FilesTruncated: truncated,
	}, nil
}

// pullOne fetches a single pull request by number, for a caller who pasted its
// URL.
//
// The detail endpoint returns the metadata the list endpoint returns and the
// counts it leaves out, so one call covers both and no listing has to walk the
// queue looking for the number.
func pullOne(tgt target, token string) (pullRequest, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/pulls/%d", tgt.APIBase, tgt.Repo, tgt.Number)
	logf("fetching #%d directly", tgt.Number)

	var combined struct {
		listEntry
		pullDetail
	}
	if err := getJSON(endpoint, token, &combined); err != nil {
		return pullRequest{}, err
	}
	if combined.Number == 0 {
		return pullRequest{}, fmt.Errorf("%s has no pull request #%d", tgt.Repo, tgt.Number)
	}

	files, truncated, err := pullFiles(tgt, token, tgt.Number)
	if err != nil {
		return pullRequest{}, err
	}

	labels := make([]string, 0, len(combined.Labels))
	for _, label := range combined.Labels {
		labels = append(labels, label.Name)
	}

	return pullRequest{
		Number:         combined.Number,
		Title:          combined.Title,
		Body:           combined.Body,
		Author:         combined.User.Login,
		CreatedAt:      combined.CreatedAt,
		UpdatedAt:      combined.UpdatedAt,
		Draft:          combined.Draft,
		Labels:         labels,
		Base:           combined.Base.Ref,
		URL:            combined.HTMLURL,
		ChangedFiles:   combined.ChangedFiles,
		Additions:      combined.Additions,
		Deletions:      combined.Deletions,
		Files:          files,
		FilesTruncated: truncated,
	}, nil
}

// buildDataset fetches pull requests and returns the dataset.
//
// limit of 0 means no limit, and an empty since means no date cutoff, which is
// how the flags express "everything the other selector allows".
func buildDataset(tgt target, token, state string, limit int, since string) (dataset, error) {
	// A URL naming one pull request skips the listing entirely, so -limit, -since
	// and -state have nothing to select from and are ignored.
	if tgt.Number > 0 {
		record, err := pullOne(tgt, token)
		if err != nil {
			return dataset{}, err
		}
		number := tgt.Number
		return dataset{
			Repo:         tgt.Repo,
			FetchedAt:    time.Now().UTC().Format(time.RFC3339),
			Selector:     selector{State: state, Pull: &number},
			PullRequests: []pullRequest{record},
		}, nil
	}

	entries, err := listPulls(tgt, token, state, limit, since)
	if err != nil {
		return dataset{}, err
	}
	logf("selected %d pull requests, now reading each one", len(entries))

	records := make([]pullRequest, 0, len(entries))
	for _, entry := range entries {
		record, err := pullRecord(tgt, token, entry)
		if err != nil {
			return dataset{}, err
		}
		records = append(records, record)
	}

	sel := selector{State: state}
	if limit > 0 {
		kept := limit
		sel.Limit = &kept
	}
	if since != "" {
		cutoff := since
		sel.Since = &cutoff
	}

	return dataset{
		Repo:         tgt.Repo,
		FetchedAt:    time.Now().UTC().Format(time.RFC3339),
		Selector:     sel,
		PullRequests: records,
	}, nil
}

// datasetSlug names the selector a dataset already records, so the dataset and
// both reports agree on one filename.
func datasetSlug(sel selector) string {
	state := sel.State
	if state == "" {
		state = "open"
	}
	if sel.Pull != nil {
		return fmt.Sprintf("pull-%d", *sel.Pull)
	}
	limit := 0
	if sel.Limit != nil {
		limit = *sel.Limit
	}
	since := ""
	if sel.Since != nil {
		since = *sel.Since
	}
	return selectorSlug(state, limit, since)
}

// selectorSlug names the selector, so two datasets for one repository sit in
// separate files.
func selectorSlug(state string, limit int, since string) string {
	switch {
	case since != "":
		return fmt.Sprintf("%s-since-%s", state, since)
	case limit <= 0:
		return state + "-all"
	default:
		return fmt.Sprintf("%s-latest-%d", state, limit)
	}
}

// repoSlug turns `owner/repo` into a filename fragment.
func repoSlug(repo string) string {
	return strings.Trim(slugPattern.ReplaceAllString(strings.ToLower(repo), "-"), "-")
}

// writeJSON writes a value as indented JSON with a trailing newline, which is
// what the Python sample writes and what git wants at the end of a file.
func writeJSON(path string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	return os.WriteFile(path, append(body, '\n'), 0o644)
}

// loadDataset reads a dataset written by either tool.
func loadDataset(path string) (dataset, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return dataset{}, fmt.Errorf("reading %s: %w", path, err)
	}

	var data dataset
	if err := json.Unmarshal(raw, &data); err != nil {
		return dataset{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if len(data.PullRequests) == 0 {
		return dataset{}, fmt.Errorf("%s holds no pull requests", path)
	}
	return data, nil
}

// trimTo cuts a string to at most n characters, for a log line that should not
// wrap.
func trimTo(text string, n int) string {
	if len(text) <= n {
		return text
	}
	return text[:n]
}
