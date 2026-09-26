// The Jev half: ask eleven questions about one pull request, then judge the
// answers.
//
// Every threshold on a number lives here rather than in a question, because Jev
// cannot count. It reads a truncated diff and answers about what it read, and the
// file and line counts stay arithmetic that Go does.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	// endpoint is the one Jev URL. One call carries every question.
	endpoint = "https://api.typesafe.ai/v1/systemone"
	// jevEndpointEnv lets a caller point at something else, which a proxy or a
	// self-hosted reproduction needs.
	jevEndpointEnv = "TYPESAFE_API_URL"
	// requestTimeout caps one call. Jev answers in well under a second, so a
	// minute means something else broke: DNS, a proxy, a hung connection.
	requestTimeout = 60 * time.Second
	// deadband is how close a load can sit to a tier cut before the tier prints as
	// undecided. Repeat calls on one pull request move a load by about this much,
	// so a value inside the band would otherwise flip between runs.
	deadband = 0.02
	// driverFloor is the smallest share of the load worth naming as a driver.
	driverFloor = 0.05
	// driverCount is how many drivers to name.
	driverCount = 3
)

// tiers names the review tiers, cheapest review first. Index order is what lets
// the size floor raise a tier without a second table.
var tiers = []string{"trivial", "low", "medium", "high"}

// loadFloors says what review load buys which tier, highest floor first.
//
// The cuts come from the cost of being wrong at each one: calling a medium pull
// request low costs a reviewer an hour, and calling a high one medium costs an
// incident.
var loadFloors = []struct {
	floor float64
	tier  string
}{
	{0.62, "high"},
	{0.42, "medium"},
	{0.22, "low"},
	{0.00, "trivial"},
}

// sizeFloors names the lowest tier a pull request of a given size can land in,
// whatever Jev returned, highest floor first.
//
// Jev sees a truncated diff, so a 54-file change can read as nine repeated edits
// and score as mechanical. Size is arithmetic, Go does it, and it only ever raises
// a tier.
var sizeFloors = []struct {
	files int
	lines int
	tier  string
}{
	{30, 1500, "high"},
	{12, 500, "medium"},
	{4, 150, "low"},
}

// consequenceQuestions are the four that say what breaks if this change is wrong,
// as opposed to how long it takes to read. Consequence is the MAX of these, never
// the mean: a change that is safe in three ways and dangerous in one is a
// dangerous change, and averaging lets three absences of risk vote down one
// confident yes.
var consequenceQuestions = []string{
	"security_surface",
	"breaking_change",
	"blast_radius",
	"infra_surface",
}

// routes name what evidence is enough, cheapest first.
//
// Nothing here says "merge". TypeSafe publishes 67.8% accuracy on their own
// benchmark, which is fine for deciding how much evidence to demand and nowhere
// near enough to be the last gate before main.
var routes = []string{
	"green-is-enough",
	"tests-are-enough",
	"ai-review-is-enough",
	"human-required",
	"human-plus-author",
}

// consequenceFloors say what consequence buys which route, highest floor first.
// The cuts come from the cost of being wrong: a change that probably touches auth
// cannot be waved through on a green tick, whatever else is true of it.
var consequenceFloors = []struct {
	floor float64
	route string
}{
	{0.60, "human-required"},
	{0.35, "ai-review-is-enough"},
	{0.15, "tests-are-enough"},
	{0.00, "green-is-enough"},
}

// effortRaises let a long read raise the route, never lower it. A long read wants
// a second pair of eyes even when nothing dangerous is in it.
var effortRaises = []struct {
	floor float64
	route string
}{
	{0.62, "ai-review-is-enough"},
	{0.42, "tests-are-enough"},
}

// routeAdvice says what each route asks of a team, so the advice travels with the
// route rather than living in a README.
var routeAdvice = map[string]string{
	"green-is-enough":     "merge when CI is green: nothing here needs a person",
	"tests-are-enough":    "merge when CI is green and a test exercises the change",
	"ai-review-is-enough": "an AI review that finds nothing is sufficient, plus green CI and tests",
	"human-required":      "a person reads this before it merges, whatever the machines say",
	"human-plus-author":   "a person reads it line by line, and the author walks them through it",
}

// tierAdvice says what to do with a pull request in each tier. The advice is the
// point of the triage, so it travels with the tier rather than living in a README.
var tierAdvice = map[string]string{
	"trivial": "merge on a glance: read the title, skim the diff, check that CI is green",
	"low":     "one reviewer, one pass, no meeting",
	"medium":  "one reviewer who knows this area, reading the whole diff",
	"high":    "a human reads this line by line, and the author walks them through it",
}

// answer is one answer from Jev, covering all three question types.
//
// The API returns a different shape per type, and one struct with every field
// costs nothing: the fields a type does not use stay at their zero values. Type
// says which ones to read.
type answer struct {
	Type string `json:"type"`
	// Noul is the probability a noul question came back with, which is also its
	// confidence.
	Noul float64 `json:"noul"`
	// Choice is the option a choice question picked.
	Choice string `json:"choice"`
	// Score is where a score question landed on its rubric, between levels.
	Score float64 `json:"score"`
	// Confidence comes with a choice and a score. A noul has none, because its
	// probability is the confidence.
	Confidence float64 `json:"confidence"`
	// Legend maps each rubric level to the text from questions.yml, keyed by the
	// level as a string. Its size is how many levels the rubric has.
	//
	// The value is any rather than string, because Jev echoes back whatever the
	// criteria held. A level written in YAML as `- None: some text` parses as a
	// mapping rather than a string, and the API returns that mapping. Decoding
	// into a string would fail on a payload this binary should still be able to
	// read.
	Legend map[string]any `json:"legend"`
	// Probabilities is the spread across a choice's options or a score's levels.
	Probabilities map[string]float64 `json:"probabilities"`
}

// usage is what one call cost in tokens.
type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// apiResponse is the whole body Jev returns.
type apiResponse struct {
	Model   string            `json:"model"`
	Answers map[string]answer `json:"answers"`
	Usage   usage             `json:"usage"`
}

// wireQuestion is one question as the API wants it.
type wireQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	// Criteria is omitted for a noul, which has none. omitempty leaves the key out
	// rather than sending null.
	Criteria any `json:"criteria,omitempty"`
}

// result is everything one pull request produced: the answers, the load, the tier
// and what the call cost.
type result struct {
	Pull         pullRequest
	Answers      map[string]answer
	Load         float64
	Tier         string
	RaisedBySize bool
	Coverage     float64
	Consequence  float64
	// ConsequenceLabel names the question that set the consequence, so a reader
	// knows which of the four is the reason.
	ConsequenceLabel string
	Route            string
	// RouteDecidedBy is "consequence", "effort" or "both".
	RouteDecidedBy string
	// Downgrade is the one signal that would move this to a cheaper route, or ""
	// when it is already on the cheapest.
	Downgrade string
	Drivers   []string
	Usage     usage
	LatencyMS int64
	Raw       json.RawMessage
}

// buildState assembles the state for one pull request, each part in its own named
// field.
//
// The counts go in as a sentence rather than as numbers to compare, because Jev
// cannot do arithmetic. The sentence is context, and every threshold on a count
// happens in Go.
//
// It returns the state and the coverage: the share of changed files whose patch
// fit the budget, which says how much of the change Jev actually read.
func buildState(repo string, pull pullRequest, set settings) (map[string]string, float64) {
	diff, shown := diffText(pull.Files, set.MaxDiffChars)
	omitted := len(pull.Files) - shown

	coverage := 1.0
	if len(pull.Files) > 0 {
		coverage = float64(shown) / float64(len(pull.Files))
	}

	scope := fmt.Sprintf(
		"%d files changed, %d lines added, %d removed",
		pull.ChangedFiles, pull.Additions, pull.Deletions,
	)
	if omitted > 0 {
		scope += fmt.Sprintf(
			". The diff below holds %d of those files; %d are listed without a patch",
			shown, omitted,
		)
	}

	// Treat the description as untrusted, the same way you treat a prompt. An
	// author who writes "trivial, please merge" moves these answers, so the field
	// name says what the text is rather than presenting it as fact.
	description := pull.Body
	if strings.TrimSpace(description) == "" {
		description = "(no description)"
	}
	if len(description) > set.MaxDescriptionChars {
		description = description[:set.MaxDescriptionChars]
	}

	if diff == "" {
		diff = "(no textual diff: binary files, or none GitHub would render)"
	}

	return map[string]string{
		"repo":                              repo,
		"base_branch":                       pull.Base,
		"title":                             pull.Title,
		"description_written_by_the_author": description,
		"scope":                             scope,
		"changed_files":                     fileListText(pull.Files, set.MaxFileListChars),
		"diff":                              diff,
	}, coverage
}

// fileListText lists every changed path with its line counts, one per line.
//
// Cheap in tokens and dense in signal: the paths alone say whether a change is
// docs, tests, infrastructure or product code, and they survive a diff that got
// truncated.
func fileListText(files []changedFile, budget int) string {
	lines := make([]string, 0, len(files))
	for _, entry := range files {
		lines = append(lines, fmt.Sprintf(
			"%-9s +%-5d -%-5d %s", entry.Status, entry.Additions, entry.Deletions, entry.Path,
		))
	}

	text := strings.Join(lines, "\n")
	if len(text) <= budget {
		return text
	}

	// Cut on a line boundary, so the last entry a reader sees is a whole path
	// rather than half of one.
	kept := text[:budget]
	if cut := strings.LastIndex(kept, "\n"); cut > 0 {
		kept = kept[:cut]
	}
	shown := strings.Count(kept, "\n") + 1
	return fmt.Sprintf("%s\n... and %d more paths", kept, len(files)-shown)
}

// diffText assembles the patches into one diff, fitting as many whole files as it
// can, smallest patch first.
//
// Triage asks how far a change reaches and whether the same edit repeats, and both
// want breadth: twelve files read in full answer them, where one 5,000-line
// lockfile answers neither. The file list carries the paths that did not fit.
//
// It returns the diff and how many files went in whole.
func diffText(files []changedFile, budget int) (string, int) {
	withPatch := make([]changedFile, 0, len(files))
	for _, entry := range files {
		if entry.HasPatch {
			withPatch = append(withPatch, entry)
		}
	}

	// sort.SliceStable keeps equal-length patches in the order the dataset listed
	// them, so two runs on one dataset build the same state.
	sort.SliceStable(withPatch, func(i, j int) bool {
		return len(withPatch[i].Patch) < len(withPatch[j].Patch)
	})

	var builder strings.Builder
	shown := 0
	for _, entry := range withPatch {
		chunk := fmt.Sprintf("diff --git a/%s b/%s\n%s\n", entry.Path, entry.Path, entry.Patch)
		if builder.Len()+len(chunk) > budget {
			break
		}
		builder.WriteString(chunk)
		shown++
	}
	return builder.String(), shown
}

// ask sends one pull request and every question in a single call.
//
// That is the point of the pattern: the pull request is the expensive part of the
// request, so asking one more question about a diff already on the wire costs a
// few tokens and no extra round trip.
func ask(key string, set settings, specs []spec, repo string, pull pullRequest) (result, error) {
	state, coverage := buildState(repo, pull, set)

	questions := make(map[string]wireQuestion, len(specs))
	for _, s := range specs {
		questions[s.ID] = wireQuestion{
			Type:         s.Q.Type,
			Instructions: s.Q.Instructions,
			Criteria:     s.Q.Criteria,
		}
	}

	body, err := json.Marshal(map[string]any{
		"model":     set.Model,
		"state":     state,
		"questions": questions,
	})
	if err != nil {
		return result{}, fmt.Errorf("encoding the request for #%d: %w", pull.Number, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	url := endpoint
	if custom := strings.TrimSpace(os.Getenv(jevEndpointEnv)); custom != "" {
		url = custom
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return result{}, fmt.Errorf("building the request for #%d: %w", pull.Number, err)
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")

	started := time.Now()
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return result{}, fmt.Errorf("calling Jev for #%d: %w", pull.Number, err)
	}
	defer response.Body.Close()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return result{}, fmt.Errorf("reading Jev's answer for #%d: %w", pull.Number, err)
	}
	latency := time.Since(started).Milliseconds()

	if response.StatusCode != http.StatusOK {
		return result{}, fmt.Errorf(
			"Jev returned %d for #%d: %s", response.StatusCode, pull.Number, trimTo(string(raw), 200),
		)
	}

	var parsed apiResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return result{}, fmt.Errorf("parsing Jev's answer for #%d: %w", pull.Number, err)
	}
	// A question this binary asked and got no answer for would otherwise surface
	// as a zero credit, which reads as a real judgment rather than a gap.
	for _, s := range specs {
		if _, ok := parsed.Answers[s.ID]; !ok {
			return result{}, fmt.Errorf("Jev returned no answer for %s on #%d", s.ID, pull.Number)
		}
	}

	load := reviewLoad(parsed.Answers, specs)
	tier, raised := tierFor(load, pull.ChangedFiles, pull.Additions+pull.Deletions)
	consequenceValue, consequenceLabel := consequence(parsed.Answers, specs)
	route, decided := routeFor(consequenceValue, load)

	return result{
		Pull:             pull,
		Answers:          parsed.Answers,
		Load:             load,
		Tier:             tier,
		RaisedBySize:     raised,
		Coverage:         coverage,
		Consequence:      consequenceValue,
		ConsequenceLabel: consequenceLabel,
		Route:            route,
		RouteDecidedBy:   decided,
		Downgrade:        downgradeFor(parsed.Answers, specs, route, decided),
		Drivers:          drivers(parsed.Answers, specs),
		Usage:            parsed.Usage,
		LatencyMS:        latency,
		Raw:              raw,
	}, nil
}

// credit turns one answer into what it contributed toward the review load, 0 to 1.
//
// A score divides by its own top level. A noul is its probability. Either one
// flips when the entry inverts, so a mechanical diff and a thorough description
// lower the load instead of raising it. A choice reads the credit table the entry
// carries, and neither choice carries one today.
func credit(s spec, a answer) float64 {
	var value float64

	switch s.Q.Type {
	case "noul":
		value = a.Noul
	case "score":
		// The legend has one entry per level, so its size minus one is the top
		// level. Guarding the divisor keeps a malformed answer from panicking.
		top := float64(len(a.Legend) - 1)
		if top > 0 {
			value = a.Score / top
		}
	default:
		value = s.Q.Credit[a.Choice]
	}

	if s.Q.Invert {
		return 1.0 - value
	}
	return value
}

// contribution is one answer's share of the load: its credit times its weight,
// over the total weight in the payload.
func contribution(s spec, a answer, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return s.Q.Weight * credit(s, a) / total
}

// reviewLoad weights every weighted answer into one number, from 0 to 1.
//
// Dividing by the weights actually present means an edited weight needs no
// rebalancing of the others.
func reviewLoad(answers map[string]answer, specs []spec) float64 {
	total := weightedTotal(specs)
	if total <= 0 {
		return 0
	}

	earned := 0.0
	for _, s := range specs {
		if s.Q.Weight <= 0 {
			continue
		}
		earned += s.Q.Weight * credit(s, answers[s.ID])
	}
	return earned / total
}

// drivers names the questions that account for most of the review load.
func drivers(answers map[string]answer, specs []spec) []string {
	total := weightedTotal(specs)

	type share struct {
		value float64
		label string
	}
	ranked := make([]share, 0, len(specs))
	for _, s := range specs {
		if s.Q.Weight <= 0 {
			continue
		}
		ranked = append(ranked, share{contribution(s, answers[s.ID], total), s.Q.Label})
	}

	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].value > ranked[j].value })

	named := make([]string, 0, driverCount)
	for _, item := range ranked {
		if len(named) == driverCount || item.value < driverFloor {
			break
		}
		named = append(named, item.label)
	}
	return named
}

// consequenceOf reads one answer as consequence, which is not the same as credit.
//
// A noul reads as its probability. A score reads as the probability it put on its
// top level, rather than as the normalised score, because only the top level of
// these rubrics describes something consequential. blast_radius level 1 is
// "confined to one module or feature area", which is ordinary work, and dividing a
// score of 1.0 by 2 would report ordinary work as half a catastrophe.
func consequenceOf(s spec, a answer) float64 {
	if s.Q.Type != "score" {
		return credit(s, a)
	}

	top := len(a.Legend) - 1
	// The wire keys probabilities by the level as a string.
	return a.Probabilities[fmt.Sprintf("%d", top)]
}

// consequence reads what breaks if this change is wrong, and names what said so.
//
// The max rather than the mean, because risk does not average. A smoke alarm that
// averages four rooms is useless.
func consequence(answers map[string]answer, specs []spec) (float64, string) {
	byID := make(map[string]spec, len(specs))
	for _, s := range specs {
		byID[s.ID] = s
	}

	worst, label := 0.0, ""
	for _, qid := range consequenceQuestions {
		s, ok := byID[qid]
		if !ok {
			continue
		}
		a, ok := answers[qid]
		if !ok {
			continue
		}
		if value := consequenceOf(s, a); value >= worst {
			worst, label = value, s.Q.Label
		}
	}
	return worst, label
}

// routeIndex says where a route sits in the order, cheapest first, or -1 for a
// name that is not a route.
func routeIndex(route string) int {
	for index, name := range routes {
		if name == route {
			return index
		}
	}
	return -1
}

// routeFor says what evidence is enough before this merges, and what decided that.
//
// Consequence sets the floor and effort can only raise it. Neither lowers the
// other, so a small diff cannot talk its way past an auth change and a quiet auth
// surface cannot wave through a thousand-line refactor.
func routeFor(consequenceValue, load float64) (string, string) {
	index, decided := 0, "consequence"
	for _, band := range consequenceFloors {
		if consequenceValue >= band.floor {
			index = routeIndex(band.route)
			break
		}
	}

	for _, band := range effortRaises {
		if load >= band.floor && routeIndex(band.route) > index {
			index, decided = routeIndex(band.route), "effort"
			break
		}
	}

	// A long read of something dangerous is the one case that wants the author
	// walking a human through it.
	if consequenceValue >= consequenceFloors[0].floor && load >= effortRaises[0].floor {
		return routes[len(routes)-1], "both"
	}
	return routes[index], decided
}

// downgradeFor names the one signal that would move this to a cheaper route.
//
// A tier tells an author nothing to do. This tells them exactly what to do.
func downgradeFor(answers map[string]answer, specs []spec, route, decided string) string {
	if route == routes[0] {
		return ""
	}
	if decided == "effort" {
		named := drivers(answers, specs)
		biggest := "the weighted questions"
		if len(named) > 0 {
			biggest = named[0]
		}
		return fmt.Sprintf("a shorter read, or splitting it: %s is carrying the load", biggest)
	}

	_, label := consequence(answers, specs)
	return fmt.Sprintf("evidence that %s is covered, a test or a reviewer who owns it", label)
}

// tierIndex says where a tier sits in the order, cheapest review first, or -1 for
// a name that is not a tier.
func tierIndex(tier string) int {
	for index, name := range tiers {
		if name == tier {
			return index
		}
	}
	return -1
}

// sizeFloor names the lowest tier a pull request of this size can land in.
func sizeFloor(changedFiles, lines int) string {
	for _, floor := range sizeFloors {
		if changedFiles > floor.files || lines > floor.lines {
			return floor.tier
		}
	}
	return tiers[0]
}

// tierFor reads a load as a tier, then lets the size floor raise it.
//
// It returns the tier and whether size raised it, so the output can mark the rows
// where Go overruled Jev instead of hiding the disagreement.
func tierFor(load float64, changedFiles, lines int) (string, bool) {
	fromLoad := tiers[0]
	for _, band := range loadFloors {
		if load >= band.floor {
			fromLoad = band.tier
			break
		}
	}

	floor := sizeFloor(changedFiles, lines)
	if tierIndex(floor) > tierIndex(fromLoad) {
		return floor, true
	}
	return fromLoad, false
}

// nearACut says whether a load sits close enough to a tier cut to move between
// runs.
func nearACut(load float64) bool {
	for _, band := range loadFloors {
		if band.floor <= 0 {
			continue
		}
		delta := load - band.floor
		if delta < 0 {
			delta = -delta
		}
		if delta <= deadband+1e-9 {
			return true
		}
	}
	return false
}

// nearARouteCut says whether a consequence sits close enough to a route cut to
// move between runs.
//
// The same deadband the tier uses. Two runs of one queue would otherwise disagree
// about a route for a value sitting on a line.
func nearARouteCut(consequenceValue float64) bool {
	for _, band := range consequenceFloors {
		if band.floor <= 0 {
			continue
		}
		delta := consequenceValue - band.floor
		if delta < 0 {
			delta = -delta
		}
		if delta <= deadband+1e-9 {
			return true
		}
	}
	return false
}

// costUSD prices a whole run from its input tokens. TypeSafe bills input tokens
// only, at the rate questions.yml records.
func costUSD(results []result, set settings) float64 {
	tokens := 0
	for _, r := range results {
		tokens += r.Usage.InputTokens
	}
	return float64(tokens) * set.InputUSDPerMillion / 1_000_000
}
