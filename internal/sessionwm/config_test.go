package sessionwm

import (
	"os"
	"path/filepath"
	"testing"
)

func writeComposerConfig(t *testing.T, dir, run, restart string) string {
	t.Helper()
	path := filepath.Join(dir, "a-kerno.md")
	content := "# Configuration for a-kerno\n\nDon't rename subheaders!\n\n## composer\n"
	if run != "" {
		content += "- run: " + run + "\n"
	}
	if restart != "" {
		content += "- restart: " + restart + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigCompositorPreferred(t *testing.T) {
	dir := t.TempDir()
	path := writeComposerConfig(t, dir, "openbox", "disabled")
	t.Setenv(EnvCompositor, "Hyprland --config /tmp/h.conf")
	t.Setenv(EnvWM, "i3")
	t.Setenv(EnvRestart, "on-failure")

	cfg := LoadConfig(path, "1000", "/home/user")
	if cfg.Spec != "Hyprland --config /tmp/h.conf" {
		t.Fatalf("Spec = %q", cfg.Spec)
	}
	if cfg.Restart != RestartOnFailure {
		t.Fatalf("Restart = %q", cfg.Restart)
	}
}

func TestLoadConfigWMFallback(t *testing.T) {
	dir := t.TempDir()
	path := writeComposerConfig(t, dir, "openbox", "")
	t.Setenv(EnvCompositor, "")
	t.Setenv(EnvWM, "openbox")
	t.Setenv(EnvRestart, "")

	cfg := LoadConfig(path, "1000", "/home/user")
	if cfg.Spec != "openbox" {
		t.Fatalf("Spec = %q", cfg.Spec)
	}
	if cfg.Restart != RestartAlways {
		t.Fatalf("Restart default = %q, want always", cfg.Restart)
	}
}

func TestLoadConfigFromFile(t *testing.T) {
	dir := t.TempDir()
	path := writeComposerConfig(t, dir, "Hyprland --config ${HOME}/hypr.conf", "on-failure")
	t.Setenv(EnvCompositor, "")
	t.Setenv(EnvWM, "")
	t.Setenv(EnvRestart, "")

	cfg := LoadConfig(path, "1000", "/home/user")
	want := "Hyprland --config /home/user/hypr.conf"
	if cfg.Spec != want {
		t.Fatalf("Spec = %q, want %q", cfg.Spec, want)
	}
	if cfg.Restart != RestartOnFailure {
		t.Fatalf("Restart = %q", cfg.Restart)
	}
}

func TestLoadConfigEmpty(t *testing.T) {
	dir := t.TempDir()
	path := writeComposerConfig(t, dir, "", "")
	t.Setenv(EnvCompositor, "")
	t.Setenv(EnvWM, "")
	cfg := LoadConfig(path, "1000", "/home/user")
	if cfg.Spec != "" {
		t.Fatalf("Spec = %q, want empty", cfg.Spec)
	}
}

func TestLoadConfigMissingFileCreatesTemplate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a-kerno.md")
	t.Setenv(EnvCompositor, "")
	t.Setenv(EnvWM, "")

	cfg := LoadConfig(path, "1000", "/home/user")
	if cfg.Spec != "Hyprland" {
		t.Fatalf("Spec = %q, want Hyprland from template", cfg.Spec)
	}
	if cfg.Restart != RestartAlways {
		t.Fatalf("Restart = %q, want always", cfg.Restart)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("template not created: %v", err)
	}
}

func TestParseSpec(t *testing.T) {
	got := ParseSpec("Hyprland --config /tmp/x.conf")
	if len(got) != 3 || got[0] != "Hyprland" || got[2] != "/tmp/x.conf" {
		t.Fatalf("ParseSpec = %#v", got)
	}
	if ParseSpec("   ") != nil {
		t.Fatal("expected nil for blank spec")
	}
}

func TestShouldRestart(t *testing.T) {
	if !shouldRestart(RestartAlways, nil) {
		t.Fatal("always should restart on clean exit")
	}
	if shouldRestart(RestartOnFailure, nil) {
		t.Fatal("on-failure should not restart on clean exit")
	}
	if !shouldRestart(RestartOnFailure, contextError{}) {
		t.Fatal("on-failure should restart on error")
	}
	if shouldRestart(RestartDisabled, contextError{}) {
		t.Fatal("disabled should never restart")
	}
}

func TestNextRestartDelay(t *testing.T) {
	if d := nextRestartDelay(0); d.Seconds() != 1 {
		t.Fatalf("delay(0) = %v", d)
	}
	if d := nextRestartDelay(1); d.Seconds() != 2 {
		t.Fatalf("delay(1) = %v", d)
	}
	if d := nextRestartDelay(10); d.Seconds() != 30 {
		t.Fatalf("delay(10) = %v, want 30s cap", d)
	}
}

type contextError struct{}

func (contextError) Error() string { return "fail" }
