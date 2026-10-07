// The report: one markdown file, a summary table and a table per bucket.
//
// Markdown because the output is read by a person and pasted into an issue, a
// pull request or a standup note. Every table pads to its own widest cell, so
// the same text reads in a terminal and renders on GitHub.
//
// Every row carries why it is there. That column is the thing that makes the
// tool correctable: when a bucket looks wrong, the reason is on the same line,
// so the fix is a reworded question rather than a guess at a weight.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// maxTitleChars keeps a title inside one table cell. A longer one is cut and
	// the issue number is a link anyway.
	maxTitleChars = 72
	// reasonCount is how many reasons to print per issue. Three fits a cell and
	// covers the signals that moved a decision.
	reasonCount = 3
)

// reportInput is everything the report needs that is not a judgment.
type reportInput struct {
	Repo        string
	State       string
	TotalIssues int
	Scored      int
	Failed      int
	InputTokens int
	CostUSD     float64
	Model       string
	RanAt       time.Time
	Elapsed     time.Duration
}

// writeMarkdown renders the report and writes it, returning the path.
func writeMarkdown(in reportInput, buckets [][]judgment, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	name := fmt.Sprintf("triage-%s-%s.md", slug(in.Repo), strings.ToLower(in.State))
	path := filepath.Join(dir, name)

	text := renderMarkdown(in, buckets)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}

// renderMarkdown builds the whole document.
func renderMarkdown(in reportInput, buckets [][]judgment) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# What to work on in %s\n\n", in.Repo)
	b.WriteString(preamble(in, buckets))
	b.WriteString("\n")
	b.WriteString(summaryTable(in, buckets))

	for index, rows := range buckets {
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n\n", horizons[index])
		b.WriteString(bucketNote(index, rows))
		b.WriteString(bucketTable(in.Repo, rows))
	}

	b.WriteString("\n")
	b.WriteString(footnote(in))
	return b.String()
}

// preamble says what the run did and what it did not do.
func preamble(in reportInput, buckets [][]judgment) string {
	var b strings.Builder

	fmt.Fprintf(&b,
		"%d %s issues read on %s, scored with two %s calls each: one that reads the issue text, "+
			"one that reads a summary of the answers and decides when to pick it up.\n",
		in.Scored, strings.ToLower(in.State), in.RanAt.Format("2 January 2006"), in.Model,
	)
	fmt.Fprintf(&b,
		"The run took %s and cost $%.4f for %s input tokens.\n",
		in.Elapsed.Round(time.Second), in.CostUSD, commas(in.InputTokens),
	)
	if in.Failed > 0 {
		fmt.Fprintf(&b, "%d issue(s) failed to score and are absent from the tables below.\n",
			in.Failed)
	}

	b.WriteString("\nThe four buckets are advice. ")
	b.WriteString("Nothing here was closed, labelled or commented on, and the ordering inside ")
	b.WriteString("each bucket is the score the model returned.\n")

	if spilled := countSpilled(buckets); spilled > 0 {
		fmt.Fprintf(&b,
			"\n%d issue(s) scored into a fuller bucket than it could hold and moved down one. "+
				"Today holds at most %d and this week at most %d, because a list of thirty things "+
				"to do today is not a plan. Those rows are marked.\n",
			spilled, horizonCaps[0], horizonCaps[1],
		)
	}
	return b.String()
}

// countSpilled totals the rows a cap pushed down.
func countSpilled(buckets [][]judgment) int {
	count := 0
	for _, rows := range buckets {
		for _, j := range rows {
			if j.SpilledFrom >= 0 {
				count++
			}
		}
	}
	return count
}

// summaryTable is the one-row-per-bucket table.
func summaryTable(in reportInput, buckets [][]judgment) string {
	rows := [][]string{{"horizon", "issues", "numbers"}}
	for index, held := range buckets {
		numbers := make([]string, 0, len(held))
		for _, j := range held {
			numbers = append(numbers, fmt.Sprintf("#%d", j.Issue.Number))
		}
		listed := strings.Join(numbers, ", ")
		if listed == "" {
			listed = "none"
		}
		rows = append(rows, []string{
			horizons[index], fmt.Sprint(len(held)), listed,
		})
	}
	return markdownTable(rows)
}

// bucketNote explains a bucket in one line before its table.
func bucketNote(index int, rows []judgment) string {
	switch index {
	case 0:
		return fmt.Sprintf(
			"The %d issue(s) the model would start on now.\n\n", len(rows))
	case len(horizons) - 1:
		return "Everything else, in score order. Worth a skim for anything " +
			"miscategorised rather than a plan.\n\n"
	default:
		return ""
	}
}

// bucketTable is the detail table for one bucket.
func bucketTable(repo string, held []judgment) string {
	rows := [][]string{{"issue", "title", "kind", "area", "why it is here"}}
	for _, j := range held {
		rows = append(rows, []string{
			issueLink(repo, j.Issue.Number),
			escapePipes(trimTo(j.Issue.Title, maxTitleChars)),
			choiceOf(j.Stage1, "report_kind"),
			choiceOf(j.Stage1, "area"),
			strings.Join(reasons(j), ", "),
		})
	}
	return markdownTable(rows)
}

// issueLink renders an issue number as a markdown link.
func issueLink(repo string, number int) string {
	return fmt.Sprintf("[#%d](https://github.com/%s/issues/%d)", number, repo, number)
}

// escapePipes keeps a title with a pipe in it from breaking the table.
func escapePipes(text string) string {
	return strings.ReplaceAll(text, "|", "\\|")
}

// reasonRule is one thing worth saying about an issue, and when to say it.
//
// Ordered by how much each would move a decision, and the first few that apply
// go in the cell. Facts come before model answers, because a fact is certain and
// a probability is not.
type reasonRule struct {
	// when decides whether the reason applies.
	when func(judgment, time.Time) bool
	// text renders it.
	text func(judgment, time.Time) string
}

var reasonRules = []reasonRule{
	{
		// A score sitting on a bucket edge is the most useful thing to know about
		// a row, because it is the one most likely to move on a rerun.
		when: func(j judgment, _ time.Time) bool { return j.Undecided },
		text: func(j judgment, _ time.Time) string {
			return fmt.Sprintf("on a bucket edge at %.2f", j.Horizon)
		},
	},
	{
		when: func(j judgment, _ time.Time) bool { return j.SpilledFrom >= 0 },
		text: func(j judgment, _ time.Time) string {
			return fmt.Sprintf("scored %s, moved down for room", horizons[j.SpilledFrom])
		},
	},
	{
		when: func(j judgment, _ time.Time) bool { return j.Issue.LinkedPullOpen },
		text: func(j judgment, _ time.Time) string {
			return fmt.Sprintf("#%d already open", j.Issue.LinkedPullRequest)
		},
	},
	{
		when: func(j judgment, _ time.Time) bool { return noulOf(j.Stage1, "claims_blocking") >= 0.5 },
		text: func(j judgment, _ time.Time) string {
			return fmt.Sprintf("says work is blocked (%.2f)", noulOf(j.Stage1, "claims_blocking"))
		},
	},
	{
		// An unanswered reporter is the discussion fact most worth surfacing,
		// because it is the one somebody can clear in a minute.
		when: func(j judgment, _ time.Time) bool { return j.Issue.Discussion.WaitingOnUs() },
		text: func(j judgment, _ time.Time) string {
			d := j.Issue.Discussion
			return fmt.Sprintf("outside reporter commented %d time(s), no reply", d.ByReporter)
		},
	},
	{
		// Several different people on one thread means more than one person is
		// affected, which a single insistent reporter cannot tell you.
		when: func(j judgment, _ time.Time) bool { return j.Issue.Discussion.People >= 3 },
		text: func(j judgment, _ time.Time) string {
			return fmt.Sprintf("%d people in the thread", j.Issue.Discussion.People)
		},
	},
	{
		when: func(j judgment, _ time.Time) bool { return noulOf(j.Stage1, "touches_security") >= 0.5 },
		text: func(j judgment, _ time.Time) string {
			return fmt.Sprintf("security (%.2f)", noulOf(j.Stage1, "touches_security"))
		},
	},
	{
		when: func(j judgment, _ time.Time) bool { return noulOf(j.Stage1, "is_performance") >= 0.5 },
		text: func(j judgment, _ time.Time) string {
			return fmt.Sprintf("performance (%.2f)", noulOf(j.Stage1, "is_performance"))
		},
	},
	{
		when: func(j judgment, _ time.Time) bool { return scoreOf(j.Stage1, "urgency_stated") >= 1.5 },
		text: func(j judgment, _ time.Time) string {
			return fmt.Sprintf("urgent in the body (%.2f of 2)", scoreOf(j.Stage1, "urgency_stated"))
		},
	},
	{
		when: func(j judgment, _ time.Time) bool { return noulOf(j.Stage1, "has_repro") >= 0.5 },
		text: func(judgment, time.Time) string { return "has a repro" },
	},
	{
		when: func(j judgment, _ time.Time) bool {
			return noulOf(j.Stage1, "has_proposed_solution") >= 0.5
		},
		text: func(judgment, time.Time) string { return "proposes a fix" },
	},
	{
		when: func(j judgment, now time.Time) bool { return j.Issue.DaysIdle(now) >= 90 },
		text: func(j judgment, now time.Time) string {
			return fmt.Sprintf("untouched for %d days", j.Issue.DaysIdle(now))
		},
	},
	{
		when: func(j judgment, now time.Time) bool { return j.Issue.DaysOpen(now) <= 7 },
		text: func(j judgment, now time.Time) string {
			return fmt.Sprintf("opened %d days ago", j.Issue.DaysOpen(now))
		},
	},
	{
		when: func(j judgment, _ time.Time) bool { return !j.Issue.FiledByMaintainer() },
		text: func(judgment, time.Time) string { return "outside reporter" },
	},
}

// reasons names the few things most worth knowing about one row.
func reasons(j judgment) []string {
	now := time.Now().UTC()
	out := make([]string, 0, reasonCount)
	for _, rule := range reasonRules {
		if len(out) >= reasonCount {
			break
		}
		if rule.when(j, now) {
			out = append(out, rule.text(j, now))
		}
	}
	if len(out) == 0 {
		out = append(out, fmt.Sprintf("scored %.2f", j.Horizon))
	}
	return out
}

// footnote closes the document with how to read it and how to change it.
func footnote(in reportInput) string {
	return "---\n\n" +
		"Buckets come from one question per issue, asked over a summary of eight " +
		"questions about the text and the facts GitHub already knows. " +
		"A row in the wrong bucket is usually a question worth rewording: the " +
		"reasons column says which signal put it there. " +
		"The questions live in `questions.yml`.\n"
}

// markdownTable pads every column to its widest cell and renders the table.
//
// Padding costs nothing and buys a table that reads as plain text, which is
// where most of these end up before anybody renders them.
func markdownTable(rows [][]string) string {
	if len(rows) < 2 {
		return ""
	}
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for column, cell := range row {
			if column < len(widths) && len(cell) > widths[column] {
				widths[column] = len(cell)
			}
		}
	}

	var b strings.Builder
	write := func(cells []string) {
		b.WriteString("|")
		for column, cell := range cells {
			if column < len(widths) {
				fmt.Fprintf(&b, " %-*s |", widths[column], cell)
			}
		}
		b.WriteString("\n")
	}

	write(rows[0])
	rule := make([]string, len(widths))
	for column, width := range widths {
		rule[column] = strings.Repeat("-", width)
	}
	write(rule)
	for _, row := range rows[1:] {
		write(row)
	}
	return b.String()
}

// commas renders a count with thousands separators.
func commas(n int) string {
	text := fmt.Sprint(n)
	if len(text) <= 3 {
		return text
	}
	var parts []string
	for len(text) > 3 {
		parts = append([]string{text[len(text)-3:]}, parts...)
		text = text[:len(text)-3]
	}
	return text + "," + strings.Join(parts, ",")
}

// slug turns owner/repo into a filename-safe owner-repo.
func slug(repo string) string {
	out := make([]rune, 0, len(repo))
	for _, char := range repo {
		if char == '/' || char == ' ' {
			out = append(out, '-')
			continue
		}
		out = append(out, char)
	}
	return string(out)
}

// sortedKeys lists a map's keys alphabetically.
//
// Go randomises map iteration deliberately, so anything that walks a map and
// prints needs its own ordering or the output changes between runs.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
