// Package sessionwm manages the ADE session compositor / window manager.
package sessionwm

import (
	"log/slog"
	"os"
	"strings"

	"github.com/0xADE/a-kerno/internal/kernocfg"
)

// Environment variable names.
const (
	EnvCompositor = "ADE_COMPOSITOR"
	EnvWM         = "ADE_WM"
	EnvRestart    = "ADE_WM_RESTART"
)

// RestartPolicy controls whether the session WM is restarted after exit.
type RestartPolicy string

const (
	RestartAlways    RestartPolicy = "always"
	RestartOnFailure RestartPolicy = "on-failure"
	RestartDisabled  RestartPolicy = "disabled"
)

// Config holds the resolved session WM command and restart policy.
type Config struct {
	// Spec is the full command string (binary + args), word-split on start.
	Spec string
	// Restart is the restart policy (default: always).
	Restart RestartPolicy
}

// LoadConfig resolves session WM settings from a-kerno.md and environment.
// Priority for run: ADE_COMPOSITOR → ADE_WM → a-kerno.md ## composer / run.
// ADE_WM_RESTART overrides restart from file.
func LoadConfig(kernoMDPath, uid, home string) Config {
	fileCfg, err := kernocfg.Load(kernoMDPath, uid, home)
	if err != nil {
		slog.Warn("failed to load a-kerno.md composer settings", "path", kernoMDPath, "error", err)
	}

	spec := strings.TrimSpace(os.Getenv(EnvCompositor))
	if spec == "" {
		spec = strings.TrimSpace(os.Getenv(EnvWM))
	}
	if spec == "" {
		spec = fileCfg.Run
	}

	restart := parseRestartPolicy(os.Getenv(EnvRestart))
	if restart == "" {
		restart = parseRestartPolicy(fileCfg.Restart)
	}
	if restart == "" {
		restart = RestartAlways
	}

	return Config{
		Spec:    spec,
		Restart: restart,
	}
}

func parseRestartPolicy(raw string) RestartPolicy {
	switch RestartPolicy(strings.TrimSpace(raw)) {
	case RestartAlways, RestartOnFailure, RestartDisabled:
		return RestartPolicy(strings.TrimSpace(raw))
	default:
		return ""
	}
}

// ParseSpec splits a command string into argv (simple whitespace split).
func ParseSpec(spec string) []string {
	fields := strings.Fields(spec)
	if len(fields) == 0 {
		return nil
	}
	return fields
}
