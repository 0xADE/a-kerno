package daemon

import (
	"os"
	"testing"
)

func TestExpandDaemonVarADERuntimeDir(t *testing.T) {
	t.Setenv("ADE_RUNTIME_DIR", "/tmp/ade-1000-debug")

	got := expandDaemonVar("ADE_INDEXD_SOCK=${ADE_RUNTIME_DIR}/indexd", "1000", "/home/user")
	want := "ADE_INDEXD_SOCK=/tmp/ade-1000-debug/indexd"
	if got != want {
		t.Fatalf("expandDaemonVar() = %q, want %q", got, want)
	}
}

func TestExpandDaemonVarADERuntimeDirDefault(t *testing.T) {
	os.Unsetenv("ADE_RUNTIME_DIR")

	got := expandDaemonVar("${ADE_RUNTIME_DIR}/indexd", "7", "/home/user")
	want := "/tmp/ade-7/indexd"
	if got != want {
		t.Fatalf("expandDaemonVar() = %q, want %q", got, want)
	}
}
