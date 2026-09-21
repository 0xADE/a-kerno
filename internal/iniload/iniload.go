// Package iniload wraps gopkg.in/ini.v1 with ADE defaults: case-sensitive
// keys and AllowShadows so repeated env keys keep every value.
package iniload

import (
	"fmt"

	"gopkg.in/ini.v1"
)

// Load parses INI bytes with AllowShadows enabled.
func Load(data []byte) (*ini.File, error) {
	f, err := ini.LoadSources(ini.LoadOptions{
		AllowShadows: true,
	}, data)
	if err != nil {
		return nil, fmt.Errorf("ini: %w", err)
	}
	return f, nil
}

// NamedSections returns all sections except DEFAULT.
func NamedSections(f *ini.File) []*ini.Section {
	var out []*ini.Section
	for _, sec := range f.Sections() {
		if sec.Name() == ini.DefaultSection {
			continue
		}
		out = append(out, sec)
	}
	return out
}

// ProgramSection returns DEFAULT when it has keys, otherwise the first named section.
func ProgramSection(f *ini.File) *ini.Section {
	def := f.Section(ini.DefaultSection)
	if len(def.Keys()) > 0 {
		return def
	}
	named := NamedSections(f)
	if len(named) > 0 {
		return named[0]
	}
	return def
}

// String returns the last value for key, or "" if the key is missing.
func String(sec *ini.Section, key string) string {
	if sec == nil || !sec.HasKey(key) {
		return ""
	}
	return sec.Key(key).String()
}

// Shadows returns every value for a repeated key (AllowShadows).
func Shadows(sec *ini.Section, key string) []string {
	if sec == nil || !sec.HasKey(key) {
		return nil
	}
	return sec.Key(key).ValueWithShadows()
}

// BoolDefault returns the boolean key value, or def when the key is missing or invalid.
func BoolDefault(sec *ini.Section, key string, def bool) bool {
	if sec == nil || !sec.HasKey(key) {
		return def
	}
	v, err := sec.Key(key).Bool()
	if err != nil {
		return def
	}
	return v
}
