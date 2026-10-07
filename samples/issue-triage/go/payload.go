// Payload loading: the two stages, the state budgets, the model pin.
//
// The binary bakes questions.yml in, so it runs with no config file beside it
// and no per-repository setup at all. Point -questions at your own copy to
// change what it asks.
//
// The canonical questions.yml sits one directory up, where the other samples in
// this repository keep theirs. Go's embed directive reads files inside its own
// directory only, so the copy next to this file is exactly that: build.sh
// copies the canonical file in before every build, and payload_test.go fails
// when the two drift apart.
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
// compiler acts on comments beginning with go: and this one copies the bytes of
// questions.yml into the executable, handing them to payloadFS, an embed.FS
// that behaves like a tiny read-only filesystem in memory. It must sit directly
// above the variable it fills, with no blank line between.
//
//go:embed questions.yml
var payloadFS embed.FS

// payloadName is the path to read inside payloadFS, and the name build.sh and
// payload_test.go both use for the copied file.
const payloadName = "questions.yml"

// question is one entry from the payload.
//
// The strings in backticks are struct tags, telling the YAML library which key
// fills which field, so max_body_chars can land in a Go field named
// MaxBodyChars. Go exports a field whose name starts with a capital letter and
// the library reads exported fields only, hence the capitals. A field the file
// leaves out keeps its zero value.
type question struct {
	// Type picks how the binary asks and how it reads the answer back: noul for
	// a yes-or-no claim, choice for one option out of a named set, score for a
	// rubric level. loadPayload rejects anything else.
	Type string `yaml:"type"`
	// Label is the short name the report prints, falling back to the id.
	Label string `yaml:"label"`
	// Instructions is the question text sent to Jev.
	Instructions string `yaml:"instructions"`
	// Criteria holds the options a choice picks from or the levels a score
	// climbs. The shape differs per type, a map for choice and a list for score,
	// so the field takes any: Go's name for a value of any type at all. The
	// binary hands it to the API untouched.
	Criteria any `yaml:"criteria"`
}

// spec pairs a question with the id it sits under, so file order survives.
//
// Question order in questions.yml is print order, and a Go map hands its keys
// back in a random order every time it is walked. Carrying the id alongside the
// question keeps them in a slice, which does hold its order.
type spec struct {
	ID string
	Q  question
}

// decisionSpec is the single stage-two question, which carries an id of its own
// in the file rather than being a key in a mapping.
type decisionSpec struct {
	ID           string `yaml:"id"`
	Label        string `yaml:"label"`
	Type         string `yaml:"type"`
	Instructions string `yaml:"instructions"`
	Criteria     any    `yaml:"criteria"`
}

// settings holds everything in the payload except the questions.
type settings struct {
	// Model is the pinned model id. The SDKs default to jev-latest, which would
	// move answers under cuts calibrated on an older version.
	Model string `yaml:"model"`
	// MaxTitleChars, MaxBodyChars and MaxLabelsChars are one budget per state
	// field in stage one, so a long body cannot crowd out the labels.
	MaxTitleChars  int `yaml:"max_title_chars"`
	MaxBodyChars   int `yaml:"max_body_chars"`
	MaxLabelsChars int `yaml:"max_labels_chars"`
	// MaxDigestChars caps the fact table stage two reads.
	MaxDigestChars int `yaml:"max_digest_chars"`
	// InputUSDPerMillion is TypeSafe's published input-token price, which the
	// report uses to cost a run. Output tokens are counted and not charged.
	InputUSDPerMillion float64 `yaml:"input_usd_per_million"`
}

// wholePayload mirrors the file, keeping the questions as a node so the order of
// the keys survives decoding.
//
// The scalar fields decode straight into settings. Questions stops at a
// yaml.Node, the library's raw view of a piece of the document, which records
// the keys in the order the file wrote them. orderedQuestions walks that node.
type wholePayload struct {
	Model              string       `yaml:"model"`
	MaxTitleChars      int          `yaml:"max_title_chars"`
	MaxBodyChars       int          `yaml:"max_body_chars"`
	MaxLabelsChars     int          `yaml:"max_labels_chars"`
	MaxDigestChars     int          `yaml:"max_digest_chars"`
	InputUSDPerMillion float64      `yaml:"input_usd_per_million"`
	Questions          yaml.Node    `yaml:"questions"`
	Decision           decisionSpec `yaml:"decision"`
}

// payload is the whole file, loaded.
type payload struct {
	Set      settings
	Stage1   []spec
	Decision decisionSpec
}

// loadPayload reads the payload from path, or from the baked copy when path is
// empty.
//
// Go functions can return several values at once, and this one returns two. A
// caller checks the error first, and the value before it means nothing when the
// error is set.
func loadPayload(path string) (payload, error) {
	var raw []byte
	var err error
	// origin names the payload in every error below, so a reader can tell a
	// broken file they passed in from a broken baked-in copy.
	origin := "the baked-in " + payloadName

	if path == "" {
		raw, err = payloadFS.ReadFile(payloadName)
	} else {
		raw, err = os.ReadFile(path)
		origin = path
	}
	// The Go idiom: a function returns an error alongside its result and the
	// caller tests err != nil right away. %w wraps the original error inside the
	// new one, so the printed message keeps the operating system's reason.
	if err != nil {
		return payload{}, fmt.Errorf("reading %s: %w", origin, err)
	}

	var whole wholePayload
	if err := yaml.Unmarshal(raw, &whole); err != nil {
		return payload{}, fmt.Errorf("parsing %s: %w", origin, err)
	}

	out := payload{
		Set: settings{
			Model:              whole.Model,
			MaxTitleChars:      whole.MaxTitleChars,
			MaxBodyChars:       whole.MaxBodyChars,
			MaxLabelsChars:     whole.MaxLabelsChars,
			MaxDigestChars:     whole.MaxDigestChars,
			InputUSDPerMillion: whole.InputUSDPerMillion,
		},
		Decision: whole.Decision,
	}
	if err := checkSettings(out.Set, origin); err != nil {
		return out, err
	}
	if err := checkDecision(out.Decision, origin); err != nil {
		return out, err
	}

	out.Stage1, err = orderedQuestions(whole.Questions, origin)
	if err != nil {
		return out, err
	}
	return out, nil
}

// checkSettings catches a key the payload left out.
//
// A missing key leaves the zero value behind and each one fails silently in its
// own way: an empty model sends the request to jev-latest, a zero budget
// truncates that part of the state to nothing, and a zero price reports every
// run as free.
func checkSettings(set settings, origin string) error {
	switch {
	case set.Model == "":
		return fmt.Errorf("%s: missing model", origin)
	case set.MaxTitleChars <= 0:
		return fmt.Errorf("%s: missing max_title_chars", origin)
	case set.MaxBodyChars <= 0:
		return fmt.Errorf("%s: missing max_body_chars", origin)
	case set.MaxLabelsChars <= 0:
		return fmt.Errorf("%s: missing max_labels_chars", origin)
	case set.MaxDigestChars <= 0:
		return fmt.Errorf("%s: missing max_digest_chars", origin)
	case set.InputUSDPerMillion <= 0:
		return fmt.Errorf("%s: missing input_usd_per_million", origin)
	}
	return nil
}

// checkDecision rejects a stage-two question the rest of the program cannot use.
//
// It has to be a score, because the fraction Jev returns between levels is what
// orders the queue inside a bucket, and the level count has to match the
// horizons the report groups by.
func checkDecision(d decisionSpec, origin string) error {
	if d.ID == "" {
		return fmt.Errorf("%s: the decision question has no id", origin)
	}
	if d.Type != "score" {
		return fmt.Errorf(
			"%s: the decision question is %q, and has to be a score so its levels can order the queue",
			origin, d.Type,
		)
	}
	levels, ok := d.Criteria.([]any)
	if !ok {
		return fmt.Errorf("%s: the decision criteria is not a list of levels", origin)
	}
	if len(levels) != len(horizons) {
		return fmt.Errorf(
			"%s: the decision has %d levels and the report groups by %d horizons",
			origin, len(levels), len(horizons),
		)
	}
	return nil
}

// orderedQuestions walks the questions node and returns one spec per entry, in
// the order the file wrote them.
//
// A yaml.Node for a mapping keeps its children in one flat slice: key, value,
// key, value. The loop steps two at a time, which is why it reads Content at
// index and index+1.
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
		// back as something this binary has no branch for, so reject it here
		// where the message can name the question.
		switch q.Type {
		case "noul", "choice", "score":
		default:
			return nil, fmt.Errorf("%s: question %s has unknown type %q", origin, id, q.Type)
		}
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

// find returns the spec with an id, and whether it was there.
func find(specs []spec, id string) (spec, bool) {
	for _, s := range specs {
		if s.ID == id {
			return s, true
		}
	}
	return spec{}, false
}
