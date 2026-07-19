package daemon

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/0xADE/a-kerno/internal/mdparse"
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

// defaultTemplate is the content written to a new daemons.md when the file
// does not exist. It includes commented examples so the user can edit it.
const defaultTemplate = `# ADE Daemons Configuration
#
# Managed by a-kerno. Changes are picked up automatically via fsnotify.
#
# Format:
#   ## enabled daemons      – task list of daemons to launch at startup
#   ## <name> properties    – per-daemon configuration section
#
# Keys inside a "properties" section:
#   - exec: /path/to/binary           (required)
#   - order: 10                       (startup order, lower = earlier, default: 0)
#   - restart: on-failure             (always | on-failure | once | disabled)
#   - ready_timeout: 10               (seconds to wait for socket, default: 10)
#   - socket: /tmp/ade-${UID}/indexd  (optional Unix socket for readiness)
#   - env: KEY=VALUE                  (extra environment variable, repeatable)

## enabled daemons
- [x] a-lancxo

## a-lancxo properties
- exec: /usr/local/bin/a-lancxo
- order: 10
- restart: on-failure
- ready_timeout: 10
- socket: /tmp/ade-${UID}/indexd
- env: ADE_INDEXD_SOCK=/tmp/ade-${UID}/indexd
`

// LoadConfig reads and parses the daemons.md file at the given path.
// It returns a slice of DaemonConfig sorted by Order.
//
// If the file does not exist, a template daemons.md is created and an empty
// configuration slice is returned with no error.  The caller receives a
// warning via structured logging.
func LoadConfig(path string, uid, home string) ([]DaemonConfig, error) {
	//nolint:gosec // path originates from trusted config directory
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if createErr := createTemplateConfig(path); createErr != nil {
				return nil, fmt.Errorf("create template %s: %w", path, createErr)
			}
			slog.Warn("daemon config not found, created template for editing", "path", path)
			return []DaemonConfig{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	sections, err := mdparse.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	enabledSec := sections["enabled daemons"]
	var enabled map[string]bool
	if enabledSec != nil {
		enabled = enabledSec.Enabled
	}

	var configs []DaemonConfig
	for heading, sec := range sections {
		name, ok := strings.CutSuffix(heading, " properties")
		if !ok || name == "" || sec == nil {
			continue
		}

		cfg, err := daemonConfigFromProperties(name, sec.Properties, uid, home)
		if err != nil {
			return nil, fmt.Errorf("section %q: %w", heading, err)
		}

		if en, exists := enabled[name]; exists {
			cfg.Enabled = en
		}

		configs = append(configs, cfg)
	}

	if len(configs) == 0 {
		slog.Warn("no daemon properties sections found in config, running with empty daemon list", "path", path)
		return []DaemonConfig{}, nil
	}

	// Sort by Order ascending.
	sort.Slice(configs, func(i, j int) bool {
		return configs[i].Order < configs[j].Order
	})

	return configs, nil
}

// createTemplateConfig writes the default daemons.md template to path.
// It also creates parent directories if needed.
func createTemplateConfig(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // config dir is user-owned
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if err := os.WriteFile(path, []byte(defaultTemplate), 0o644); err != nil { //nolint:gosec // user config template
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// daemonConfigFromProperties builds a DaemonConfig from parsed key-value properties.
func daemonConfigFromProperties(name string, props map[string]string, uid, home string) (DaemonConfig, error) {
	cfg := DaemonConfig{
		Name:         name,
		Restart:      DefaultRestart,
		ReadyTimeout: DefaultReadyTimeout,
		Env:          make(map[string]string),
	}

	for key, value := range props {
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
			k, v, found := strings.Cut(value, "=")
			if !found {
				return cfg, fmt.Errorf("invalid env format %q (expected KEY=VALUE)", value)
			}
			cfg.Env[k] = v
		}
	}

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
