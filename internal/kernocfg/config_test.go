package kernocfg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCreatesTemplate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a-kerno.md")

	composer, err := Load(path, "1000", "/home/user")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if composer.Run != "Hyprland" {
		t.Fatalf("Run = %q, want Hyprland", composer.Run)
	}
	if composer.Restart != "always" {
		t.Fatalf("Restart = %q, want always", composer.Restart)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("template not created: %v", err)
	}
}

func TestLoadParsesComposer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a-kerno.md")
	content := `# Configuration for a-kerno

Don't rename subheaders!

## composer
- run: Hyprland --config ${HOME}/hypr.conf
- restart: on-failure
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	composer, err := Load(path, "42", "/home/test")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	wantRun := "Hyprland --config /home/test/hypr.conf"
	if composer.Run != wantRun {
		t.Fatalf("Run = %q, want %q", composer.Run, wantRun)
	}
	if composer.Restart != "on-failure" {
		t.Fatalf("Restart = %q", composer.Restart)
	}
}
