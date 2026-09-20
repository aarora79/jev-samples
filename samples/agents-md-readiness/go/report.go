// The JSON report, in the same shape the Python sample writes, so one jq query
// reads either.
//
// The report is a diffable record of one run. Each run writes a file named after
// the source it read, so re-running the same repo overwrites that repo's own
// report and `git diff` shows what moved: a readiness score, a judgement word, a
// token count. That makes the diff a calibration check on the model and on the
// payload. The same file is the input the sample README's jq one-liners read, so
// the field names and nesting here match the Python sample field for field. A
// rename on either side breaks those jq lines.
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// reportQuestion is one question as the report records it: what Jev returned,
// beside what the payload weighted it at and what this run made of it.
//
// The string in backticks after each field is a struct tag. Go's json package
// reads it to decide the key it writes, so the Go field name can stay capitalised
// (capitalised means exported, which the json package requires) while the JSON
// key stays the snake_case name the Python sample writes.
//
// Three fields are pointers (`*float64`, `*string`). A pointer either holds an
// address or is nil, and the json package writes nil as `null`. An unweighted
// question therefore reports `"weight": null` instead of `0`, which would read as
// a real weight of zero.
type reportQuestion struct {
	// Label is the display label from questions.yml, the same text the table prints.
	Label string `json:"label"`
	// Type is the question type Jev answered, boolean or scale or the like.
	Type string `json:"type"`
	// Weight is the payload's weight for this question, nil when the payload gave none.
	Weight *float64 `json:"weight"`
	// Inverted records that a yes counts against readiness, so a reader of the
	// report can tell why a true answer earned no credit.
	Inverted bool `json:"inverted"`
	// Returned is Jev's answer exactly as the API sent it, kept raw so the report
	// shows the model's own output beside this run's reading of it.
	Returned answer `json:"returned"`
	// Credit is the fraction of the weight this answer earned, nil when unweighted.
	Credit *float64 `json:"credit"`
	// Judgement is the word the sample prints for this answer, nil when unweighted.
	Judgement *string `json:"judgement"`
}

// report is a scored run. Every field lands in the JSON, so this struct is the
// schema the README's jq queries target.
type report struct {
	// Filename is the document that was read, AGENTS.md or CLAUDE.md.
	Filename string `json:"filename"`
	// Source is the URL or local path the bytes came from.
	Source string `json:"source"`
	// Found is true in this struct by construction. A run that found nothing writes
	// the missing struct instead, and jq can branch on this one key either way.
	Found bool `json:"found"`
	// Looked lists the candidate names the sample tried, in order. A slice is a
	// growable view of an array, and the json package writes it as a JSON array.
	Looked []string `json:"looked"`
	// Model is the pinned model the API reported back, recorded so a later diff
	// shows when the pin moved.
	Model string `json:"model"`
	// Usage is the token counts the API returned.
	Usage usage `json:"usage"`
	// LatencyMS is the round trip this run measured, in milliseconds.
	LatencyMS int `json:"latency_ms"`
	// CostUSD is the priced cost of those tokens.
	CostUSD float64 `json:"cost_usd"`
	// QuestionsAsked is how many questions the payload carried into this call.
	QuestionsAsked int `json:"questions_asked"`
	// Readiness is the weighted score, 0 through 1.
	Readiness float64 `json:"readiness"`
	// Questions maps each question ID to its record. A Go map writes as a JSON
	// object, with the question IDs as the keys jq indexes by.
	Questions map[string]reportQuestion `json:"questions"`
}

// missing is a run that found no file. Readiness is null rather than zero,
// because zero would say the document failed every check. The pointer fields hold
// nil, so the json package writes `"readiness": null` and `"filename": null`, and
// a jq query that averages readiness skips the run instead of dragging the average
// down with a score the sample never measured. Found stays false, which is the
// key a reader filters on.
type missing struct {
	// Filename is nil: no document was read, so there is no name to report.
	Filename *string `json:"filename"`
	// Source is the repo or directory the sample searched.
	Source string `json:"source"`
	// Found is false, the flag that separates this shape from a scored report.
	Found bool `json:"found"`
	// Looked lists the names the sample tried and did not find.
	Looked []string `json:"looked"`
	// Model is nil: the sample made no API call, so no model answered.
	Model *string `json:"model"`
	// Usage is nil for the same reason, no call means no tokens.
	Usage *usage `json:"usage"`
	// Readiness is nil, the null that keeps an unmeasured run out of an average.
	Readiness *float64 `json:"readiness"`
	// Questions is an empty map, so jq sees the same `questions` object here as in a
	// scored report and needs no special case. The empty map is allocated, because a
	// nil map would write as `null`.
	Questions map[string]string `json:"questions"`
}

// slugPattern matches any run of characters outside a to z and 0 to 9. The square
// brackets are a character class and the leading ^ negates it, so the pattern
// catches every separator at once: spaces, dots, slashes, underscores. Compiling
// once at package level keeps the work out of the loop. MustCompile panics on a
// bad pattern, which is what you want for a pattern written as a literal here.
var slugPattern = regexp.MustCompile(`[^a-z0-9]+`)

// slug reduces a name to lowercase words joined by hyphens. ToLower folds the case
// first, so the pattern only has to know about lowercase. ReplaceAllString turns
// each matched run of separators into a single hyphen, which is how "AGENTS.md"
// becomes "agents-md". Trim then drops a leading or trailing hyphen left behind by
// a separator at either end.
func slug(text string) string {
	return strings.Trim(slugPattern.ReplaceAllString(strings.ToLower(text), "-"), "-")
}

// reportPath names the file after the repo and the document that was read. A raw
// GitHub URL gives owner and repo; a local path gives the directory the file sits
// in, which is the repo for a checkout.
//
// The name encodes the source on purpose. A raw.githubusercontent.com URL of the
// form <rawPrefix>/owner/repo/branch/AGENTS.md becomes owner-repo-agents-md.json,
// and /home/me/myrepo/AGENTS.md becomes myrepo-agents-md.json. Re-running one
// repo therefore overwrites that repo's own report and leaves every other report
// alone, which turns `git diff` on the report directory into a calibration check.
func reportPath(dir string, doc document) string {
	// project is the first half of the name. "local" is the fallback when neither
	// branch below finds something better.
	project := "local"
	if strings.HasPrefix(doc.Source, rawPrefix) {
		// Split the path that follows the raw host into segments. The first two are
		// owner and repo.
		parts := strings.Split(strings.TrimPrefix(doc.Source, rawPrefix), "/")
		if len(parts) >= 2 {
			project = parts[0] + "-" + parts[1]
		}
	} else if parent := filepath.Base(filepath.Dir(doc.Source)); parent != "" && parent != "." && parent != string(filepath.Separator) {
		// A local path: Dir drops the filename and Base takes the last directory, so a
		// checkout reports its own directory name. The guards reject the three answers
		// that carry no information, an empty string, "." for the working directory,
		// and the filesystem root. Note that parent is declared inside the if, with
		// :=, which is Go's short declaration; it is in scope for this branch only.
		project = parent
	}

	// The second half of the name is the document. A run that found nothing still
	// writes a report, under "not-found", so the absence is on disk beside the hits.
	suffix := "not-found"
	if doc.Found {
		suffix = slug(doc.Name)
	}
	// Join builds the path with the separator the host OS uses.
	return filepath.Join(dir, fmt.Sprintf("%s-%s.json", slug(project), suffix))
}

// round4 keeps the report readable without pretending to more precision than the
// model gives. Four decimals carry every difference a weighted score of a handful
// of questions can show, and they keep a diff from churning on digits that are
// noise. Go has no round-to-n-places call, so multiply by 10000, round to a whole
// number, divide back: that expression is the idiom.
func round4(value float64) float64 {
	return math.Round(value*10000) / 10000
}

// scoredReport builds the report for a run that scored a document. It walks the
// question specs in payload order, records what Jev returned for each, and fills
// in weight, credit and judgement for the weighted ones. Readiness rounds to four
// decimals, cost to eight: one run costs a fraction of a cent, and four decimals
// would round most runs to zero.
func scoredReport(doc document, specs []spec, res apiResponse, ready float64, latency int, set settings) report {
	// make allocates the map with room for every spec up front, so the loop never has
	// to grow it. A map declared without make is nil and panics on assignment.
	questions := make(map[string]reportQuestion, len(specs))
	// range over a slice yields index and value; _ discards the index, which the
	// order of specs already carries.
	for _, s := range specs {
		// Indexing a map returns the zero value when the key is absent, so a question
		// Jev left out records as an empty answer rather than crashing the run.
		a := res.Answers[s.ID]
		entry := reportQuestion{
			Label:    s.Q.Label,
			Type:     s.Q.Type,
			Inverted: s.Q.Invert,
			Returned: a,
		}
		// Only a weighted question earns credit, so the three pointer fields stay nil
		// for the rest and the JSON reports null there.
		if s.Q.Weight > 0 {
			// Each value is copied into its own local first. & takes the address of that
			// local, and Go keeps the local alive for as long as the pointer does, so each
			// entry points at its own value instead of sharing one loop variable.
			weight := s.Q.Weight
			got := round4(credit(s.Q, a))
			word := judgementOf(s.Q, a)
			entry.Weight = &weight
			entry.Credit = &got
			entry.Judgement = &word
		}
		questions[s.ID] = entry
	}

	return report{
		Filename:       doc.Name,
		Source:         doc.Source,
		Found:          true,
		Looked:         doc.Looked,
		Model:          res.Model,
		Usage:          res.Usage,
		LatencyMS:      latency,
		CostUSD:        math.Round(costUSD(res.Usage, set)*1e8) / 1e8,
		QuestionsAsked: len(specs),
		Readiness:      round4(ready),
		Questions:      questions,
	}
}

// writeJSON writes one report into dir, creating it when needed, and returns the
// path it wrote, so the caller can print the exact file a reader should open.
//
// body is `any`, Go's empty interface, which accepts a value of any type. That
// lets one function write both a report and a missing, since the json package
// inspects whichever struct it receives.
func writeJSON(dir string, doc document, body any) (string, error) {
	// MkdirAll creates every missing parent and stays quiet when the directory is
	// already there. 0o755 is the octal Unix mode: the owner may write, everyone may
	// read and enter the directory.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		// The two results are a path and an error. On failure the path is the empty
		// string and the error carries the cause: %w wraps the original error inside the
		// new message, so a caller can still test what went wrong underneath.
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}

	// MarshalIndent encodes with two spaces per level. Indented JSON puts one field
	// per line, which is what makes a later `git diff` of the report readable.
	encoded, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding the report: %w", err)
	}

	path := reportPath(dir, doc)
	// append adds the trailing newline that every text file wants, and 0o644 makes the
	// report owner-writable and world-readable. WriteFile truncates an existing file,
	// which is how a re-run of one repo replaces that repo's own report.
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	// nil in the error slot is how a Go function reports success; callers check
	// `err != nil` and use the path when it is nil.
	return path, nil
}

// missingReport builds the report for a repo or directory holding neither name.
// The fields left out of this literal take their zero values, and for a pointer
// that zero is nil, which the JSON writes as null. Filename, Model, Usage and
// Readiness all report null that way, so the record says the sample measured
// nothing here instead of claiming a score of zero. Questions gets an allocated
// empty map, which writes as `{}` and keeps the key present for the jq queries.
func missingReport(doc document) missing {
	return missing{
		Source:    doc.Source,
		Found:     false,
		Looked:    doc.Looked,
		Questions: map[string]string{},
	}
}
