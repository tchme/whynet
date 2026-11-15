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
	Diagnoses  map[string]Diagnosis `yaml:"diagnoses"`
	Steps      map[string]StepText  `yaml:"steps"`
	Status     map[string]string    `yaml:"status"`
	Confidence map[string]string    `yaml:"confidence"`
	Report     map[string]string    `yaml:"report"`
	Values     map[string]string    `yaml:"values"`
	Notes      []Note               `yaml:"notes"`
	UI         map[string]string    `yaml:"ui"`
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
}

// Note joins, in file order, the notes whose fact a step has.
func (t *Texts) Note(facts map[string]string) string {
	var out []string
	for _, n := range t.Notes {
		if k, v, _ := strings.Cut(n.Fact, "="); facts[k] == v {
			out = append(out, Fill(n.Text, facts))
		}
	}
	return strings.Join(out, " ")
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
