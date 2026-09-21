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

	"github.com/0xADE/a-kerno/internal/iniload"
	"gopkg.in/ini.v1"
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
// as parsed from a named section in daemons.ini.
type DaemonConfig struct {
	// Name is the daemon identifier (INI section name).
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

	// Enabled indicates whether the daemon should be started.
	Enabled bool
}

// Default values for DaemonConfig fields.
const (
	DefaultRestart      = RestartOnFailure
	DefaultReadyTimeout = 10 * time.Second
)

// defaultTemplate is the content written to a new daemons.ini when the file
// does not exist. It includes commented examples so the user can edit it.
const defaultTemplate = `# ADE Daemons Configuration
#
# Managed by a-kerno. Changes are picked up automatically via fsnotify.
#
# One section per daemon. Toggle enabled to start/stop on reload.
# Property changes do not restart an already running daemon.
#
# Keys:
#   enabled = true                    (default true)
#   exec = a-lancxo                   (required; PATH or absolute)
#   order = 10                        (startup order, lower = earlier, default: 0)
#   restart = on-failure              (always | on-failure | once | disabled)
#   ready_timeout = 10                (seconds to wait for socket, default: 10)
#   socket = ${ADE_RUNTIME_DIR}/indexd
#   env = KEY=VALUE                   (repeatable)

[a-lancxo]
enabled = true
exec = a-lancxo
order = 10
restart = on-failure
ready_timeout = 10
socket = ${ADE_RUNTIME_DIR}/indexd
env = ADE_INDEXD_SOCK=${ADE_RUNTIME_DIR}/indexd
`

// LoadConfig reads and parses the daemons.ini file at the given path.
// It returns a slice of DaemonConfig sorted by Order.
//
// If the file does not exist, a template daemons.ini is created and then parsed.
func LoadConfig(path string, uid, home string) ([]DaemonConfig, error) {
	warnLegacyMarkdown(path)

	//nolint:gosec // path originates from trusted config directory
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if createErr := createTemplateConfig(path); createErr != nil {
				return nil, fmt.Errorf("create template %s: %w", path, createErr)
			}
			slog.Warn("daemon config not found, created template for editing", "path", path)
			//nolint:gosec // path originates from trusted config directory
			data, err = os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read new template %s: %w", path, err)
			}
		} else {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
	}

	file, err := iniload.Load(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	var configs []DaemonConfig
	for _, sec := range iniload.NamedSections(file) {
		cfg, err := daemonConfigFromSection(sec, uid, home)
		if err != nil {
			return nil, fmt.Errorf("section %q: %w", sec.Name(), err)
		}
		configs = append(configs, cfg)
	}

	if len(configs) == 0 {
		slog.Warn("no daemon sections found in config, running with empty daemon list", "path", path)
		return []DaemonConfig{}, nil
	}

	sort.Slice(configs, func(i, j int) bool {
		return configs[i].Order < configs[j].Order
	})

	return configs, nil
}

func warnLegacyMarkdown(iniPath string) {
	mdPath := strings.TrimSuffix(iniPath, filepath.Ext(iniPath)) + ".md"
	if _, err := os.Stat(mdPath); err == nil {
		slog.Warn("legacy markdown config is ignored; use INI", "ignored", mdPath, "using", iniPath)
	}
}

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

func daemonConfigFromSection(sec *ini.Section, uid, home string) (DaemonConfig, error) {
	cfg := DaemonConfig{
		Name:         sec.Name(),
		Restart:      DefaultRestart,
		ReadyTimeout: DefaultReadyTimeout,
		Env:          make(map[string]string),
		Enabled:      iniload.BoolDefault(sec, "enabled", true),
	}

	if exec := iniload.String(sec, "exec"); exec != "" {
		cfg.Exec = expandDaemonVar(exec, uid, home)
	}

	if order := iniload.String(sec, "order"); order != "" {
		n, err := strconv.Atoi(order)
		if err != nil {
			return cfg, fmt.Errorf("invalid order %q: %w", order, err)
		}
		cfg.Order = n
	}

	if restart := strings.TrimSpace(iniload.String(sec, "restart")); restart != "" {
		policy := RestartPolicy(restart)
		if !validRestartPolicies[policy] {
			return cfg, fmt.Errorf("invalid restart policy %q (valid: always, on-failure, once, disabled)", restart)
		}
		cfg.Restart = policy
	}

	if timeout := iniload.String(sec, "ready_timeout"); timeout != "" {
		d, err := parseDuration(timeout)
		if err != nil {
			return cfg, fmt.Errorf("invalid ready_timeout %q: %w", timeout, err)
		}
		cfg.ReadyTimeout = d
	}

	if socket := iniload.String(sec, "socket"); socket != "" {
		cfg.Socket = expandDaemonVar(socket, uid, home)
	}

	for _, value := range iniload.Shadows(sec, "env") {
		value = expandDaemonVar(value, uid, home)
		k, v, found := strings.Cut(value, "=")
		if !found {
			return cfg, fmt.Errorf("invalid env format %q (expected KEY=VALUE)", value)
		}
		cfg.Env[k] = v
	}

	if cfg.Exec == "" {
		return cfg, fmt.Errorf("exec is required for daemon %q", cfg.Name)
	}

	return cfg, nil
}

func parseDuration(s string) (time.Duration, error) {
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}

	if n, err := strconv.Atoi(s); err == nil {
		return time.Duration(n) * time.Second, nil
	}

	return 0, fmt.Errorf("cannot parse duration %q", s)
}

func expandDaemonVar(s, uid, home string) string {
	s = strings.ReplaceAll(s, "${UID}", uid)
	s = strings.ReplaceAll(s, "${HOME}", home)

	xdgRuntime := os.Getenv("XDG_RUNTIME_DIR")
	if xdgRuntime == "" {
		xdgRuntime = fmt.Sprintf("/run/user/%s", uid)
	}
	s = strings.ReplaceAll(s, "${XDG_RUNTIME_DIR}", xdgRuntime)

	s = strings.ReplaceAll(s, "${ADE_RUNTIME_DIR}", adeRuntimeDir(uid))

	return s
}

func adeRuntimeDir(uid string) string {
	if v := os.Getenv("ADE_RUNTIME_DIR"); v != "" {
		return v
	}
	return fmt.Sprintf("/tmp/ade-%s", uid)
}

// SaveConfig writes the daemon configuration back to a file.
// It is a placeholder for future implementation.
func SaveConfig(path string, configs []DaemonConfig) error {
	return fmt.Errorf("SaveConfig is not yet implemented")
}
