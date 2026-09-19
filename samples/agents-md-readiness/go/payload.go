// Payload loading: the questions, weights, model pin and price.
//
// The binary bakes in questions.yml, so it runs with no config file. Point
// -questions at your own copy to score against different questions or weights,
// and the baked copy stays as the default.
package main

import (
	"embed"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// payloadFS carries questions.yml into the binary.
//
//go:embed questions.yml
var payloadFS embed.FS

const payloadName = "questions.yml"

// question is one entry from the questions map.
type question struct {
	Type         string             `yaml:"type"`
	Label        string             `yaml:"label"`
	Weight       float64            `yaml:"weight"`
	Invert       bool               `yaml:"invert"`
	Instructions string             `yaml:"instructions"`
	Criteria     any                `yaml:"criteria"`
	Credit       map[string]float64 `yaml:"credit"`
}

// settings holds everything in the payload except the questions.
type settings struct {
	Model              string  `yaml:"model"`
	MaxStateChars      int     `yaml:"max_state_chars"`
	InputUSDPerMillion float64 `yaml:"input_usd_per_million"`
}

// spec pairs a question with its id, so the order in the file survives.
type spec struct {
	ID string
	Q  question
}

// wholePayload mirrors the file, keeping the questions as a node so the order
// of the keys survives decoding.
type wholePayload struct {
	Model              string    `yaml:"model"`
	MaxStateChars      int       `yaml:"max_state_chars"`
	InputUSDPerMillion float64   `yaml:"input_usd_per_million"`
	Questions          yaml.Node `yaml:"questions"`
}

// loadPayload reads the payload from path, or from the baked copy when path is
// empty. It returns the settings and the questions in file order.
func loadPayload(path string) (settings, []spec, error) {
	var raw []byte
	var err error
	origin := "the baked-in " + payloadName

	if path == "" {
		raw, err = payloadFS.ReadFile(payloadName)
	} else {
		raw, err = os.ReadFile(path)
		origin = path
	}
	if err != nil {
		return settings{}, nil, fmt.Errorf("reading %s: %w", origin, err)
	}

	var whole wholePayload
	if err := yaml.Unmarshal(raw, &whole); err != nil {
		return settings{}, nil, fmt.Errorf("parsing %s: %w", origin, err)
	}

	set := settings{
		Model:              whole.Model,
		MaxStateChars:      whole.MaxStateChars,
		InputUSDPerMillion: whole.InputUSDPerMillion,
	}
	switch {
	case set.Model == "":
		return set, nil, fmt.Errorf("%s: missing model", origin)
	case set.MaxStateChars <= 0:
		return set, nil, fmt.Errorf("%s: missing max_state_chars", origin)
	case set.InputUSDPerMillion <= 0:
		return set, nil, fmt.Errorf("%s: missing input_usd_per_million", origin)
	}

	specs, err := orderedQuestions(whole.Questions, origin)
	if err != nil {
		return set, nil, err
	}
	if len(specs) == 0 {
		return set, nil, fmt.Errorf("%s: no questions", origin)
	}
	return set, specs, nil
}

// orderedQuestions walks the mapping node so the questions keep the order the
// file wrote them in, which is the order the sample prints them.
func orderedQuestions(node yaml.Node, origin string) ([]spec, error) {
	if node.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: questions is not a mapping", origin)
	}

	specs := make([]spec, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		id := node.Content[i].Value
		var q question
		if err := node.Content[i+1].Decode(&q); err != nil {
			return nil, fmt.Errorf("%s: question %s: %w", origin, id, err)
		}
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
	return specs, nil
}

// weighted returns the specs that carry a weight, in file order.
func weighted(specs []spec) []spec {
	out := make([]spec, 0, len(specs))
	for _, s := range specs {
		if s.Q.Weight > 0 {
			out = append(out, s)
		}
	}
	return out
}
