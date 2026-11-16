// Package rules turns facts into a diagnosis. The rules and markers are data
// (diagnoses.yaml, markers.yaml), built into the exe.
package rules

import (
	"compress/gzip"
	"embed"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"whynet/internal/probe"
)

//go:embed diagnoses.yaml live.yaml app.yaml markers.yaml ads.txt.gz
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
	var err error
	for name, v := range map[string]any{"diagnoses.yaml": &rs, "markers.yaml": &m} {
		b, err := files.ReadFile(name)
		if err == nil {
			err = yaml.Unmarshal(b, v)
		}
		if err != nil {
			return nil, m, fmt.Errorf("%s: %w", name, err)
		}
	}
	if err := check("diagnoses.yaml", rs); err != nil {
		return nil, m, err
	}
	m.AdSet, err = adSet()
	if err != nil {
		return nil, m, fmt.Errorf("ads.txt.gz: %w", err)
	}
	return rs, m, nil
}

// LoadLive reads the stability check's rules (live.yaml): kept apart so the site rules never
// fire on a stability check's facts, nor the other way round.
func LoadLive() ([]Rule, error) { return loadRules("live.yaml") }

// LoadApp reads the app check's rules (app.yaml), apart from the site's for the same reason.
func LoadApp() ([]Rule, error) { return loadRules("app.yaml") }

func loadRules(name string) ([]Rule, error) {
	var rs []Rule
	b, err := files.ReadFile(name)
	if err == nil {
		err = yaml.Unmarshal(b, &rs)
	}
	if err == nil {
		err = check(name, rs)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return rs, nil
}

func check(name string, rs []Rule) error {
	for i, r := range rs {
		if r.ID == "" || r.Confidence != "high" && r.Confidence != "likely" {
			return fmt.Errorf("%s: rule %d: needs an id and confidence high|likely", name, i+1)
		}
	}
	return nil
}

// adSet reads the built-in ad and tracker domains once (StevenBlack/hosts, see ads-LICENSE.txt).
var adSet = sync.OnceValues(func() (map[string]bool, error) {
	f, err := files.Open("ads.txt.gz")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, 80000)
	for _, d := range strings.Split(string(b), "\n") {
		if d != "" {
			set[d] = true
		}
	}
	return set, nil
})

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
