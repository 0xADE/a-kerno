package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestLoadConfigCreatesAndParsesTemplate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemons.ini")
	t.Setenv("ADE_RUNTIME_DIR", "/tmp/ade-debug")

	configs, err := LoadConfig(path, "1000", "/home/user")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("template not created: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("len = %d, want 1 from template", len(configs))
	}
	cfg := configs[0]
	if cfg.Name != "a-lancxo" {
		t.Fatalf("Name = %q", cfg.Name)
	}
	if cfg.Exec != "a-lancxo" {
		t.Fatalf("Exec = %q", cfg.Exec)
	}
	if !cfg.Enabled {
		t.Fatal("template a-lancxo should be enabled")
	}
	if cfg.Socket != "/tmp/ade-debug/indexd" {
		t.Fatalf("Socket = %q", cfg.Socket)
	}
	if cfg.Env["ADE_INDEXD_SOCK"] != "/tmp/ade-debug/indexd" {
		t.Fatalf("Env = %#v", cfg.Env)
	}
}

func TestLoadConfigEnabledAndMultiEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemons.ini")
	content := `[a-lancxo]
enabled = true
exec = a-lancxo
order = 10
env = FOO=1
env = BAR=${HOME}/x

[disabled-one]
enabled = false
exec = /bin/false
order = 20
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	configs, err := LoadConfig(path, "1000", "/home/test")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(configs) != 2 {
		t.Fatalf("len = %d", len(configs))
	}
	if configs[0].Name != "a-lancxo" {
		t.Fatalf("order: first = %q", configs[0].Name)
	}
	if configs[0].Env["FOO"] != "1" || configs[0].Env["BAR"] != "/home/test/x" {
		t.Fatalf("env = %#v", configs[0].Env)
	}
	if configs[1].Enabled {
		t.Fatal("disabled-one should be disabled")
	}
}

func TestLoadConfigWarnsLegacyMarkdown(t *testing.T) {
	dir := t.TempDir()
	iniPath := filepath.Join(dir, "daemons.ini")
	mdPath := filepath.Join(dir, "daemons.md")
	if err := os.WriteFile(mdPath, []byte("# leftover"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(iniPath, []byte("[d]\nexec = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(iniPath, "1", "/home/u"); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
}

func TestParseDuration(t *testing.T) {
	d, err := parseDuration("10")
	if err != nil || d != 10*time.Second {
		t.Fatalf("bare seconds: %v %v", d, err)
	}
	d, err = parseDuration("5s")
	if err != nil || d != 5*time.Second {
		t.Fatalf("go duration: %v %v", d, err)
	}
}
