// Package rules turns facts into a diagnosis. The rules and markers are data
// (diagnoses.yaml, markers.yaml), built into the exe.
package rules

import (
	"embed"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"whynet/internal/probe"
)

//go:embed diagnoses.yaml markers.yaml
var files embed.FS

type Rule struct {
	ID         string         `yaml:"id"`
	Confidence string         `yaml:"confidence"` // high | likely
	When       map[string]any `yaml:"when"`
}

type Verdict struct {
	ID         string `json:"id"`
	Confidence string `json:"confidence"`
}

// Load reads the built-in rules and markers.
func Load() ([]Rule, probe.Markers, error) {
	var rs []Rule
	var m probe.Markers
	for name, v := range map[string]any{"diagnoses.yaml": &rs, "markers.yaml": &m} {
		b, err := files.ReadFile(name)
		if err == nil {
			err = yaml.Unmarshal(b, v)
		}
		if err != nil {
			return nil, m, fmt.Errorf("%s: %w", name, err)
		}
	}
	for i, r := range rs {
		if r.ID == "" || r.Confidence != "high" && r.Confidence != "likely" {
			return nil, m, fmt.Errorf("diagnoses.yaml: rule %d: needs an id and confidence high|likely", i+1)
		}
	}
	return rs, m, nil
}

// Match returns the first rule whose conditions all hold.
func Match(rs []Rule, facts map[string]string) Verdict {
	for _, r := range rs {
		if matches(r.When, facts) {
			return Verdict{ID: r.ID, Confidence: r.Confidence}
		}
	}
	return Verdict{ID: "unknown", Confidence: "likely"}
}

func matches(when map[string]any, facts map[string]string) bool {
	for key, want := range when {
		got, ok := facts[key]
		switch w := want.(type) {
		case []any:
			if !ok || !slices.ContainsFunc(w, func(x any) bool { return fmt.Sprint(x) == got }) {
				return false
			}
		default:
			ws := fmt.Sprint(w)
			if not, neg := strings.CutPrefix(ws, "!"); neg {
				if ok && got == not {
					return false
				}
			} else if !ok || got != ws {
				return false
			}
		}
	}
	return true
}
