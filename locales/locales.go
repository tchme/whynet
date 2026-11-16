// Package locales holds every user-facing string, one YAML file per language.
package locales

import (
	"embed"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed *.yaml
var files embed.FS

type Diagnosis struct {
	Title   string `yaml:"title"`
	Explain string `yaml:"explain"`
	Advice  string `yaml:"advice"`
}

type StepText struct {
	Name string `yaml:"name"`
	What string `yaml:"what"`
}

type Texts struct {
	Diagnoses map[string]Diagnosis `yaml:"diagnoses"`
	// The stability check's verdicts and words (window, report), apart from the site check's.
	LiveDiagnoses map[string]Diagnosis `yaml:"live_diagnoses"`
	Live          map[string]string    `yaml:"live"`
	// The app check's verdicts and words.
	AppDiagnoses map[string]Diagnosis `yaml:"app_diagnoses"`
	App          map[string]string    `yaml:"app"`
	Steps        map[string]StepText  `yaml:"steps"`
	Status       map[string]string    `yaml:"status"`
	Confidence   map[string]string    `yaml:"confidence"`
	Report       map[string]string    `yaml:"report"`
	Values       map[string]string    `yaml:"values"`
	Notes        []Note               `yaml:"notes"`
	Kinds        map[string]string    `yaml:"kinds"`
	Results      map[string]string    `yaml:"results"`
	Phases       map[string]string    `yaml:"phases"`    // where a slow file's time went
	Countries    map[string]string    `yaml:"countries"` // ISO code -> name as in "из <name>"
	Colos        map[string]string    `yaml:"colos"`     // Cloudflare node (airport code) -> city
	UI           map[string]string    `yaml:"ui"`
}

// Load reads one language, e.g. "ru".
func Load(lang string) (*Texts, error) {
	b, err := files.ReadFile(lang + ".yaml")
	if err != nil {
		return nil, err
	}
	t := &Texts{}
	return t, yaml.Unmarshal(b, t)
}

// Note is a one-line explanation shown under a step when one of its facts has a given value.
type Note struct {
	Fact string `yaml:"fact"` // "key=value"
	Text string `yaml:"text"`
	Info bool   `yaml:"info"` // shown under a healthy step too: something worth knowing, not a problem
}

// Note joins, in file order, the notes whose fact a step has. Without problems, only info notes.
func (t *Texts) Note(facts map[string]string, problems bool) string {
	named := t.Named(facts)
	var out []string
	for _, n := range t.Notes {
		if k, v, _ := strings.Cut(n.Fact, "="); facts[k] == v && (problems || n.Info) {
			out = append(out, Fill(n.Text, named))
		}
	}
	return strings.Join(out, " ")
}

// Named swaps country codes (keys ending in _country) and Cloudflare node codes (_colo)
// in facts for their names in this language; unknown codes stay as they are.
func (t *Texts) Named(facts map[string]string) map[string]string {
	out := make(map[string]string, len(facts))
	for k, v := range facts {
		switch {
		case strings.HasSuffix(k, "_country") && t.Countries[v] != "":
			v = t.Countries[v]
		case strings.HasSuffix(k, "_colo") && t.Colos[v] != "":
			v = t.Colos[v] + " (" + v + ")"
		}
		out[k] = v
	}
	return out
}

// Plain returns facts with values translated through t.Values where a translation exists.
func (t *Texts) Plain(facts map[string]string) map[string]string {
	out := make(map[string]string, len(facts))
	for k, v := range facts {
		if p, ok := t.Values[v]; ok {
			v = p
		}
		out[k] = v
	}
	return out
}

// Fill replaces {fact.key} placeholders with the facts' values.
func Fill(s string, facts map[string]string) string {
	for k, v := range facts {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}
