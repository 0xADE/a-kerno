package iniload

import (
	"testing"

	"gopkg.in/ini.v1"
)

func TestLoadShadowsEnv(t *testing.T) {
	data := []byte(`[a-lancxo]
exec = a-lancxo
env = A=1
env = B=2
`)
	f, err := Load(data)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sec := f.Section("a-lancxo")
	got := Shadows(sec, "env")
	if len(got) != 2 || got[0] != "A=1" || got[1] != "B=2" {
		t.Fatalf("Shadows(env) = %#v", got)
	}
	if String(sec, "exec") != "a-lancxo" {
		t.Fatalf("exec = %q", String(sec, "exec"))
	}
}

func TestNamedSectionsSkipsDefault(t *testing.T) {
	f, err := Load([]byte("[composer]\nrun = Hyprland\n"))
	if err != nil {
		t.Fatal(err)
	}
	named := NamedSections(f)
	if len(named) != 1 || named[0].Name() != "composer" {
		t.Fatalf("NamedSections = %#v", named)
	}
}

func TestProgramSectionDefaultThenNamed(t *testing.T) {
	flat, err := Load([]byte("exec = foo\nphase = early\n"))
	if err != nil {
		t.Fatal(err)
	}
	sec := ProgramSection(flat)
	if sec.Name() != ini.DefaultSection {
		t.Fatalf("flat section = %q", sec.Name())
	}
	if String(sec, "exec") != "foo" {
		t.Fatalf("exec = %q", String(sec, "exec"))
	}

	named, err := Load([]byte("[wallpaper]\nexec = swaybg\n"))
	if err != nil {
		t.Fatal(err)
	}
	sec = ProgramSection(named)
	if sec.Name() != "wallpaper" {
		t.Fatalf("named section = %q", sec.Name())
	}
}

func TestBoolDefault(t *testing.T) {
	f, err := Load([]byte("[d]\nenabled = false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if BoolDefault(f.Section("d"), "enabled", true) {
		t.Fatal("expected false")
	}
	if !BoolDefault(f.Section("d"), "missing", true) {
		t.Fatal("missing should default true")
	}
}
