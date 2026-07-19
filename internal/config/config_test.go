package config

import (
	"os"
	"testing"
)

func TestExpandVarADERuntimeDir(t *testing.T) {
	t.Setenv("ADE_RUNTIME_DIR", "/tmp/ade-1000-debug")
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	got := expandVar("${ADE_RUNTIME_DIR}/indexd", "1000", "/home/user")
	want := "/tmp/ade-1000-debug/indexd"
	if got != want {
		t.Fatalf("expandVar() = %q, want %q", got, want)
	}
}

func TestExpandVarADERuntimeDirDefault(t *testing.T) {
	os.Unsetenv("ADE_RUNTIME_DIR")

	got := expandVar("${ADE_RUNTIME_DIR}/kerno", "42", "/home/user")
	want := "/tmp/ade-42/kerno"
	if got != want {
		t.Fatalf("expandVar() = %q, want %q", got, want)
	}
}

func TestAdeRuntimeDirFromEnv(t *testing.T) {
	t.Setenv("ADE_RUNTIME_DIR", "/custom/runtime")
	if got := adeRuntimeDir("1000"); got != "/custom/runtime" {
		t.Fatalf("adeRuntimeDir() = %q, want /custom/runtime", got)
	}
}
