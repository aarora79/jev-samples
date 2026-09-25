// Payload loading: the questions, the weights, the model pin, the budgets and
// the price.
//
// The binary bakes in questions.yml, so it runs with no config file beside it.
// Point -questions at your own copy to triage against different questions or
// weights, and the baked copy stays as the default.
//
// The Python sample one directory up owns the canonical payload, at
// ../questions.yml. Go's embed directive reads files inside its own directory
// only, so it cannot reach up a level, and the questions.yml sitting beside this
// file is a copy. build.sh copies the canonical file in before every build, and
// payload_test.go fails when the two drift apart.
package main

import (
	"embed"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// payloadFS carries questions.yml into the binary.
//
// The line below is a compiler directive, not a comment for a reader. The Go
// compiler acts on comments that begin with go: and this one copies the bytes of
// questions.yml into the executable, handing them to payloadFS, an embed.FS that
// behaves like a tiny read-only filesystem in memory. The directive must sit
// directly above the variable it fills, with no blank line between.
//
// That is what makes this sample one file to ship: the questions travel inside
// the binary, so a CI runner needs no checkout of this repo.
//
//go:embed questions.yml
var payloadFS embed.FS

// payloadName is the path to read inside payloadFS, and the name build.sh and
// payload_test.go both use for the copied file.
const payloadName = "questions.yml"

// question is one entry from the questions map in the payload.
//
// The strings in backticks after each field are struct tags, telling the YAML
// library which key fills which field, so max_description_chars can land in a Go
// field named MaxDescriptionChars. Go exports a field whose name starts with a
// capital letter, and the library reads exported fields only, hence the capitals.
//
// A field the file leaves out keeps its zero value: "" for a string, 0 for a
// number, false for a bool, nil for a map.
type question struct {
	// Type picks how the binary asks and how it scores the answer: noul for a
	// yes-or-no claim, choice for one option out of a named set, score for a
	// rubric level. loadPayload rejects anything else.
	Type string `yaml:"type"`
	// Label is the short name the reports print. orderedQuestions falls back to
	// the question id when the file gives no label.
	Label string `yaml:"label"`
	// Weight is this question's share of the review load. A question with no
	// weight still gets asked and printed, and contributes nothing. change_kind
	// and review_focus are the two that carry none.
	Weight float64 `yaml:"weight"`
	// Invert flips the meaning of a yes. mechanical, has_tests and
	// description_quality set it, because a repeated diff, a tested change and a
	// thorough description each lower the load rather than raising it.
	Invert bool `yaml:"invert"`
	// Instructions is the question text the binary sends to Jev.
	Instructions string `yaml:"instructions"`
	// Criteria holds the options a choice question picks from, or the rubric
	// levels a score question climbs. The shape differs per type, a map for choice
	// and a list for score, so the field takes any: Go's name for a value of any
	// type at all. The binary hands it straight to the API.
	Criteria any `yaml:"criteria"`
	// Credit says what each choice option is worth toward the load, keyed by
	// option name. A missing option reads as 0, a Go map's zero value for a
	// float64. Only choice questions use it, and only when they carry a weight.
	Credit map[string]float64 `yaml:"credit"`
}

// settings holds everything in the payload except the questions.
//
// This sample sends a state with four named fields rather than one blob, so it
// carries one budget per field that can run long. A pull request description, a
// file list and a diff each grow without limit, and one budget shared between
// them would let a long diff crowd out the description.
type settings struct {
	// Model is the pinned model id, jev-1.13.0. Both SDKs default to jev-latest,
	// which would move answers under thresholds calibrated on an older version.
	Model string `yaml:"model"`
	// MaxDescriptionChars caps the author's description.
	MaxDescriptionChars int `yaml:"max_description_chars"`
	// MaxFileListChars caps the list of changed paths. Past the budget the list
	// keeps whole paths and reports how many it dropped.
	MaxFileListChars int `yaml:"max_file_list_chars"`
	// MaxDiffChars caps the patch text. Past the budget the smallest patches go
	// in whole first, because triage asks how far a change reaches and whether one
	// edit repeats, and both of those want breadth over depth.
	MaxDiffChars int `yaml:"max_diff_chars"`
	// InputUSDPerMillion is TypeSafe's published input-token price, which the
	// reports use to cost a run. Output tokens are counted and not charged.
	InputUSDPerMillion float64 `yaml:"input_usd_per_million"`
}

// spec pairs a question with its id, so the order in the file survives.
//
// Question order in questions.yml is print order, and a Go map hands its keys
// back in a random order every time it is walked. Carrying the id alongside the
// question keeps them in a slice, which does hold its order.
type spec struct {
	// ID is the key from the file, such as blast_radius.
	ID string
	// Q is the question itself, embedded by value so a spec is self-contained.
	Q question
}

// wholePayload mirrors the file, keeping the questions as a node so the order of
// the keys survives decoding.
//
// The scalar fields decode straight into settings. Questions stops at a
// yaml.Node, the library's raw view of a piece of the document, which records
// the keys in the order the file wrote them. orderedQuestions walks that node.
type wholePayload struct {
	Model               string    `yaml:"model"`
	MaxDescriptionChars int       `yaml:"max_description_chars"`
	MaxFileListChars    int       `yaml:"max_file_list_chars"`
	MaxDiffChars        int       `yaml:"max_diff_chars"`
	InputUSDPerMillion  float64   `yaml:"input_usd_per_million"`
	Questions           yaml.Node `yaml:"questions"`
}

// loadPayload reads the payload from path, or from the baked copy when path is
// empty. It returns the settings and the questions in file order.
//
// Go functions can return several values at once, and this one returns three: the
// settings, the questions, and an error. A caller checks the error first, and the
// two values before it mean nothing when the error is set.
func loadPayload(path string) (settings, []spec, error) {
	var raw []byte
	var err error
	// origin names the payload in every error message below, so a reader can tell
	// a broken file they passed in from a broken baked-in copy.
	origin := "the baked-in " + payloadName

	if path == "" {
		raw, err = payloadFS.ReadFile(payloadName)
	} else {
		raw, err = os.ReadFile(path)
		origin = path
	}
	// The Go idiom: a function returns an error alongside its result, and the
	// caller tests err != nil right away. %w wraps the original error inside the
	// new one, so the printed message keeps the operating system's reason and a
	// caller further up can still inspect the cause.
	if err != nil {
		return settings{}, nil, fmt.Errorf("reading %s: %w", origin, err)
	}

	// Unmarshal fills a struct from YAML bytes. &whole passes the address of the
	// struct, a pointer, because the library writes into it rather than handing
	// back a copy. Without the &, Unmarshal would fill a throwaway.
	var whole wholePayload
	if err := yaml.Unmarshal(raw, &whole); err != nil {
		return settings{}, nil, fmt.Errorf("parsing %s: %w", origin, err)
	}

	set := settings{
		Model:               whole.Model,
		MaxDescriptionChars: whole.MaxDescriptionChars,
		MaxFileListChars:    whole.MaxFileListChars,
		MaxDiffChars:        whole.MaxDiffChars,
		InputUSDPerMillion:  whole.InputUSDPerMillion,
	}
	// A switch with no value after it runs the first case whose condition holds,
	// which reads better than five stacked ifs. Each check catches a key the file
	// left out, since a missing key leaves the zero value behind: an empty model
	// would send the request to jev-latest, a zero budget would truncate that part
	// of the state to nothing, and a zero price would report every run as free.
	switch {
	case set.Model == "":
		return set, nil, fmt.Errorf("%s: missing model", origin)
	case set.MaxDescriptionChars <= 0:
		return set, nil, fmt.Errorf("%s: missing max_description_chars", origin)
	case set.MaxFileListChars <= 0:
		return set, nil, fmt.Errorf("%s: missing max_file_list_chars", origin)
	case set.MaxDiffChars <= 0:
		return set, nil, fmt.Errorf("%s: missing max_diff_chars", origin)
	case set.InputUSDPerMillion <= 0:
		return set, nil, fmt.Errorf("%s: missing input_usd_per_million", origin)
	}

	specs, err := orderedQuestions(whole.Questions, origin)
	if err != nil {
		return set, nil, err
	}
	return set, specs, nil
}

// orderedQuestions walks the questions node and returns one spec per entry, in
// the order the file wrote them.
//
// A yaml.Node for a mapping keeps its children in one flat slice: key, value,
// key, value. The loop below steps two at a time, which is why it reads Content
// at index and index+1.
func orderedQuestions(node yaml.Node, origin string) ([]spec, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: questions is not a mapping", origin)
	}

	specs := make([]spec, 0, len(node.Content)/2)
	for index := 0; index+1 < len(node.Content); index += 2 {
		id := node.Content[index].Value

		var q question
		if err := node.Content[index+1].Decode(&q); err != nil {
			return nil, fmt.Errorf("%s: question %s: %w", origin, id, err)
		}
		// A type the scorer cannot read would sail through to the API and come
		// back as something this binary has no branch for, so reject it here where
		// the message can name the question.
		switch q.Type {
		case "noul", "choice", "score":
		default:
			return nil, fmt.Errorf("%s: question %s has unknown type %q", origin, id, q.Type)
		}
		// The reports print the label, so fall back to the id rather than a blank
		// column.
		if q.Label == "" {
			q.Label = id
		}
		specs = append(specs, spec{ID: id, Q: q})
	}

	if len(specs) == 0 {
		return nil, fmt.Errorf("%s: no questions", origin)
	}
	return specs, nil
}

// weightedTotal adds up every weight in the payload.
//
// The load divides by this rather than by a constant 1.0, so editing one weight
// needs no rebalancing of the others.
func weightedTotal(specs []spec) float64 {
	total := 0.0
	for _, s := range specs {
		total += s.Q.Weight
	}
	return total
}
