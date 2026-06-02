// Package config provides configuration management for a-kerno.
// It reads environment variables, expands paths, and initializes
// the global configuration singleton.
package config

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
)

// Environment variable names used by a-kerno.
const (
	EnvConfigHome    = "ADE_CONFIG_HOME"
	EnvRuntimeDir    = "ADE_RUNTIME_DIR"
	EnvKernoSock     = "ADE_KERNO_SOCK"
	EnvIndexdSock    = "ADE_INDEXD_SOCK"
	EnvSkriptoSock   = "ADE_SKRIPTO_SOCK"
	EnvTondujoSock   = "ADE_TONDUJO_SOCK"
	EnvDosieragoSock = "ADE_DOSIERAGO_SOCK"
)

// Default values.
const (
	DefaultConfigHome = "~/.config/ade"
	DefaultRuntimeDir = "/tmp/ade-${UID}"
	DefaultKernoSock  = "/tmp/ade-${UID}/kerno"
)

// Config holds all configuration values for a-kerno.
type Config struct {
	// ConfigHome is the ADE configuration directory (expanded).
	ConfigHome string

	// RuntimeDir is the runtime directory for sockets and PID files (expanded).
	RuntimeDir string

	// KernoSock is the path to the management Unix socket.
	KernoSock string

	// DaemonsMD is the path to the daemons configuration file.
	DaemonsMD string

	// AutostartDir is the path to the autostart directory.
	AutostartDir string

	// DaemonSockets maps daemon names to their socket paths.
	DaemonSockets map[string]string
}

var (
	globalConfig *Config
	once         sync.Once
)

// Init initializes the global configuration. It is safe to call multiple times;
// only the first call performs the initialization.
func Init() error {
	var initErr error
	once.Do(func() {
		globalConfig = &Config{
			DaemonSockets: make(map[string]string),
		}

		uid, home, err := resolveUser()
		if err != nil {
			initErr = fmt.Errorf("resolve user: %w", err)
			return
		}

		// ADE_CONFIG_HOME
		globalConfig.ConfigHome = expandVar(
			envOrDefault(EnvConfigHome, DefaultConfigHome),
			uid, home,
		)

		// ADE_RUNTIME_DIR
		globalConfig.RuntimeDir = expandVar(
			envOrDefault(EnvRuntimeDir, DefaultRuntimeDir),
			uid, home,
		)

		// ADE_KERNO_SOCK
		globalConfig.KernoSock = expandVar(
			envOrDefault(EnvKernoSock, DefaultKernoSock),
			uid, home,
		)
		if globalConfig.KernoSock == "" {
			globalConfig.KernoSock = expandVar(DefaultKernoSock, uid, home)
		}

		// Paths derived from ConfigHome
		globalConfig.DaemonsMD = filepath.Join(globalConfig.ConfigHome, "daemons.md")
		globalConfig.AutostartDir = filepath.Join(globalConfig.ConfigHome, "autostart")

		// Collect daemon socket paths from environment
		collectSockets(globalConfig, uid, home)
	})
	return initErr
}

// Get returns the global configuration. If Init has not been called, it calls Init first.
func Get() *Config {
	if globalConfig == nil {
		_ = Init()
	}
	return globalConfig
}

// envOrDefault returns the value of the environment variable or the default.
func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// resolveUser returns the current user's UID and home directory.
func resolveUser() (uid, home string, err error) {
	currentUser, err := user.Current()
	if err != nil {
		return "", "", err
	}
	uid = currentUser.Uid
	home = currentUser.HomeDir
	return uid, home, nil
}

// expandVar substitutes ${UID}, ${HOME}, ${XDG_RUNTIME_DIR} and ~ in the path.
func expandVar(path, uid, home string) string {
	// Expand ~ prefix
	if strings.HasPrefix(path, "~") {
		path = strings.Replace(path, "~", home, 1)
	}

	// Expand ${VARIABLE} placeholders
	path = strings.ReplaceAll(path, "${UID}", uid)
	path = strings.ReplaceAll(path, "${HOME}", home)

	// ${XDG_RUNTIME_DIR}
	xdgRuntime := os.Getenv("XDG_RUNTIME_DIR")
	if xdgRuntime == "" {
		xdgRuntime = fmt.Sprintf("/run/user/%s", uid)
	}
	path = strings.ReplaceAll(path, "${XDG_RUNTIME_DIR}", xdgRuntime)

	return path
}

// collectSockets reads daemon socket paths from environment variables and
// stores them in the Config.
func collectSockets(cfg *Config, uid, home string) {
	socketVars := map[string]string{
		"a-lancxo":    EnvIndexdSock,
		"a-skripto":   EnvSkriptoSock,
		"a-tondujo":   EnvTondujoSock,
		"a-dosierago": EnvDosieragoSock,
	}

	for name, envVar := range socketVars {
		if v := os.Getenv(envVar); v != "" {
			cfg.DaemonSockets[name] = expandVar(v, uid, home)
		}
	}
}
