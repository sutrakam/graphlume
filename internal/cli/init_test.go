package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/multirepo"
)

func TestInitWizardBasic(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	g := &Global{Project: dir}
	cmd := newInitCmd(g)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--rules"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("graphlume init failed: %v\nstderr: %s", err, errOut.String())
	}

	dbPath := filepath.Join(dir, IndexDir, IndexFile)
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected index.db to exist at %s: %v", dbPath, err)
	}

	agentsMD := filepath.Join(dir, "AGENTS.md")
	if _, err := os.Stat(agentsMD); err != nil {
		t.Fatalf("expected AGENTS.md to be created: %v", err)
	}
	content, _ := os.ReadFile(agentsMD)
	if !bytes.Contains(content, []byte("GraphLume")) {
		t.Errorf("AGENTS.md does not mention GraphLume:\n%s", string(content))
	}
}

func TestInitWizardFlags(t *testing.T) {
	dir := t.TempDir()
	g := &Global{Project: dir}

	// Plain init does not create AGENTS.md without --rules or --all
	cmd := newInitCmd(g)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("graphlume init failed: %v", err)
	}

	dbPath := filepath.Join(dir, IndexDir, IndexFile)
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected index.db to exist: %v", err)
	}

	agentsMD := filepath.Join(dir, "AGENTS.md")
	if _, err := os.Stat(agentsMD); err == nil {
		t.Errorf("expected AGENTS.md to NOT exist without --rules")
	}

	// Re-running without --force should fail
	cmd2 := newInitCmd(g)
	cmd2.SetArgs([]string{})
	if err := cmd2.Execute(); err == nil {
		t.Errorf("expected error when running init twice without --force, got nil")
	}

	// Re-running with --force and --all should succeed and create AGENTS.md
	cmd3 := newInitCmd(g)
	cmd3.SetArgs([]string{"--force", "--all"})
	if err := cmd3.Execute(); err != nil {
		t.Errorf("expected init --force --all to succeed, got %v", err)
	}
	if _, err := os.Stat(agentsMD); err != nil {
		t.Errorf("expected AGENTS.md to exist with --all, got err: %v", err)
	}
}

func TestInitRegistersProjectInRegistry(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	dir := t.TempDir()
	g := &Global{Project: dir}
	cmd := newInitCmd(g)
	cmd.SetArgs([]string{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	reg, err := multirepo.LoadGlobalRegistry(fakeHome)
	if err != nil {
		t.Fatalf("load global registry: %v", err)
	}
	if len(reg.Projects) != 1 {
		t.Fatalf("expected 1 project registered, got: %d", len(reg.Projects))
	}
	if reg.Projects[0].RootPath != multirepo.CanonicalPath(dir) {
		t.Errorf("expected root path %s, got %s", dir, reg.Projects[0].RootPath)
	}
}

// The invariant the compatibility gate enforces: onboarding always completes.
// An indexer that cannot be installed is named, with the download it was going
// to make, and the project is still indexed and resolved without it.
func TestInitIndexersFailureNeverStopsOnboarding(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stand-ins for node and npm")
	}
	bin := t.TempDir()
	script := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil { //nolint:gosec // test stand-in executable
			t.Fatal(err)
		}
	}
	script("node", `echo v22.0.0`)
	script("npm", `echo "npm: registry unreachable" >&2; exit 1`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname = \"x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.py"), []byte("def f():\n    return g()\n\n\ndef g():\n    return 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	g := &Global{Project: dir}
	cmd := newInitCmd(g)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--index", "--indexers", "--yes"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("onboarding stopped because an indexer could not be installed: %v\n%s", err, out.String())
	}
	text := out.String()
	for _, want := range []string{"Missing:", "@sourcegraph/scip-python@", "npm registry", "could not install scip-python", "graded edges only", "Resolved "} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, text)
		}
	}
}

// --all is the whole onboarding, compiler indexers included: with it the
// download is described, and without a terminal or --yes nothing is installed.
func TestInitAllImpliesIndexers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stand-ins for node and npm")
	}
	bin := t.TempDir()
	for name, body := range map[string]string{"node": "echo v22.0.0", "npm": "exit 1"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil { //nolint:gosec // test stand-in executable
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.py"), []byte("def f():\n    return 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname = \"x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newInitCmd(&Global{Project: dir})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--all", "--local"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --all: %v\n%s", err, out.String())
	}
	for _, want := range []string{"@sourcegraph/scip-python@", "plan only", "Nothing is installed", "Python stays graded"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("--all did not reach the guided indexer setup (lacks %q):\n%s", want, out.String())
		}
	}
}
