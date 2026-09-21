package program

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseINIProgramFlatAndNamed(t *testing.T) {
	dir := t.TempDir()
	flat := filepath.Join(dir, "wallpaper.ini")
	if err := os.WriteFile(flat, []byte("exec = swaybg -i ${HOME}/w.jpg\nphase = early\nenv = A=1\nenv = B=2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := parseINIProgram(flat, "wallpaper", "1000", "/home/u")
	if err != nil {
		t.Fatalf("flat: %v", err)
	}
	if cfg.Exec != "swaybg -i /home/u/w.jpg" {
		t.Fatalf("Exec = %q", cfg.Exec)
	}
	if cfg.Phase != phaseEarly {
		t.Fatalf("Phase = %q", cfg.Phase)
	}
	if cfg.Source != "ini" {
		t.Fatalf("Source = %q", cfg.Source)
	}
	if cfg.Env["A"] != "1" || cfg.Env["B"] != "2" {
		t.Fatalf("Env = %#v", cfg.Env)
	}

	named := filepath.Join(dir, "picom.ini")
	if err := os.WriteFile(named, []byte("[picom]\nexec = picom\nenabled = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = parseINIProgram(named, "picom", "1000", "/home/u")
	if err != nil {
		t.Fatalf("named: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("enabled=false")
	}
	if cfg.Exec != "picom" {
		t.Fatalf("Exec = %q", cfg.Exec)
	}
}

func TestLoadProgramConfigsINIOverDesktop(t *testing.T) {
	adeDir := t.TempDir()
	xdgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(adeDir, "foo.ini"), []byte("exec = from-ini\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	desktop := "[Desktop Entry]\nExec=from-desktop\n"
	if err := os.WriteFile(filepath.Join(xdgDir, "foo.desktop"), []byte(desktop), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdgDir, "bar.desktop"), []byte(desktop), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgs, err := LoadProgramConfigs(adeDir, xdgDir, "1", "/home/u")
	if err != nil {
		t.Fatalf("LoadProgramConfigs: %v", err)
	}
	byName := map[string]ProgramConfig{}
	for _, c := range cfgs {
		byName[c.Name] = c
	}
	if byName["foo"].Exec != "from-ini" || byName["foo"].Source != "ini" {
		t.Fatalf("foo = %+v", byName["foo"])
	}
	if byName["bar"].Exec != "from-desktop" || byName["bar"].Source != "desktop" {
		t.Fatalf("bar = %+v", byName["bar"])
	}
}
