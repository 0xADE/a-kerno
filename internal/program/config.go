// Package program provides user program configuration loading and management
// for a-kerno. It handles .md (ADE autostart) and .desktop (XDG autostart)
// configuration files, populating ProgramConfig structures with variable
// expansion and priority resolution.
package program

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ProgramConfig represents the parsed configuration of a single user program.
type ProgramConfig struct {
	Name          string            // file name without extension
	Exec          string            `md:"exec"`
	Phase         string            `md:"phase"`          // "early" or "post" (default "post")
	Priority      int               `md:"priority"`       // within phase
	Enabled       bool              `md:"enabled"`        // default true
	DependsOn     []string          `md:"depends_on"`     // list of program names
	StartDelay    int               `md:"start_delay"`    // milliseconds
	HealthCheck   string            `md:"health_check"`   // command for health check
	HealthTimeout int               `md:"health_timeout"` // seconds (default: 30)
	HealthRetry   int               `md:"health_retry"`   // attempts (default: 3)
	Env           map[string]string `md:"env"`
	Restart       bool              `md:"restart"` // restart on failure
	Source        string            // "markdown" or "desktop"
}

// Default values for program config fields.
const (
	DefaultPhase         = "post"
	DefaultHealthTimeout = 30
	DefaultHealthRetry   = 3
	DefaultStartDelay    = 0
)

// LoadProgramConfigs scans the ADE autostart directory (~/.config/ade/autostart/)
// and the XDG autostart directory (~/.config/autostart/) for program definitions.
// .md files take precedence over .desktop files with the same base name.
// uid and home are used for variable expansion.
func LoadProgramConfigs(autostartDir, xdgAutostartDir, uid, home string) ([]ProgramConfig, error) {
	var allConfigs []ProgramConfig
	seen := make(map[string]bool) // tracks names that already have a definition

	// 1. Parse .md files from ADE autostart directory (highest priority).
	mdConfigs, err := scanDir(autostartDir, ".md", uid, home, "markdown", seen)
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", autostartDir, err)
	}
	allConfigs = append(allConfigs, mdConfigs...)

	// 2. Parse .desktop files from XDG autostart directory (fallback).
	if xdgAutostartDir != "" {
		desktopConfigs, err := scanDir(xdgAutostartDir, ".desktop", uid, home, "desktop", seen)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("scan %s: %w", xdgAutostartDir, err)
		}
		allConfigs = append(allConfigs, desktopConfigs...)
	}

	// Sort: early phase first, then by priority ascending, then by name.
	sort.Slice(allConfigs, func(i, j int) bool {
		ai, aj := allConfigs[i], allConfigs[j]
		if ai.Phase != aj.Phase {
			// "early" comes before "post"
			if ai.Phase == "early" {
				return true
			}
			if aj.Phase == "early" {
				return false
			}
		}
		if ai.Priority != aj.Priority {
			return ai.Priority < aj.Priority
		}
		return ai.Name < aj.Name
	})

	return allConfigs, nil
}

// scanDir scans a directory for files with the given extension and parses them.
// seen tracks names that have already been defined (higher priority wins).
func scanDir(dir, ext, uid, home, source string, seen map[string]bool) ([]ProgramConfig, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var configs []ProgramConfig
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ext {
			continue
		}

		baseName := strings.TrimSuffix(name, ext)
		if seen[baseName] {
			continue // already defined by a higher-priority source
		}
		seen[baseName] = true

		fullPath := filepath.Join(dir, name)

		var cfg ProgramConfig
		switch source {
		case "markdown":
			cfg, err = parseMarkdownProgram(fullPath, baseName, uid, home)
		case "desktop":
			cfg, err = parseDesktopProgram(fullPath, baseName, uid, home)
		default:
			err = fmt.Errorf("unknown source: %s", source)
		}

		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", fullPath, err)
		}
		configs = append(configs, cfg)
	}

	return configs, nil
}

// parseMarkdownProgram parses an ADE autostart .md file.
// Format is similar to daemons.md sections: "## <name> properties"
// with "- key: value" body lines.
func parseMarkdownProgram(path, name, uid, home string) (ProgramConfig, error) {
	cfg := ProgramConfig{
		Name:          name,
		Phase:         DefaultPhase,
		Enabled:       true,
		Source:        "markdown",
		HealthTimeout: DefaultHealthTimeout,
		HealthRetry:   DefaultHealthRetry,
		Env:           make(map[string]string),
	}

	file, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Expect "- key: value"
		rest, ok := strings.CutPrefix(line, "- ")
		if !ok {
			continue
		}

		key, value, found := strings.Cut(rest, ": ")
		if !found {
			key, value, found = strings.Cut(rest, ":")
			if !found {
				continue
			}
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		// Expand variables.
		value = expandVar(value, uid, home)

		switch key {
		case "exec":
			cfg.Exec = value
		case "phase":
			phase := strings.ToLower(value)
			if phase != "early" && phase != "post" {
				phase = DefaultPhase
			}
			cfg.Phase = phase
		case "priority":
			if n, err := strconv.Atoi(value); err == nil {
				cfg.Priority = n
			}
		case "enabled":
			switch strings.ToLower(value) {
			case "false", "no", "0":
				cfg.Enabled = false
			default:
				cfg.Enabled = true
			}
		case "depends_on":
			cfg.DependsOn = parseCommaList(value)
		case "start_delay":
			if n, err := strconv.Atoi(value); err == nil {
				cfg.StartDelay = n
			}
		case "health_check":
			cfg.HealthCheck = value
		case "health_timeout":
			if n, err := strconv.Atoi(value); err == nil {
				cfg.HealthTimeout = n
			}
		case "health_retry":
			if n, err := strconv.Atoi(value); err == nil {
				cfg.HealthRetry = n
			}
		case "env":
			k, v, found := strings.Cut(value, "=")
			if found {
				cfg.Env[k] = v
			}
		case "restart":
			switch strings.ToLower(value) {
			case "true", "yes", "1":
				cfg.Restart = true
			default:
				cfg.Restart = false
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return cfg, err
	}

	if cfg.Exec == "" {
		return cfg, fmt.Errorf("exec is required for program %q", name)
	}

	return cfg, nil
}

// parseDesktopProgram parses an XDG .desktop file.
// Extracts Exec=, Hidden=, OnlyShowIn=, NotShowIn= from [Desktop Entry].
func parseDesktopProgram(path, name, uid, home string) (ProgramConfig, error) {
	cfg := ProgramConfig{
		Name:          name,
		Phase:         DefaultPhase,
		Enabled:       true,
		Source:        "desktop",
		HealthTimeout: DefaultHealthTimeout,
		HealthRetry:   DefaultHealthRetry,
		Env:           make(map[string]string),
	}

	file, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer file.Close()

	inDesktopEntry := false
	hidden := false

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Section header.
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inDesktopEntry = strings.EqualFold(line, "[Desktop Entry]")
			continue
		}

		if !inDesktopEntry {
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		switch strings.ToLower(key) {
		case "exec":
			// Strip %f, %F, %u, %U, %k, %c, %i, %d, %D, %n, %N, %v, %m placeholders.
			value = stripDesktopExecPlaceholders(value)
			value = expandVar(value, uid, home)
			cfg.Exec = value
		case "hidden":
			if strings.EqualFold(value, "true") || value == "1" {
				hidden = true
			}
		case "onlyshowin":
			// If OnlyShowIn is set and does not include "ADE", disable the program.
			if value != "" && !containsADE(value) {
				cfg.Enabled = false
			}
		case "notshowin":
			// If NotShowIn contains "ADE", disable the program.
			if containsADE(value) {
				cfg.Enabled = false
			}
		case "x-ade-phase":
			phase := strings.ToLower(value)
			if phase == "early" || phase == "post" {
				cfg.Phase = phase
			}
		case "x-ade-priority":
			if n, err := strconv.Atoi(value); err == nil {
				cfg.Priority = n
			}
		case "x-ade-depends-on":
			cfg.DependsOn = parseCommaList(value)
		case "x-ade-health-check":
			cfg.HealthCheck = value
		case "x-ade-start-delay":
			if n, err := strconv.Atoi(value); err == nil {
				cfg.StartDelay = n
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return cfg, err
	}

	if hidden {
		cfg.Enabled = false
	}

	if cfg.Exec == "" {
		return cfg, fmt.Errorf("exec not found in desktop entry %q", name)
	}

	return cfg, nil
}

// stripDesktopExecPlaceholders removes field codes like %f, %F, %u, %U, %k, etc.
// from an XDG desktop Exec key value.
func stripDesktopExecPlaceholders(exec string) string {
	// Remove all %X single-letter codes (common in .desktop files).
	// Also handle %% (literal percent).
	result := strings.Builder{}
	skipNext := false
	for i, r := range exec {
		if skipNext {
			skipNext = false
			// If the next char is not a letter, keep the %.
			if i > 0 && exec[i-1] == '%' {
				if !isAlpha(byte(r)) && r != '%' {
					result.WriteByte('%')
					result.WriteRune(r)
				}
			}
			continue
		}
		if r == '%' {
			if i+1 < len(exec) {
				if exec[i+1] == '%' {
					result.WriteByte('%')
					skipNext = true
					continue
				}
				if isAlpha(exec[i+1]) {
					skipNext = true
					continue
				}
			}
		}
		result.WriteRune(r)
	}
	return result.String()
}

func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// containsADE checks whether a semicolon-delimited list contains "ADE".
func containsADE(value string) bool {
	for _, part := range strings.Split(value, ";") {
		if strings.EqualFold(strings.TrimSpace(part), "ADE") {
			return true
		}
	}
	return false
}

// parseCommaList splits a comma-delimited string into a trimmed string slice.
func parseCommaList(s string) []string {
	var result []string
	for _, part := range strings.Split(s, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// expandVar substitutes ${UID}, ${HOME}, ${XDG_RUNTIME_DIR} in the string.
func expandVar(s, uid, home string) string {
	s = strings.ReplaceAll(s, "${UID}", uid)
	s = strings.ReplaceAll(s, "${HOME}", home)

	xdgRuntime := os.Getenv("XDG_RUNTIME_DIR")
	if xdgRuntime == "" {
		xdgRuntime = fmt.Sprintf("/run/user/%s", uid)
	}
	s = strings.ReplaceAll(s, "${XDG_RUNTIME_DIR}", xdgRuntime)

	return s
}
