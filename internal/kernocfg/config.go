// Package kernocfg loads a-kerno.md (mdconfig) for core a-kerno settings.
package kernocfg

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/0xADE/a-kerno/internal/mdparse"
)

const (
	// ComposerSection is the H2 section name for session WM / compositor settings.
	ComposerSection = "composer"
)

// Composer holds composer settings from ## composer in a-kerno.md.
type Composer struct {
	// Run is the full WM/compositor command line.
	Run string
	// Restart is the restart policy string: always, on-failure, disabled.
	Restart string
}

// Load reads a-kerno.md at path and returns the composer section.
// If the file does not exist, a template is written and composer defaults are returned.
// Parse errors are logged and an empty Composer is returned.
func Load(path, uid, home string) (Composer, error) {
	//nolint:gosec // path originates from trusted config directory
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if createErr := writeTemplate(path); createErr != nil {
				return Composer{}, fmt.Errorf("create template %s: %w", path, createErr)
			}
			slog.Warn("a-kerno config not found, created template for editing", "path", path)
			//nolint:gosec // path originates from trusted config directory
			data, err = os.ReadFile(path)
			if err != nil {
				return Composer{}, fmt.Errorf("read new template %s: %w", path, err)
			}
		} else {
			return Composer{}, fmt.Errorf("read %s: %w", path, err)
		}
	}

	composer, err := parseComposer(data, uid, home)
	if err != nil {
		slog.Warn("failed to parse a-kerno config, using empty composer settings",
			"path", path, "error", err)
		return Composer{}, nil
	}
	return composer, nil
}

const defaultTemplate = `# Configuration for a-kerno
#
# Managed by a-kerno.
#
# Don't rename subheaders!
#
# ## composer — session window manager / compositor
# Keys:
#   - run: command line (required for GUI session)
#   - restart: always | on-failure | disabled

## composer
- run: Hyprland
- restart: always
`

func writeTemplate(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // user config dir
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if err := os.WriteFile(path, []byte(defaultTemplate), 0o644); err != nil { //nolint:gosec // user config template
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func parseComposer(data []byte, uid, home string) (Composer, error) {
	sections, err := mdparse.Parse(data)
	if err != nil {
		return Composer{}, fmt.Errorf("parse: %w", err)
	}

	sec := sections[ComposerSection]
	if sec == nil {
		return Composer{}, nil
	}

	out := Composer{
		Run:     expandVar(strings.TrimSpace(sec.Properties["run"]), uid, home),
		Restart: strings.TrimSpace(sec.Properties["restart"]),
	}
	return out, nil
}

func expandVar(s, uid, home string) string {
	if strings.HasPrefix(s, "~") {
		s = strings.Replace(s, "~", home, 1)
	}
	s = strings.ReplaceAll(s, "${UID}", uid)
	s = strings.ReplaceAll(s, "${HOME}", home)

	xdgRuntime := os.Getenv("XDG_RUNTIME_DIR")
	if xdgRuntime == "" {
		xdgRuntime = fmt.Sprintf("/run/user/%s", uid)
	}
	s = strings.ReplaceAll(s, "${XDG_RUNTIME_DIR}", xdgRuntime)

	adeRuntime := os.Getenv("ADE_RUNTIME_DIR")
	if adeRuntime == "" {
		adeRuntime = fmt.Sprintf("/tmp/ade-%s", uid)
	}
	s = strings.ReplaceAll(s, "${ADE_RUNTIME_DIR}", adeRuntime)

	return s
}
