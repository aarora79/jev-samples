// The two Jev calls, the digest between them, and the bucketing after.
//
// Stage one reads the issue body and answers eight questions about the text.
// Stage two reads a fact table and answers one question: when should somebody
// pick this up. Nothing in between does arithmetic on a model's output except
// to round a score into a bucket, and the rounding is here where a diff shows
// it.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	// endpoint is the one Jev URL.
	endpoint = "https://api.typesafe.ai/v1/systemone"
	// jevEndpointEnv lets a caller point at something else, which a proxy or a
	// self-hosted reproduction needs.
	jevEndpointEnv = "TYPESAFE_API_URL"
	// requestTimeout caps one call. Jev answers in well under a second, so a
	// minute means something else broke: DNS, a proxy, a hung connection.
	requestTimeout = 60 * time.Second
	// deadband is how close a horizon score can sit to a bucket edge before the
	// report marks it undecided. Repeat calls on one issue move a score by about
	// this much, so a value inside the band would flip between runs with nothing
	// having changed.
	deadband = 0.15
)

// horizons name the buckets, soonest first. Index order matches the levels of
// the decision question, so level 3 is horizons[0].
var horizons = []string{"Today", "This week", "This month", "When time permits"}

// horizonCaps limit how many issues a bucket may hold, by index.
//
// The names are capacity promises. Thirty things to do today is not a triage, so
// the overflow spills into the next bucket down and the report says it happened.
// Zero means no cap.
var horizonCaps = []int{5, 15, 0, 0}

// answer is one entry from the answers map Jev returns.
type answer struct {
	Type string `json:"type"`
	// Noul is the probability a noul came back with, which is also its
	// confidence.
	Noul float64 `json:"noul"`
	// Choice is the option a choice picked.
	Choice string `json:"choice"`
	// Score is where a score landed on its rubric, between levels.
	Score float64 `json:"score"`
	// Confidence comes with a choice and a score. A noul has none, because its
	// probability is the confidence.
	Confidence float64 `json:"confidence"`
	// Legend maps each level to the text from questions.yml, keyed by the level
	// as a string. Its size is how many levels the rubric has, which is how the
	// code reads a score without hard-coding the top.
	//
	// The value is any rather than string, because Jev echoes back whatever the
	// criteria held.
	Legend map[string]any `json:"legend"`
	// Probabilities is the spread across a choice's options or a score's levels.
	Probabilities map[string]float64 `json:"probabilities"`
}

// usage is the token count one call reported.
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
	// Criteria is omitted for a noul, which has none. omitempty leaves the key
	// out rather than sending null.
	Criteria any `json:"criteria,omitempty"`
}

// discussion is who has been talking on an issue, with bots left out.
//
// Counted rather than asked, because GitHub knows exactly who wrote what and a
// model reading a body cannot.
type discussion struct {
	// Total is every comment GitHub counted, bots included.
	Total int
	// ByReporter is how many comments the person who filed the issue wrote.
	ByReporter int
	// ByMaintainer is how many came from somebody with a project role, counted
	// whether or not they also filed the issue.
	//
	// These two overlap on purpose, so they do not sum to Total. A maintainer
	// who files an issue and then answers a question on it has written a
	// maintainer comment and a reporter comment, and counting it as only one of
	// those was a bug: it made a maintainer talking on their own issue read as
	// an outside reporter nobody had answered.
	ByMaintainer int
	// ByOthers is how many came from somebody who neither filed it nor has a
	// project role.
	ByOthers int
	// Bots is how many comments came from automation and were ignored.
	Bots int
	// People is how many distinct humans have commented.
	People int
	// ReporterIsMaintainer records whether the person who filed it speaks for
	// the project, which decides whether an unanswered thread means anything.
	ReporterIsMaintainer bool
	// Partial is set when the thread runs past one page, so the breakdown covers
	// part of it and the total does not.
	Partial bool
}

// WaitingOnUs says an outside reporter has asked and nobody from the project has
// answered.
//
// The reporter being a maintainer rules it out, because then there is nobody
// outside waiting. Measured on the reference repository that case is half the
// queue: the maintainer filed 49 of 106 open issues.
func (d discussion) WaitingOnUs() bool {
	return !d.ReporterIsMaintainer && d.ByReporter > 0 && d.ByMaintainer == 0
}

// issue is one open issue, with everything GitHub knows about it.
//
// The fields below the text are facts rather than judgements: exact, free, and
// never produced by a model.
type issue struct {
	Number int      `json:"number"`
	Title  string   `json:"title"`
	Body   string   `json:"body"`
	Labels []string `json:"labels"`
	// Milestone is the milestone title, empty when there is none.
	Milestone string `json:"milestone"`
	// AuthorAssociation is who filed it: OWNER, MEMBER, CONTRIBUTOR, NONE.
	// Handed to stage two as a fact with no weight attached, because the
	// measurement that would justify a weight measures what this repository did
	// rather than what it should have done.
	AuthorAssociation string `json:"author_association"`
	// LinkedPullRequest is the number of a pull request that references this
	// issue, or 0, and LinkedPullOpen says whether it is still open. Assignment
	// is aspirational in the reference repository, so an assignee says nothing
	// about whether anybody is working on something. A linked pull request does.
	LinkedPullRequest int  `json:"linked_pull_request"`
	LinkedPullOpen    bool `json:"linked_pull_open"`
	// Discussion is who has been talking, counted from the comments.
	Discussion discussion `json:"discussion"`
	CreatedAt  string     `json:"created_at"`
	UpdatedAt  string     `json:"updated_at"`
	// State and ClosedAt are set for a closed issue, which the tool scores as a
	// sanity check against work whose outcome is already known.
	State    string `json:"state"`
	ClosedAt string `json:"closed_at"`
}

// DaysOpen is how long the issue has been open, or was open before it closed.
func (i issue) DaysOpen(now time.Time) int {
	start, err := time.Parse(time.RFC3339, i.CreatedAt)
	if err != nil {
		return 0
	}
	end := now
	if i.ClosedAt != "" {
		if closed, err := time.Parse(time.RFC3339, i.ClosedAt); err == nil {
			end = closed
		}
	}
	return int(end.Sub(start).Hours() / 24)
}

// DaysIdle is how long since anything happened on the issue.
func (i issue) DaysIdle(now time.Time) int {
	touched, err := time.Parse(time.RFC3339, i.UpdatedAt)
	if err != nil {
		return 0
	}
	return int(now.Sub(touched).Hours() / 24)
}

// FiledByMaintainer says whether the issue came from inside the project.
func (i issue) FiledByMaintainer() bool {
	switch i.AuthorAssociation {
	case "OWNER", "MEMBER", "COLLABORATOR":
		return true
	}
	return false
}

// judgment is everything the two calls produced for one issue.
type judgment struct {
	Issue issue
	// Stage1 holds the answers about the text, keyed by question id.
	Stage1 map[string]answer
	// Digest is the exact text stage two read, kept so a surprising bucket can
	// be explained by looking at what the decision actually saw.
	Digest string
	// Horizon is the score Jev returned, 0 to 3, and HorizonIndex is the bucket
	// it rounded into.
	Horizon      float64
	HorizonIndex int
	// Undecided marks a score sitting within the deadband of a bucket edge.
	Undecided bool
	// SpilledFrom records the bucket this issue would have been in before a cap
	// pushed it down, or -1.
	SpilledFrom int
	InputTokens int
	LatencyMS   int64
}

// buildState assembles the three named state fields stage one carries.
//
// Named fields rather than one blob, so the model knows where the title ends
// and the body begins, and so one budget per field stops a long body crowding
// out the labels.
//
// Everything here is text somebody outside the project wrote, and an issue body
// arguing for its own priority moves the answer the same way a prompt would.
// Nothing downstream treats an answer as authority: the buckets are advice and
// the tool writes a file.
func buildState(in issue, set settings) map[string]string {
	labels := strings.Join(in.Labels, ", ")
	if labels == "" {
		labels = "none"
	}
	return map[string]string{
		"issue_title":  trimTo(in.Title, set.MaxTitleChars),
		"issue_body":   trimTo(in.Body, set.MaxBodyChars),
		"issue_labels": trimTo(labels, set.MaxLabelsChars),
	}
}

// trimTo cuts a string to at most n characters.
//
// Go indexes a string by byte, and a body with an emoji or an accented
// character in it would slice mid-rune and send invalid UTF-8, so this counts
// runes.
func trimTo(text string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	return string(runes[:n])
}

// buildDigest writes the fact table stage two reads.
//
// Plain `key: value` lines, one fact per line, because that is the shape a
// reader can check against a bucket they disagree with. The stage-one answers
// arrive as the numbers Jev gave them, so nothing is rounded away before the
// decision sees it.
//
// The body is deliberately absent. Stage two decides from the facts, and
// handing it the prose as well would let the text argue past them.
func buildDigest(
	in issue,
	stage1 map[string]answer,
	specs []spec,
	now time.Time,
) string {
	var b strings.Builder

	for _, s := range specs {
		a, ok := stage1[s.ID]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", s.Q.Label, renderAnswer(a))
	}

	filed := "an outside reporter"
	if in.FiledByMaintainer() {
		filed = "a project maintainer"
	}
	fmt.Fprintf(&b, "filed by: %s\n", filed)
	fmt.Fprintf(&b, "days open: %d\n", in.DaysOpen(now))
	fmt.Fprintf(&b, "days since last activity: %d\n", in.DaysIdle(now))
	writeDiscussion(&b, in.Discussion)

	switch {
	case in.LinkedPullRequest == 0:
		b.WriteString("linked pull request: none\n")
	case in.LinkedPullOpen:
		fmt.Fprintf(&b, "linked pull request: #%d, still open, so work is already under way\n",
			in.LinkedPullRequest)
	default:
		fmt.Fprintf(&b, "linked pull request: #%d, already merged\n", in.LinkedPullRequest)
	}

	if in.Milestone != "" {
		fmt.Fprintf(&b, "milestone: %s\n", in.Milestone)
	}
	if len(in.Labels) > 0 {
		fmt.Fprintf(&b, "labels: %s\n", strings.Join(in.Labels, ", "))
	}
	return b.String()
}

// writeDiscussion puts the comment counts into the digest.
//
// Written as sentences rather than bare numbers, because "nobody from the
// project has replied" is the thing worth acting on and `maintainer_comments: 0`
// buries it. The counts go in alongside, so a reader checking a bucket sees the
// same figures the decision did.
func writeDiscussion(b *strings.Builder, d discussion) {
	if d.Total == 0 {
		b.WriteString("discussion: nobody has commented\n")
		return
	}

	fmt.Fprintf(b, "comments: %d from %d different %s\n",
		d.Total, d.People, plural(d.People, "person", "people"))

	who := "the reporter"
	if d.ReporterIsMaintainer {
		// Saying so matters, because "the reporter has commented four times with
		// no reply" means something entirely different when the reporter is the
		// person who would be replying.
		who = "the reporter, who is a project maintainer"
	}
	fmt.Fprintf(b, "comments by %s: %d\n", who, d.ByReporter)
	fmt.Fprintf(b, "comments by a project maintainer: %d\n", d.ByMaintainer)
	if d.ByOthers > 0 {
		fmt.Fprintf(b, "comments by other people: %d\n", d.ByOthers)
	}
	if d.WaitingOnUs() {
		b.WriteString("discussion: an outside reporter has commented and no maintainer has replied\n")
	}
	if d.Bots > 0 {
		fmt.Fprintf(b, "bot comments ignored: %d\n", d.Bots)
	}
	if d.Partial {
		b.WriteString("note: the thread is longer than one page, so the breakdown is partial\n")
	}
}

// plural picks a word form for a count.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// renderAnswer turns one stage-one answer into the value side of a digest line.
//
// A score reads as its level text plus the number, so stage two gets the words
// and the precision together. A noul reads as a probability, and a choice as the
// option it picked.
func renderAnswer(a answer) string {
	switch {
	case a.Choice != "":
		return a.Choice
	case len(a.Legend) > 0:
		return fmt.Sprintf("%.2f of %d, %q", a.Score, len(a.Legend)-1, levelText(a))
	default:
		return fmt.Sprintf("%.2f", a.Noul)
	}
}

// levelText names the rubric level a score sits nearest.
func levelText(a answer) string {
	key := fmt.Sprintf("%d", int(math.Round(a.Score)))
	if text, ok := a.Legend[key]; ok {
		return fmt.Sprint(text)
	}
	return ""
}

// ask sends one state and one set of questions in a single call.
//
// Both stages go through here. Stage one carries eight questions about a body,
// and stage two carries one question about a digest, which is why the state and
// the questions are both parameters rather than built inside.
func ask(
	key string,
	model string,
	state map[string]string,
	questions map[string]wireQuestion,
	label string,
) (map[string]answer, usage, int64, error) {
	body, err := json.Marshal(map[string]any{
		"model":     model,
		"state":     state,
		"questions": questions,
	})
	if err != nil {
		return nil, usage{}, 0, fmt.Errorf("encoding the request for %s: %w", label, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	url := endpoint
	if custom := strings.TrimSpace(os.Getenv(jevEndpointEnv)); custom != "" {
		url = custom
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, usage{}, 0, fmt.Errorf("building the request for %s: %w", label, err)
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")

	started := time.Now()
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, usage{}, 0, fmt.Errorf("calling Jev for %s: %w", label, err)
	}
	defer response.Body.Close()

	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, usage{}, 0, fmt.Errorf("reading Jev's answer for %s: %w", label, err)
	}
	latency := time.Since(started).Milliseconds()

	if response.StatusCode != http.StatusOK {
		return nil, usage{}, latency, fmt.Errorf(
			"Jev returned %d for %s: %s", response.StatusCode, label, trimTo(string(raw), 200),
		)
	}

	var parsed apiResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, usage{}, latency, fmt.Errorf("parsing Jev's answer for %s: %w", label, err)
	}
	for id := range questions {
		if _, ok := parsed.Answers[id]; !ok {
			return nil, usage{}, latency, fmt.Errorf("Jev returned no answer for %s on %s", id, label)
		}
	}
	return parsed.Answers, parsed.Usage, latency, nil
}

// wireOf turns payload specs into the map the API wants.
func wireOf(specs []spec) map[string]wireQuestion {
	out := make(map[string]wireQuestion, len(specs))
	for _, s := range specs {
		out[s.ID] = wireQuestion{
			Type:         s.Q.Type,
			Instructions: s.Q.Instructions,
			Criteria:     s.Q.Criteria,
		}
	}
	return out
}

// triage runs both stages over one issue.
func triage(key string, p payload, in issue, now time.Time) (judgment, error) {
	out := judgment{Issue: in, SpilledFrom: -1}
	label := fmt.Sprintf("#%d", in.Number)

	stage1, used1, latency1, err := ask(
		key, p.Set.Model, buildState(in, p.Set), wireOf(p.Stage1), label+" stage 1",
	)
	if err != nil {
		return out, err
	}
	out.Stage1 = stage1
	out.InputTokens = used1.InputTokens
	out.LatencyMS = latency1

	out.Digest = buildDigest(in, stage1, p.Stage1, now)
	decision := map[string]wireQuestion{
		p.Decision.ID: {
			Type:         p.Decision.Type,
			Instructions: p.Decision.Instructions,
			Criteria:     p.Decision.Criteria,
		},
	}
	state := map[string]string{
		"issue_summary": trimTo(out.Digest, p.Set.MaxDigestChars),
	}

	stage2, used2, latency2, err := ask(key, p.Set.Model, state, decision, label+" stage 2")
	if err != nil {
		return out, err
	}
	out.InputTokens += used2.InputTokens
	out.LatencyMS += latency2

	a := stage2[p.Decision.ID]
	out.Horizon = a.Score
	out.HorizonIndex, out.Undecided = horizonOf(a.Score)
	return out, nil
}

// horizonOf turns a score into a bucket index, and says when it sits too close
// to an edge to call.
//
// The levels run 0 for "when time permits" up to 3 for "today", and the buckets
// run the other way, so the index is the level counted from the top. Rounding
// rather than clustering, because four levels already give four buckets: if a
// real queue comes back bunched inside one level, the report says so and the
// cuts become worth revisiting.
func horizonOf(score float64) (int, bool) {
	top := float64(len(horizons) - 1)
	level := math.Max(0, math.Min(top, score))
	index := int(top - math.Round(level))
	if index < 0 {
		index = 0
	}
	if index >= len(horizons) {
		index = len(horizons) - 1
	}
	// Distance to the nearest edge between levels, which sits on the halves.
	gap := math.Abs(level - math.Round(level))
	return index, math.Abs(gap-0.5) <= deadband
}

// bucket groups judged issues into the horizons, applying the caps.
//
// Inside a bucket the order is the score Jev returned, highest first, so the
// fraction between levels does the ordering that a plain choice would have
// thrown away.
func bucket(judged []judgment) [][]judgment {
	out := make([][]judgment, len(horizons))
	ordered := make([]judgment, len(judged))
	copy(ordered, judged)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Horizon != ordered[j].Horizon {
			return ordered[i].Horizon > ordered[j].Horizon
		}
		return ordered[i].Issue.Number < ordered[j].Issue.Number
	})

	for _, j := range ordered {
		index := j.HorizonIndex
		// Walk down until a bucket has room. A capped bucket spills into the next
		// one, and the row remembers where it came from.
		for index < len(horizons)-1 && horizonCaps[index] > 0 &&
			len(out[index]) >= horizonCaps[index] {
			if j.SpilledFrom < 0 {
				j.SpilledFrom = j.HorizonIndex
			}
			index++
		}
		out[index] = append(out[index], j)
	}
	return out
}

// choiceOf reads a choice answer by id, or returns a dash.
func choiceOf(stage1 map[string]answer, id string) string {
	if a, ok := stage1[id]; ok && a.Choice != "" {
		return a.Choice
	}
	return "-"
}

// noulOf reads a noul probability by id.
func noulOf(stage1 map[string]answer, id string) float64 {
	if a, ok := stage1[id]; ok {
		return a.Noul
	}
	return 0
}

// scoreOf reads a score by id.
func scoreOf(stage1 map[string]answer, id string) float64 {
	if a, ok := stage1[id]; ok {
		return a.Score
	}
	return 0
}
