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

// Fill replaces {fact.key} placeholders with the facts' values.
func Fill(s string, facts map[string]string) string {
	for k, v := range facts {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}
