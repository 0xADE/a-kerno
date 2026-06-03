package daemon

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RestartPolicy defines the daemon restart behavior.
type RestartPolicy string

const (
	RestartAlways    RestartPolicy = "always"
	RestartOnFailure RestartPolicy = "on-failure"
	RestartOnce      RestartPolicy = "once"
	RestartDisabled  RestartPolicy = "disabled"
)

// validRestartPolicies is the set of allowed restart policy values.
var validRestartPolicies = map[RestartPolicy]bool{
	RestartAlways:    true,
	RestartOnFailure: true,
	RestartOnce:      true,
	RestartDisabled:  true,
}

// DaemonConfig represents the configuration of a single daemon
// as parsed from a "## <name> properties" section in daemons.md.
type DaemonConfig struct {
	// Name is the daemon identifier (extracted from section heading).
	Name string

	// Exec is the path to the daemon executable (required).
	Exec string

	// Order determines the startup order; lower values start first.
	// Daemons with equal Order may start in parallel.
	Order int

	// Restart is the restart policy: always, on-failure, once, disabled.
	Restart RestartPolicy

	// ReadyTimeout is the maximum time to wait for the daemon to become ready.
	ReadyTimeout time.Duration

	// Socket is an optional Unix socket path used for readiness detection.
	Socket string

	// Env holds additional environment variables for the daemon process.
	Env map[string]string

	// Enabled indicates whether the daemon is enabled in the task list.
	Enabled bool
}

// Default values for DaemonConfig fields.
const (
	DefaultRestart      = RestartOnFailure
	DefaultReadyTimeout = 10 * time.Second
)

// LoadConfig reads and parses the daemons.md file at the given path.
// It returns a slice of DaemonConfig sorted by Order.
func LoadConfig(path string, uid, home string) ([]DaemonConfig, error) {
	//nolint:gosec // path originates from trusted config directory
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	// Parse the Markdown file into sections.
	sections, err := parseSections(file)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	// Extract enabled set from the "enabled daemons" task list.
	enabled := parseEnabledList(sections["enabled daemons"])

	// Parse each "## <name> properties" section into a DaemonConfig.
	var configs []DaemonConfig
	for heading, lines := range sections {
		name, ok := strings.CutSuffix(heading, " properties")
		if !ok || name == "" {
			continue
		}

		cfg, err := parseDaemonSection(name, lines, uid, home)
		if err != nil {
			return nil, fmt.Errorf("section %q: %w", heading, err)
		}

		// Apply enabled status from task list.
		// If the daemon is not listed in "enabled daemons", it is
		// considered disabled by default.
		if en, exists := enabled[name]; exists {
			cfg.Enabled = en
		}

		configs = append(configs, cfg)
	}

	if len(configs) == 0 {
		return nil, fmt.Errorf("no daemon properties sections found in %s", path)
	}

	// Sort by Order ascending.
	sort.Slice(configs, func(i, j int) bool {
		return configs[i].Order < configs[j].Order
	})

	return configs, nil
}

// parseSections reads a Markdown file and returns a map from
// H2 heading text (e.g. "a-lancxo properties") to the list of
// non-empty, non-comment body lines belonging to that section.
func parseSections(file *os.File) (map[string][]string, error) {
	sections := make(map[string][]string)
	var currentHeading string

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Skip empty lines and comments.
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// H2 heading: "## heading text"
		if strings.HasPrefix(trimmed, "## ") {
			currentHeading = strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			if _, exists := sections[currentHeading]; !exists {
				sections[currentHeading] = []string{}
			}
			continue
		}

		// H1 heading resets current section.
		if strings.HasPrefix(trimmed, "# ") && !strings.HasPrefix(trimmed, "## ") {
			currentHeading = ""
			continue
		}

		// Accumulate body lines under the current H2 section.
		if currentHeading != "" {
			sections[currentHeading] = append(sections[currentHeading], trimmed)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return sections, nil
}

// parseEnabledList extracts the set of enabled daemon names from the
// "## enabled daemons" task list section.
func parseEnabledList(lines []string) map[string]bool {
	enabled := make(map[string]bool)
	for _, line := range lines {
		// Match "- [x] name" and "- [ ] name".
		name, en, ok := parseTaskItem(line)
		if ok {
			enabled[name] = en
		}
	}
	return enabled
}

// parseTaskItem parses a single Markdown task list item.
// Returns the item text, checked status, and whether the line was a task item.
func parseTaskItem(line string) (name string, checked bool, ok bool) {
	// Strip leading "- ".
	rest, hasDash := strings.CutPrefix(line, "- ")
	if !hasDash {
		return "", false, false
	}

	// Expect "[x] " or "[ ] ".
	if after, found := strings.CutPrefix(rest, "[x] "); found {
		return strings.TrimSpace(after), true, true
	}
	if after, found := strings.CutPrefix(rest, "[ ] "); found {
		return strings.TrimSpace(after), false, true
	}

	return "", false, false
}

// parseDaemonSection parses the body lines of a "## <name> properties"
// section into a DaemonConfig.
func parseDaemonSection(name string, lines []string, uid, home string) (DaemonConfig, error) {
	cfg := DaemonConfig{
		Name:         name,
		Restart:      DefaultRestart,
		ReadyTimeout: DefaultReadyTimeout,
		Env:          make(map[string]string),
	}

	for _, line := range lines {
		// Expect "- key: value"
		rest, ok := strings.CutPrefix(line, "- ")
		if !ok {
			continue
		}

		key, value, found := strings.Cut(rest, ": ")
		if !found {
			// Try with just ":" (no space) for backward compatibility.
			key, value, found = strings.Cut(rest, ":")
			if !found {
				continue
			}
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		// Expand variables in the value.
		value = expandDaemonVar(value, uid, home)

		switch key {
		case "exec":
			cfg.Exec = value
		case "order":
			n, err := strconv.Atoi(value)
			if err != nil {
				return cfg, fmt.Errorf("invalid order %q: %w", value, err)
			}
			cfg.Order = n
		case "restart":
			policy := RestartPolicy(value)
			if !validRestartPolicies[policy] {
				return cfg, fmt.Errorf("invalid restart policy %q (valid: always, on-failure, once, disabled)", value)
			}
			cfg.Restart = policy
		case "ready_timeout":
			d, err := parseDuration(value)
			if err != nil {
				return cfg, fmt.Errorf("invalid ready_timeout %q: %w", value, err)
			}
			cfg.ReadyTimeout = d
		case "socket":
			cfg.Socket = value
		case "env":
			// "env: KEY=VALUE"
			k, v, found := strings.Cut(value, "=")
			if !found {
				return cfg, fmt.Errorf("invalid env format %q (expected KEY=VALUE)", value)
			}
			cfg.Env[k] = v
		default:
			// Unknown keys are silently ignored for forward compatibility.
		}
	}

	// Validation.
	if cfg.Exec == "" {
		return cfg, fmt.Errorf("exec is required for daemon %q", name)
	}

	return cfg, nil
}

// parseDuration parses a duration string that may be a bare number (seconds)
// or a Go-style duration string ("5s", "1m30s", etc.).
func parseDuration(s string) (time.Duration, error) {
	// Try Go duration parsing first.
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}

	// Try bare number of seconds.
	if n, err := strconv.Atoi(s); err == nil {
		return time.Duration(n) * time.Second, nil
	}

	return 0, fmt.Errorf("cannot parse duration %q", s)
}

// expandDaemonVar substitutes ${UID}, ${HOME}, ${XDG_RUNTIME_DIR} in the string.
func expandDaemonVar(s, uid, home string) string {
	s = strings.ReplaceAll(s, "${UID}", uid)
	s = strings.ReplaceAll(s, "${HOME}", home)

	xdgRuntime := os.Getenv("XDG_RUNTIME_DIR")
	if xdgRuntime == "" {
		xdgRuntime = fmt.Sprintf("/run/user/%s", uid)
	}
	s = strings.ReplaceAll(s, "${XDG_RUNTIME_DIR}", xdgRuntime)

	return s
}

// SaveConfig writes the daemon configuration back to a file.
// It is a placeholder for future implementation.
func SaveConfig(path string, configs []DaemonConfig) error {
	return fmt.Errorf("SaveConfig is not yet implemented")
}

// Ensure filepath import is used (satisfies the linter).
var _ = filepath.Separator
