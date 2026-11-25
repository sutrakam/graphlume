package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A declined offer is added to the user config without losing the user's
// comments or keys, is read back by Load, and is forgotten by ForgetDeclined.
func TestRememberAndForgetDeclined(t *testing.T) {
	home := t.TempDir()
	t.Setenv("GRAPHLUME_CONFIG_HOME", home)
	path := filepath.Join(home, "config.yml")
	orig := "# my settings\naccuracy:\n  mode: auto # keep\n  indexers:\n    provision: prompt\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"toolchain:dotnet", "indexer:scip-python", "toolchain:dotnet"} {
		if err := RememberDeclined(path, k); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{"# my settings", "# keep", "provision: prompt", "- toolchain:dotnet", "- indexer:scip-python"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("config lacks %q:\n%s", want, b)
		}
	}
	if strings.Count(string(b), "toolchain:dotnet") != 1 {
		t.Errorf("a key was remembered twice:\n%s", b)
	}
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.IndexerDeclined, ",") != "toolchain:dotnet,indexer:scip-python" {
		t.Errorf("IndexerDeclined = %v", cfg.IndexerDeclined)
	}
	n, err := ForgetDeclined(path)
	if err != nil || n != 2 {
		t.Fatalf("ForgetDeclined = %d, %v", n, err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), "declined") || !strings.Contains(string(b), "provision: prompt") {
		t.Errorf("after forgetting:\n%s", b)
	}
}

// With no user config at all, remembering creates it, private to the user.
func TestRememberDeclinedCreatesConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yml")
	if err := RememberDeclined(path, "toolchain:node"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", fi.Mode().Perm())
	}
	if n, err := ForgetDeclined(filepath.Join(t.TempDir(), "absent.yml")); n != 0 || err != nil {
		t.Errorf("forgetting a missing file: %d, %v", n, err)
	}
}
