// Printing: the explained blocks, then one padded table.
package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// tableHeader names the table columns, in order.
var tableHeader = []string{"Key", "Label", "Type", "Jev returned", "Weight", "Credit", "Judgement"}

// printAnswers prints every answer, what the number means, the verdicts, and the
// table, in the same layout as the Python sample beside this file.
func printAnswers(doc document, specs []spec, res apiResponse, ready float64, latency int, set settings) {
	fmt.Printf("\n%s  (%s)\n\n", doc.Name, doc.Source)

	byID := make(map[string]question, len(specs))
	for _, s := range specs {
		byID[s.ID] = s.Q
	}

	if q, ok := byID["written_for"]; ok {
		a := res.Answers["written_for"]
		fmt.Printf("  %-15s %-12s (confidence %.2f)\n", q.Label, a.Choice, a.Confidence)
		fmt.Printf("      Choice: one label out of %d,\n", len(a.Probabilities))
		fmt.Printf("      scored %s.\n\n", describeChoice(a))
	}

	for _, s := range specs {
		if s.Q.Type != "score" {
			continue
		}
		a := res.Answers[s.ID]
		fmt.Printf("  %-15s %.1f / %d      (confidence %.2f)\n", s.Q.Label, a.Score, len(a.Legend)-1, a.Confidence)
		fmt.Printf("      %s\n", describeScore(a))
	}
	fmt.Println()

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

	if q, ok := byID["weakest_area"]; ok {
		a := res.Answers["weakest_area"]
		fmt.Printf("  %-15s %-12s (confidence %.2f)\n", q.Label, a.Choice, a.Confidence)
		fmt.Printf("      Choice: %s.\n", describeChoice(a))
		fmt.Printf("      Jev ranks the %d areas and names one even when every area is strong,\n", len(a.Probabilities))
		fmt.Println("      so read this next to the readiness number rather than on its own.")
		fmt.Println()
	}

	printVerdicts(byID, res.Answers, ready)
	fmt.Println()
	printTable(specs, res.Answers, ready)
	noun := "questions"
	if len(specs) == 1 {
		noun = "question"
	}
	fmt.Printf("\n%d %s, %s input tokens, %d ms, $%.5f at $%v per million input tokens.\n",
		len(specs), noun, commas(res.Usage.InputTokens), latency, costUSD(res.Usage, set), set.InputUSDPerMillion)
}

// printVerdicts prints one advisory per action, each at its own threshold. The
// -fail-under and -fail-on-credential flags turn the same numbers into an exit
// code; these lines say the same thing to a reader.
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

	if a, ok := answers["written_for"]; ok {
		if _, declared := byID["written_for"]; declared && a.Choice != "agent" && a.Confidence > 0.7 {
			fmt.Printf("  -> this reads as %s, so an agent gets no instructions from it\n", a.Choice)
		}
	}
}

// printTable prints the whole call as one padded table. The padding makes it
// readable in a terminal, and the pipes keep it valid markdown.
func printTable(specs []spec, answers map[string]answer, ready float64) {
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

	fmt.Println(line(tableHeader, widths))
	dashes := make([]string, len(widths))
	for i, width := range widths {
		dashes[i] = strings.Repeat("-", width)
	}
	fmt.Println(line(dashes, widths))
	for _, row := range rows {
		fmt.Println(line(row, widths))
	}

	var total float64
	weightedSpecs := weighted(specs)
	for _, s := range weightedSpecs {
		total += s.Q.Weight
	}
	fmt.Printf("\n**Readiness %.2f / 1.00**, the weighted average of the credit column over %d questions carrying %.2f of weight.\n",
		ready, len(weightedSpecs), total)
	fmt.Println("Weights live in questions.yml, so raise the one you care about and re-run.")
}

// line pads one row of cells into a markdown row.
func line(cells []string, widths []int) string {
	padded := make([]string, len(cells))
	for i, cell := range cells {
		padded[i] = cell + strings.Repeat(" ", widths[i]-len(cell))
	}
	return "| " + join(padded, " | ") + " |"
}

// printMissing says that neither name was there, and that readiness does not
// apply.
func printMissing(doc document) {
	fmt.Printf("\nno %s\n\n", join(defaultTargets, " or "))
	for _, place := range doc.Looked {
		fmt.Printf("  looked at       %s\n", place)
	}
	fmt.Println("\n  readiness       None (not available)")
	fmt.Println("      Nothing reached Jev, so no question ran and no readiness exists.")
	fmt.Println("      A missing file has no score. Zero would mean it failed every check.")
}

// noulWord reads one Noul probability as a word, matching the Python bands.
func noulWord(value float64) string {
	switch {
	case value >= 0.9:
		return "yes"
	case value >= 0.7:
		return "probably yes"
	case value >= 0.3:
		return "unsettled"
	case value >= 0.1:
		return "probably no"
	default:
		return "no"
	}
}

// commas groups an integer with thousands separators.
func commas(n int) string {
	digits := fmt.Sprintf("%d", n)
	if len(digits) <= 3 {
		return digits
	}
	var out strings.Builder
	lead := len(digits) % 3
	if lead > 0 {
		out.WriteString(digits[:lead])
	}
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

// readAll drains a response body.
func readAll(response *http.Response) ([]byte, error) {
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("reading the response: %w", err)
	}
	return raw, nil
}

// trim shortens a body for an error message.
func trim(raw []byte) string {
	const limit = 200
	text := strings.TrimSpace(string(raw))
	if len(text) > limit {
		return text[:limit] + "..."
	}
	return text
}
