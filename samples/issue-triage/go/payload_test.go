// Tests over the payload, the digest, and the bucketing.
//
// A file ending in _test.go holds tests and stays out of the built binary. Run
// them with:
//
//	go test ./...
//
// Each test is a function starting with Test that takes *testing.T, the handle
// it calls to report a failure. t.Fatalf prints a message and stops that test,
// and t.Errorf records one and carries on.
package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

// canonical is the payload a reader edits, one directory up, where the other
// samples in this repository keep theirs.
const canonical = "../questions.yml"

// TestEmbeddedPayloadMatchesCanonical compares the copy compiled into the
// binary against the canonical file on disk.
//
// Without this test the two drift in silence: somebody rewords a question one
// directory up, the binary keeps asking the old one, and the report disagrees
// with the file for a reason nobody can see.
func TestEmbeddedPayloadMatchesCanonical(t *testing.T) {
	want, err := os.ReadFile(canonical)
	if err != nil {
		t.Fatalf("reading %s: %v", canonical, err)
	}
	got, err := payloadFS.ReadFile(payloadName)
	if err != nil {
		t.Fatalf("reading the embedded %s: %v", payloadName, err)
	}
	// Go cannot compare two byte slices with ==, so bytes.Equal does it.
	if !bytes.Equal(want, got) {
		t.Fatalf("%s and the embedded copy differ: run ./build.sh, which copies it in", canonical)
	}
}

// TestEmbeddedPayloadLoads parses the embedded payload and checks the fields the
// rest of the program depends on.
func TestEmbeddedPayloadLoads(t *testing.T) {
	p, err := loadPayload("")
	if err != nil {
		t.Fatalf("loading the embedded payload: %v", err)
	}

	// %+v prints a struct with its field names, so a failure says which field
	// came back empty. Every budget matters: a zero one truncates that part of
	// the state to nothing and the answers then describe an issue that is not
	// there.
	switch {
	case p.Set.Model == "":
		t.Fatalf("no model pinned: %+v", p.Set)
	case p.Set.MaxTitleChars == 0 || p.Set.MaxBodyChars == 0 ||
		p.Set.MaxLabelsChars == 0 || p.Set.MaxDigestChars == 0:
		t.Fatalf("a state budget came back zero: %+v", p.Set)
	case p.Set.InputUSDPerMillion == 0:
		t.Fatalf("no price, so every run would report as free: %+v", p.Set)
	}
	if len(p.Stage1) == 0 {
		t.Fatal("no stage-one questions")
	}
}

// TestDecisionMatchesHorizons guards the join between the rubric and the output.
//
// The report groups by `horizons`, and the bucket an issue lands in is its
// rubric level counted from the top. A rubric with a different number of levels
// would quietly map every issue into the wrong bucket, so loadPayload refuses
// one and this checks that it does.
func TestDecisionMatchesHorizons(t *testing.T) {
	p, err := loadPayload("")
	if err != nil {
		t.Fatalf("loading the embedded payload: %v", err)
	}
	levels, ok := p.Decision.Criteria.([]any)
	if !ok {
		t.Fatalf("the decision criteria is %T, expected a list", p.Decision.Criteria)
	}
	if len(levels) != len(horizons) {
		t.Fatalf("the decision has %d levels and the report groups by %d horizons",
			len(levels), len(horizons))
	}
	if len(horizonCaps) != len(horizons) {
		t.Fatalf("%d caps for %d horizons", len(horizonCaps), len(horizons))
	}
}

// TestRejectsBadDecision checks the two ways a decision question can be
// unusable.
func TestRejectsBadDecision(t *testing.T) {
	cases := map[string]string{
		"a choice cannot order a bucket": `
model: jev-1.13.0
max_title_chars: 10
max_body_chars: 10
max_labels_chars: 10
max_digest_chars: 10
input_usd_per_million: 0.042
questions:
  a: {type: noul, instructions: x}
decision:
  id: work_horizon
  type: choice
  instructions: when
  criteria: {today: now}
`,
		"the wrong number of levels": `
model: jev-1.13.0
max_title_chars: 10
max_body_chars: 10
max_labels_chars: 10
max_digest_chars: 10
input_usd_per_million: 0.042
questions:
  a: {type: noul, instructions: x}
decision:
  id: work_horizon
  type: score
  instructions: when
  criteria: [a, b]
`,
	}
	for name, body := range cases {
		path := t.TempDir() + "/questions.yml"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("writing the fixture: %v", err)
		}
		if _, err := loadPayload(path); err == nil {
			t.Errorf("%s: loaded without an error", name)
		}
	}
}

// TestHorizonOf checks a score becomes the right bucket.
//
// Level 3 is "today" and horizons[0] is "Today", so the index is the level
// counted from the top. Getting that backwards would invert the whole report.
func TestHorizonOf(t *testing.T) {
	cases := []struct {
		score float64
		want  int
	}{
		{3.0, 0}, {2.9, 0},
		{2.0, 1}, {2.1, 1},
		{1.0, 2},
		{0.0, 3}, {0.2, 3},
		// Out of range in both directions clamps rather than panicking.
		{9.0, 0}, {-1.0, 3},
	}
	for _, c := range cases {
		got, _ := horizonOf(c.score)
		if got != c.want {
			t.Errorf("horizonOf(%.1f) = %d (%s), want %d (%s)",
				c.score, got, horizons[got], c.want, horizons[c.want])
		}
	}
}

// TestHorizonDeadband checks a score on a level boundary is flagged.
//
// Repeat calls move a score slightly, so a value halfway between two levels
// would flip buckets between runs. The report marks those rather than letting a
// reader read a changed digit as a change of mind.
func TestHorizonDeadband(t *testing.T) {
	if _, undecided := horizonOf(2.5); !undecided {
		t.Error("a score exactly between two levels is not flagged")
	}
	if _, undecided := horizonOf(3.0); undecided {
		t.Error("a score on a level is flagged as undecided")
	}
}

// TestBucketCapsSpillDownward checks a full bucket pushes the overflow down.
//
// The bucket names are capacity promises, so twenty issues cannot all be
// "today". The ones that move keep a record of where they came from, which the
// report prints.
func TestBucketCapsSpillDownward(t *testing.T) {
	var judged []judgment
	for n := 0; n < 12; n++ {
		judged = append(judged, judgment{
			Issue:        issue{Number: n + 1},
			Horizon:      3.0,
			HorizonIndex: 0,
			SpilledFrom:  -1,
		})
	}

	buckets := bucket(judged)
	if len(buckets[0]) != horizonCaps[0] {
		t.Errorf("today holds %d, want the cap of %d", len(buckets[0]), horizonCaps[0])
	}
	if len(buckets[1]) != 12-horizonCaps[0] {
		t.Errorf("this week holds %d, want the %d that spilled",
			len(buckets[1]), 12-horizonCaps[0])
	}
	for _, j := range buckets[1] {
		if j.SpilledFrom != 0 {
			t.Errorf("#%d spilled into this week without recording where from", j.Issue.Number)
		}
	}
	// Nothing may be lost on the way down.
	held := 0
	for _, rows := range buckets {
		held += len(rows)
	}
	if held != 12 {
		t.Errorf("%d issues went in and %d came out", 12, held)
	}
}

// TestBucketOrdersByScore checks the fraction between levels does the ordering.
//
// That fraction is the reason the decision is a score rather than a choice, so
// losing it would make the ordering inside a bucket arbitrary.
func TestBucketOrdersByScore(t *testing.T) {
	judged := []judgment{
		{Issue: issue{Number: 1}, Horizon: 2.2, HorizonIndex: 1, SpilledFrom: -1},
		{Issue: issue{Number: 2}, Horizon: 2.4, HorizonIndex: 1, SpilledFrom: -1},
		{Issue: issue{Number: 3}, Horizon: 1.8, HorizonIndex: 1, SpilledFrom: -1},
	}
	week := bucket(judged)[1]
	want := []int{2, 1, 3}
	for index, number := range want {
		if week[index].Issue.Number != number {
			t.Fatalf("bucket order %v, want %v",
				[]int{week[0].Issue.Number, week[1].Issue.Number, week[2].Issue.Number}, want)
		}
	}
}

// TestDigestCarriesFactsAndNotTheBody is the one that guards the design.
//
// Stage two decides from the facts. Handing it the body as well would let a
// persuasively written issue argue past them, which is the failure this split
// exists to prevent.
func TestDigestCarriesFactsAndNotTheBody(t *testing.T) {
	p, err := loadPayload("")
	if err != nil {
		t.Fatalf("loading the embedded payload: %v", err)
	}
	now := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	in := issue{
		Number:            42,
		Title:             "health checker ignores HTTPS_PROXY",
		Body:              "THIS-IS-THE-BODY and it should not reach stage two",
		Labels:            []string{"bug", "api"},
		AuthorAssociation: "NONE",
		LinkedPullRequest: 99,
		LinkedPullOpen:    true,
		Discussion: discussion{
			Total: 5, ByReporter: 2, ByMaintainer: 1, ByOthers: 1, Bots: 1, People: 4,
		},
		CreatedAt: "2026-09-30T00:00:00Z",
		UpdatedAt: "2026-10-05T00:00:00Z",
	}
	stage1 := map[string]answer{
		"report_kind":      {Choice: "defect"},
		"touches_security": {Noul: 0.93},
		"claims_blocking":  {Noul: 0.39},
		"urgency_stated":   {Score: 1.95, Legend: map[string]any{"0": "a", "1": "b", "2": "c"}},
	}

	digest := buildDigest(in, stage1, p.Stage1, now)

	if strings.Contains(digest, "THIS-IS-THE-BODY") {
		t.Error("the digest carries the issue body, so stage two can be argued at")
	}
	for _, want := range []string{
		"defect", "0.93", "days open: 7", "days since last activity: 2",
		"#99, still open", "an outside reporter", "comments: 5 from 4 different people",
		"bug, api", "comments by the reporter: 2", "bot comments ignored: 1",
	} {
		if !strings.Contains(digest, want) {
			t.Errorf("the digest is missing %q:\n%s", want, digest)
		}
	}
	// A score should arrive with its level text, so the decision reads words as
	// well as a number.
	if !strings.Contains(digest, "1.95 of 2") {
		t.Errorf("a score lost its level count:\n%s", digest)
	}
}

// TestDaysFieldsHandleAClosedIssue checks the date facts when scoring closed
// work.
//
// Scoring closed issues is the sanity check, and for one of those "days open"
// has to mean until it closed rather than until today, or every closed issue
// looks ancient.
func TestDaysFieldsHandleAClosedIssue(t *testing.T) {
	now := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	in := issue{
		CreatedAt: "2026-01-01T00:00:00Z",
		ClosedAt:  "2026-01-11T00:00:00Z",
		UpdatedAt: "2026-01-11T00:00:00Z",
	}
	if got := in.DaysOpen(now); got != 10 {
		t.Errorf("a closed issue was open %d days, want 10", got)
	}
	// A missing timestamp returns zero rather than a wild number.
	if got := (issue{}).DaysOpen(now); got != 0 {
		t.Errorf("an issue with no dates reports %d days open, want 0", got)
	}
}

// TestSampleIsReproducible checks a seeded sample repeats.
//
// An unreproducible sample cannot be compared against itself after a question
// is reworded, which is the only reason to sample.
func TestSampleIsReproducible(t *testing.T) {
	issues := make([]issue, 50)
	for n := range issues {
		issues[n] = issue{Number: n + 1}
	}
	first := sampleOf(issues, 10, 7)
	second := sampleOf(issues, 10, 7)
	for index := range first {
		if first[index].Number != second[index].Number {
			t.Fatal("the same seed produced a different sample")
		}
	}
	if different := sampleOf(issues, 10, 8); different[0].Number == first[0].Number &&
		different[1].Number == first[1].Number {
		t.Error("two seeds produced the same sample, so the seed is being ignored")
	}
	if len(first) != 10 {
		t.Errorf("asked for 10 and got %d", len(first))
	}
}

// TestTrimToKeepsValidText checks the budget cut does not split a character.
//
// Go indexes a string by byte, so cutting a body at a byte offset can land in
// the middle of a multi-byte character and send invalid UTF-8 to the API. Issue
// bodies carry emoji and accented names routinely.
func TestTrimToKeepsValidText(t *testing.T) {
	cases := []struct {
		text string
		n    int
		want string
	}{
		{"hello", 3, "hel"},
		{"hello", 99, "hello"},
		{"hello", 0, ""},
		{"héllo", 2, "hé"},
		// A four-byte character, which is where a byte-wise cut does the most
		// damage. Issue bodies carry these routinely.
		{"\U0001D11E\U0001D11E\U0001D11E", 2, "\U0001D11E\U0001D11E"},
	}
	for _, c := range cases {
		if got := trimTo(c.text, c.n); got != c.want {
			t.Errorf("trimTo(%q, %d) = %q, want %q", c.text, c.n, got, c.want)
		}
	}
}

// TestMarkdownTableEscapesPipes checks a title cannot break the table.
func TestMarkdownTableEscapesPipes(t *testing.T) {
	if got := escapePipes("a | b"); got != `a \| b` {
		t.Errorf("escapePipes left a bare pipe: %q", got)
	}
	table := markdownTable([][]string{{"a", "b"}, {"1", "2"}})
	lines := strings.Split(strings.TrimSpace(table), "\n")
	if len(lines) != 3 {
		t.Fatalf("a two-row table rendered %d lines, want 3 with the rule", len(lines))
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
			t.Errorf("row is not delimited: %q", line)
		}
	}
}

// TestCommas checks the token count reads properly in the preamble.
func TestCommas(t *testing.T) {
	for input, want := range map[int]string{
		0: "0", 42: "42", 999: "999", 1000: "1,000", 913194: "913,194",
	} {
		if got := commas(input); got != want {
			t.Errorf("commas(%d) = %q, want %q", input, got, want)
		}
	}
}

// TestSplitRepoAcceptsEitherOrder checks the repository is found wherever it
// sits among the flags.
//
// Go's flag package stops at the first non-flag argument, so without this the
// natural `issue-triage owner/repo -data out` would drop -data in silence. That
// bug cost a run that wrote its report to the wrong directory.
func TestSplitRepoAcceptsEitherOrder(t *testing.T) {
	cases := []struct {
		name string
		args []string
		rest []string
	}{
		{"repo first", []string{"a/b", "-stdout"}, []string{"-stdout"}},
		{"repo last", []string{"-stdout", "a/b"}, []string{"-stdout"}},
		{"repo after a flag value", []string{"-data", "out", "a/b"}, []string{"-data", "out"}},
		{"a flag value containing a slash", []string{"-data", "x/y", "a/b"},
			[]string{"-data", "x/y"}},
		{"joined flag value", []string{"-data=x/y", "a/b"}, []string{"-data=x/y"}},
	}
	// A small stand-in for the real flag set, so the test says which flags take
	// a value rather than depending on package state.
	takesValue := func(name string) bool { return name == "data" || name == "state" }

	for _, c := range cases {
		repo, rest := splitRepo(c.args, takesValue)
		if repo != "a/b" {
			t.Errorf("%s: repo = %q, want a/b", c.name, repo)
		}
		if strings.Join(rest, " ") != strings.Join(c.rest, " ") {
			t.Errorf("%s: rest = %v, want %v", c.name, rest, c.rest)
		}
	}
	// No repository at all has to come back empty rather than eating a flag.
	if repo, _ := splitRepo([]string{"-version"}, takesValue); repo != "" {
		t.Errorf("found a repository %q where there was none", repo)
	}
}

// TestCountComments checks who gets credited for a comment.
//
// Three things can go wrong here and each one misreads a thread. Counting a bot
// makes a CI job posting twelve build failures look like a contested
// discussion. Crediting the reporter's own comments to the project hides an
// unanswered question. Counting one person twice inflates how many people are
// affected.
func TestCountComments(t *testing.T) {
	node := wireIssue{
		Author: &wireActor{Login: "reporter", TypeName: "User"},
		Comments: struct {
			TotalCount int `json:"totalCount"`
			Nodes      []struct {
				AuthorAssociation string     `json:"authorAssociation"`
				Author            *wireActor `json:"author"`
			} `json:"nodes"`
		}{
			TotalCount: 7,
			Nodes: []struct {
				AuthorAssociation string     `json:"authorAssociation"`
				Author            *wireActor `json:"author"`
			}{
				{"NONE", &wireActor{Login: "reporter", TypeName: "User"}},
				{"NONE", &wireActor{Login: "reporter", TypeName: "User"}},
				{"MEMBER", &wireActor{Login: "maintainer", TypeName: "User"}},
				{"CONTRIBUTOR", &wireActor{Login: "passerby", TypeName: "User"}},
				// A real Bot account.
				{"NONE", &wireActor{Login: "dependabot", TypeName: "Bot"}},
				// An app posting through a user account, which GitHub reports as a
				// User with a [bot] suffix.
				{"NONE", &wireActor{Login: "codecov[bot]", TypeName: "User"}},
				// A deleted account counts as a comment and not as a person.
				{"NONE", nil},
			},
		},
	}

	got := countComments(node)
	for _, c := range []struct {
		name string
		got  int
		want int
	}{
		{"total", got.Total, 7},
		{"by the reporter", got.ByReporter, 2},
		{"by a maintainer", got.ByMaintainer, 1},
		{"by others", got.ByOthers, 2}, // the passerby and the deleted account
		{"bots", got.Bots, 2},
		{"distinct people", got.People, 3}, // reporter, maintainer, passerby
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
	if got.Partial {
		t.Error("a thread of 7 with 7 read reports as partial")
	}
	// A maintainer did reply in this fixture, so nothing is waiting on us.
	// TestWaitingOnUs covers the case where none has.
	if got.WaitingOnUs() {
		t.Error("a thread a maintainer replied on reads as waiting on us")
	}
	if got.ReporterIsMaintainer {
		t.Error("a reporter with association NONE is recorded as a maintainer")
	}
}

// TestMaintainerOnTheirOwnIssue is the case verifying against GitHub caught.
//
// Issue #358 of the reference repository was filed by a MEMBER who then
// commented four times. Crediting those to the reporter and not to the project
// made it read as an outside reporter nobody had answered, which is the
// opposite of what was happening. The maintainer filed 49 of that repository's
// 106 open issues, so this is half the queue rather than an edge case.
func TestMaintainerOnTheirOwnIssue(t *testing.T) {
	node := wireIssue{
		AuthorAssociation: "MEMBER",
		Author:            &wireActor{Login: "maintainer", TypeName: "User"},
	}
	node.Comments.TotalCount = 5
	for n := 0; n < 4; n++ {
		node.Comments.Nodes = append(node.Comments.Nodes, struct {
			AuthorAssociation string     `json:"authorAssociation"`
			Author            *wireActor `json:"author"`
		}{"MEMBER", &wireActor{Login: "maintainer", TypeName: "User"}})
	}
	node.Comments.Nodes = append(node.Comments.Nodes, struct {
		AuthorAssociation string     `json:"authorAssociation"`
		Author            *wireActor `json:"author"`
	}{"CONTRIBUTOR", &wireActor{Login: "passerby", TypeName: "User"}})

	got := countComments(node)
	if !got.ReporterIsMaintainer {
		t.Error("a MEMBER who filed the issue is not recorded as a maintainer")
	}
	// One comment, two roles: the counts overlap rather than partition.
	if got.ByReporter != 4 {
		t.Errorf("by the reporter = %d, want 4", got.ByReporter)
	}
	if got.ByMaintainer != 4 {
		t.Errorf("by a maintainer = %d, want 4, counted even though they filed it",
			got.ByMaintainer)
	}
	if got.ByOthers != 1 {
		t.Errorf("by others = %d, want 1", got.ByOthers)
	}
	if got.WaitingOnUs() {
		t.Error("a maintainer talking on their own issue reads as waiting on us")
	}
}

// TestWaitingOnUs checks the derived fact the report leans on.
func TestWaitingOnUs(t *testing.T) {
	cases := []struct {
		name string
		d    discussion
		want bool
	}{
		{"reporter asked, nobody answered", discussion{ByReporter: 2}, true},
		{"a maintainer replied", discussion{ByReporter: 2, ByMaintainer: 1}, false},
		{"nobody said anything", discussion{}, false},
		{"only an outsider spoke", discussion{ByOthers: 3}, false},
	}
	for _, c := range cases {
		if got := c.d.WaitingOnUs(); got != c.want {
			t.Errorf("%s: WaitingOnUs = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestPartialThreadIsFlagged checks a long thread says its breakdown is partial.
//
// GitHub returns the true total and one page of comments, so an issue with 300
// comments has a breakdown of the first hundred. Reporting that as the whole
// would understate every count.
func TestPartialThreadIsFlagged(t *testing.T) {
	node := wireIssue{Author: &wireActor{Login: "a"}}
	node.Comments.TotalCount = 300
	for n := 0; n < 100; n++ {
		node.Comments.Nodes = append(node.Comments.Nodes, struct {
			AuthorAssociation string     `json:"authorAssociation"`
			Author            *wireActor `json:"author"`
		}{"NONE", &wireActor{Login: "a"}})
	}
	if got := countComments(node); !got.Partial {
		t.Error("a 300-comment thread read one page deep is not flagged as partial")
	}
}

// TestBotDetection covers both ways GitHub reports automation.
func TestBotDetection(t *testing.T) {
	cases := map[*wireActor]bool{
		{Login: "dependabot", TypeName: "Bot"}:     true,
		{Login: "codecov[bot]", TypeName: "User"}:  true,
		{Login: "a-real-person", TypeName: "User"}: false,
		{Login: "robotics-fan", TypeName: "User"}:  false,
	}
	for actor, want := range cases {
		if got := actor.IsBot(); got != want {
			t.Errorf("%s (%s): IsBot = %v, want %v", actor.Login, actor.TypeName, got, want)
		}
	}
	// A nil actor is a deleted account, not a bot.
	var missing *wireActor
	if missing.IsBot() {
		t.Error("a deleted account reads as a bot")
	}
	if missing.Name() != "(deleted)" {
		t.Errorf("a deleted account is named %q", missing.Name())
	}
}
