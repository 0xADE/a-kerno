// Package mdparse adapts github.com/konkero-project/mdconfig DynamicParse output
// to ADE Markdown config files that use ## sections, checklists, and
// "- key: value" property lists.
package mdparse

import (
	"fmt"
	"strings"

	"github.com/konkero-project/mdconfig"
)

// Section holds parsed content for one Markdown H2 section.
type Section struct {
	// Enabled maps daemon/program names to checked state from the first checklist.
	Enabled map[string]bool
	// Properties maps keys to values from "- key: value" list items.
	Properties map[string]string
	// Lines preserves raw list item text for callers that need the original lines.
	Lines []string
}

// Parse decodes a Markdown configuration document into sections keyed by heading title.
func Parse(data []byte) (map[string]*Section, error) {
	cfg, err := mdconfig.DynamicParse(data)
	if err != nil {
		return nil, fmt.Errorf("mdconfig parse: %w", err)
	}

	sections := make(map[string]*Section)
	for title, sec := range cfg.Sections {
		if sec == nil {
			continue
		}
		sections[title] = fromMDSection(sec)
	}
	return sections, nil
}

func fromMDSection(sec *mdconfig.Section) *Section {
	out := &Section{
		Properties: make(map[string]string),
	}

	if len(sec.BoolLists) > 0 {
		out.Enabled = cloneBoolMap(sec.BoolLists[0])
	}

	for _, list := range sec.Lists {
		for _, item := range list {
			out.Lines = append(out.Lines, item)
			key, value, ok := splitKeyValue(item)
			if ok {
				out.Properties[key] = value
			}
		}
	}

	return out
}

// ParseRootLists parses a flat Markdown file (no sections) into key-value properties.
func ParseRootLists(data []byte) (map[string]string, []string, error) {
	cfg, err := mdconfig.DynamicParse(data)
	if err != nil {
		return nil, nil, fmt.Errorf("mdconfig parse: %w", err)
	}

	props := make(map[string]string)
	var lines []string

	for _, list := range cfg.RootSection.Lists {
		for _, item := range list {
			lines = append(lines, item)
			key, value, ok := splitKeyValue(item)
			if ok {
				props[key] = value
			}
		}
	}

	return props, lines, nil
}

func splitKeyValue(item string) (key, value string, ok bool) {
	item = strings.TrimSpace(item)
	if item == "" {
		return "", "", false
	}

	key, value, found := strings.Cut(item, ": ")
	if !found {
		key, value, found = strings.Cut(item, ":")
		if !found {
			return "", "", false
		}
	}

	key = strings.TrimSpace(key)
	value = strings.TrimSpace(value)
	if key == "" {
		return "", "", false
	}
	return key, value, true
}

func cloneBoolMap(src map[string]bool) map[string]bool {
	if src == nil {
		return nil
	}
	dst := make(map[string]bool, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
