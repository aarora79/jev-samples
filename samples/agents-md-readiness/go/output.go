// Printing: the explained blocks, then one padded table.
//
// Every number this file prints comes with a line saying what it means, built from
// the answer rather than from prose written by hand. A reader who has never seen a
// Jev answer should be able to act on the output without reading the docs first.
//
// The layout matches the Python sample beside this folder line for line, so the
// two implementations stay comparable and the recorded runs in either README stay
// true.
package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// tableHeader names the table columns, in order.
var tableHeader = []string{"Key", "Label", "Type", "Jev returned", "Weight", "Credit", "Judgement"}

// noulWords names each band of a Noul probability. A Noul carries one number, and
// these words say what that number claims, highest floor first so a search returns
// the first floor the value clears.
var noulWords = []band{
	{0.9, "yes"},
	{0.7, "probably yes"},
	{0.3, "unsettled"},
	{0.1, "probably no"},
	{0.0, "no"},
}

// printAnswers prints every answer, what its number means, the verdicts, and the
// table.
//
// The arguments are the document it scored, the questions in payload order, the
// response, the readiness it computed, the round-trip time, and the settings that
// hold the price.
func printAnswers(doc document, specs []spec, res apiResponse, ready float64, latency int, set settings) {
	// The header names the file and where it came from, so a run against a repo
	// says which URL it read.
	fmt.Printf("\n%s  (%s)\n\n", doc.Name, doc.Source)

	// A lookup keyed by question id, so the blocks below can reach one question by
	// name. specs stays the source of print order.
	byID := make(map[string]question, len(specs))
	for _, s := range specs {
		byID[s.ID] = s.Q
	}

	// Reading a Go map returns two values: the entry, and a bool saying whether it
	// was there. The ok check means a custom questions.yml without this question
	// prints no block instead of an empty one.
	if q, ok := byID["written_for"]; ok {
		a := res.Answers["written_for"]
		// %-15s pads a string to 15 characters on the left, which lines the columns
		// up without a table library.
		fmt.Printf("  %-15s %-12s (confidence %.2f)\n", q.Label, a.Choice, a.Confidence)
		fmt.Printf("      Choice: one label out of %d,\n", len(a.Probabilities))
		fmt.Printf("      scored %s.\n\n", describeChoice(a))
	}

	// The Scores, in payload order. continue skips to the next iteration, which is
	// how one loop over every question prints only the Scores.
	for _, s := range specs {
		if s.Q.Type != "score" {
			continue
		}
		a := res.Answers[s.ID]
		fmt.Printf("  %-15s %.1f / %d      (confidence %.2f)\n", s.Q.Label, a.Score, len(a.Legend)-1, a.Confidence)
		fmt.Printf("      %s\n", describeScore(a))
	}
	fmt.Println()

	// The Nouls, as a checklist. Each line pairs the probability with the word it
	// stands for, so a reader does not have to remember where the bands sit.
	fmt.Println("  agents.md checklist")
	for _, s := range specs {
		if s.Q.Type != "noul" {
			continue
		}
		a := res.Answers[s.ID]
		fmt.Printf("    %-24s %.2f   %s\n", s.Q.Label, a.Noul, noulWord(a.Noul))
	}
	fmt.Println("      Noul: one probability, and the number is the confidence. The agents.md FAQ")
	fmt.Println("      says the format requires no fields, so read these as coverage rather than")
	fmt.Println("      as a pass or a fail.")
	fmt.Println()

	// weakest_area carries no weight: it routes the fix rather than scoring the
	// file, so it prints after the checks it comments on.
	if q, ok := byID["weakest_area"]; ok {
		a := res.Answers["weakest_area"]
		fmt.Printf("  %-15s %-12s (confidence %.2f)\n", q.Label, a.Choice, a.Confidence)
		fmt.Printf("      Choice: %s.\n", describeChoice(a))
		// Python spells this count, so the Go spells it too and the two outputs stay
		// comparable. countWord falls back to digits past ten.
		fmt.Printf("      Jev ranks the %s areas and names one even when every area is strong,\n", countWord(len(a.Probabilities)))
		fmt.Println("      so read this next to the readiness number rather than on its own.")
		fmt.Println()
	}

	printVerdicts(byID, res.Answers, ready)
	fmt.Println()
	printTable(specs, res.Answers, ready)
	// Jev cannot count, so the singular and the plural come from Go.
	noun := "questions"
	if len(specs) == 1 {
		noun = "question"
	}
	// The closing line prices the run: token count, round trip, and dollars at the
	// price the payload carries.
	fmt.Printf("\n%d %s, %s input tokens, %d ms, $%.5f at $%v per million input tokens.\n",
		len(specs), noun, commas(res.Usage.InputTokens), latency, costUSD(res.Usage, set), set.InputUSDPerMillion)
}

// printVerdicts prints one advisory per action, each at its own threshold.
//
// The thresholds differ on purpose, and each sits next to the action it guards. A
// leaked key costs a rotation and an audit, so that one fires on a maybe; a
// suggested rewrite costs a developer an afternoon, so it waits for a low score.
// Gating per system, with one number for everything, would have to pick between
// the two.
//
// The -fail-under and -fail-on-credential flags turn the same numbers into an exit
// code. These lines say the same thing to a human reading the terminal.
func printVerdicts(byID map[string]question, answers map[string]answer, ready float64) {
	// A leaked credential costs a key rotation and an audit, so this sits low on
	// purpose and fires on a maybe.
	if a, ok := answers["leaks_secret"]; ok && a.Noul > 0.3 {
		fmt.Println("  -> read the file for a credential before you commit it")
	}

	// Missing test commands means an agent skips your tests, which is the one
	// failure the format's FAQ calls out.
	if a, ok := answers["test_commands"]; ok && a.Noul < 0.5 {
		fmt.Println("  -> add the test commands, because an agent runs the ones it finds")
	}

	// Rewriting a document is cheap and reversible, so 0.6 of the range is bar
	// enough to suggest it.
	if ready < 0.6 {
		if a, ok := answers["weakest_area"]; ok {
			fmt.Printf("  -> thin for an agent: start with %s\n", a.Choice)
		}
	}

	// A file written for a human reader tells an agent little, and this fires only
	// when Jev is sure of that reading. The byID check keeps the verdict quiet when
	// a custom payload dropped the question.
	if a, ok := answers["written_for"]; ok {
		if _, declared := byID["written_for"]; declared && a.Choice != "agent" && a.Confidence > 0.7 {
			fmt.Printf("  -> this reads as %s, so an agent gets no instructions from it\n", a.Choice)
		}
	}
}

// printTable prints the whole call as one padded table. The padding makes it
// readable in a terminal, and the pipes keep it valid markdown, so the same text
// pastes into a pull request.
func printTable(specs []spec, answers map[string]answer, ready float64) {
	// [][]string is a slice of rows, each row a slice of cells. Build every cell
	// first, measure the columns second, print third: the widths depend on the
	// content, so nothing can print until every row exists.
	rows := make([][]string, 0, len(specs))
	for _, s := range specs {
		a := answers[s.ID]
		kind := s.Q.Type
		if s.Q.Invert {
			kind += " (inverted)"
		}
		if s.Q.Weight == 0 { // diagnostic rather than graded
			rows = append(rows, []string{"`" + s.ID + "`", s.Q.Label, kind, returned(a), "", "", "routes the fix"})
			continue
		}
		rows = append(rows, []string{
			"`" + s.ID + "`",
			s.Q.Label,
			kind,
			returned(a),
			fmt.Sprintf("%.2f", s.Q.Weight),
			fmt.Sprintf("%.2f", credit(s.Q, a)),
			judgementOf(s.Q, a),
		})
	}

	// Measure each column: start at the header width, then stretch to the widest
	// cell underneath it.
	widths := make([]int, len(tableHeader))
	for i, cell := range tableHeader {
		widths[i] = len(cell)
	}
	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	// Header, then the dashed separator markdown wants, then the rows.
	fmt.Println(line(tableHeader, widths))
	dashes := make([]string, len(widths))
	for i, width := range widths {
		dashes[i] = strings.Repeat("-", width)
	}
	fmt.Println(line(dashes, widths))
	for _, row := range rows {
		fmt.Println(line(row, widths))
	}

	// Say what the readiness number is an average of, so a reader can check it
	// against the column above rather than trusting it.
	var total float64
	weightedSpecs := weighted(specs)
	for _, s := range weightedSpecs {
		total += s.Q.Weight
	}
	fmt.Printf("\n**Readiness %.2f / 1.00**, the weighted average of the credit column over %d questions carrying %.2f of weight.\n",
		ready, len(weightedSpecs), total)
	fmt.Println("Weights live in questions.yml, so raise the one you care about and re-run.")
}

// line pads one row of cells into a markdown row. Each cell gets trailing spaces
// up to its column width, which lines the pipes up in a terminal while leaving the
// markdown valid.
func line(cells []string, widths []int) string {
	padded := make([]string, len(cells))
	for i, cell := range cells {
		// strings.Repeat builds the padding. Counting bytes rather than display
		// width is fine here: every cell holds ASCII.
		padded[i] = cell + strings.Repeat(" ", widths[i]-len(cell))
	}
	return "| " + join(padded, " | ") + " |"
}

// printMissing says that neither name was there, and that readiness does not
// apply. It lists every place the run looked, so a reader can see whether the
// search or the repo is the problem.
func printMissing(doc document) {
	fmt.Printf("\nno %s\n\n", join(defaultTargets, " or "))
	for _, place := range doc.Looked {
		fmt.Printf("  looked at       %s\n", place)
	}
	// None rather than 0: a file that does not exist failed no check, and printing
	// zero would claim it failed all of them. The JSON report says null for the
	// same reason.
	fmt.Println("\n  readiness       None (not available)")
	fmt.Println("      Nothing reached Jev, so no question ran and no readiness exists.")
	fmt.Println("      A missing file has no score. Zero would mean it failed every check.")
}

// noulWord reads one Noul probability as a word.
//
// bandWord does the work, which means a probability sitting within the deadband of
// a band edge reads as both words, "unsettled to probably yes" for 0.69. Two runs
// of the same document then agree, where a bare comparison would print a different
// word each time a probability wobbled across 0.70.
func noulWord(value float64) string {
	return bandWord(value, noulWords)
}

// countWord spells a small count, so a sentence reads as prose. Past ten the digit
// reads better than the word, so the digit is what comes back.
func countWord(n int) string {
	words := []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"}
	if n >= 0 && n < len(words) {
		return words[n]
	}
	return fmt.Sprintf("%d", n)
}

// commas groups an integer with thousands separators, so 6410 prints as 6,410.
// Token counts are the one number in the output a reader compares between runs,
// and separators make that comparison quick.
func commas(n int) string {
	digits := fmt.Sprintf("%d", n)
	if len(digits) <= 3 {
		return digits
	}
	// strings.Builder grows one buffer instead of allocating a new string per
	// concatenation.
	var out strings.Builder
	// The leading group is whatever does not divide into threes: 6410 has one
	// digit, 64100 has two.
	lead := len(digits) % 3
	if lead > 0 {
		out.WriteString(digits[:lead])
	}
	// Then take three digits at a time. digits[i : i+3] is a substring, and the
	// comma goes in front of every group except the first thing written.
	for i := lead; i < len(digits); i += 3 {
		if out.Len() > 0 {
			out.WriteString(",")
		}
		out.WriteString(digits[i : i+3])
	}
	return out.String()
}

// join concatenates with a separator, kept here so the printing code reads in
// one place.
func join(parts []string, sep string) string {
	return strings.Join(parts, sep)
}

// readAll drains a response body into bytes. The raw bytes serve twice: -verbose
// prints them, and an error message quotes them.
func readAll(response *http.Response) ([]byte, error) {
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("reading the response: %w", err)
	}
	return raw, nil
}

// trim shortens a body for an error message. A 400 can return a long explanation,
// and a terminal wants the first line of it.
func trim(raw []byte) string {
	const limit = 200
	// string(raw) copies the bytes into a string, and TrimSpace drops the leading
	// and trailing whitespace an API tends to add.
	text := strings.TrimSpace(string(raw))
	if len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}
