package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSetupDryRun(t *testing.T) {
	dir := t.TempDir()
	g := &Global{Project: dir}
	cmd := newSetupCmd(g)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("setup --dry-run failed: %v", err)
	}

	dbPath := filepath.Join(dir, IndexDir, IndexFile)
	if _, err := os.Stat(dbPath); err == nil {
		t.Fatalf("dry-run should not create %s", dbPath)
	}
	agentsMD := filepath.Join(dir, "AGENTS.md")
	if _, err := os.Stat(agentsMD); err == nil {
		t.Fatalf("dry-run should not create %s", agentsMD)
	}
	if !bytes.Contains(out.Bytes(), []byte("[dry-run]")) {
		t.Errorf("expected dry-run output to indicate dry-run, got:\n%s", out.String())
	}
}

func TestSetupExecution(t *testing.T) {
	dir := t.TempDir()
	goFile := filepath.Join(dir, "calc.go")
	if err := os.WriteFile(goFile, []byte("package calc\n\nfunc Add(a, b int) int { return a + b }\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	g := &Global{Project: dir}
	cmd := newSetupCmd(g)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("setup failed: %v\nstderr: %s", err, errOut.String())
	}

	dbPath := filepath.Join(dir, IndexDir, IndexFile)
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected index.db to exist at %s: %v", dbPath, err)
	}

	agentsMD := filepath.Join(dir, "AGENTS.md")
	if _, err := os.Stat(agentsMD); err != nil {
		t.Fatalf("expected AGENTS.md to be created: %v", err)
	}
}

func TestSetupJSON(t *testing.T) {
	dir := t.TempDir()
	g := &Global{Project: dir, JSON: true}
	cmd := newSetupCmd(g)
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--no-index", "--no-rules"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("setup failed: %v\nstderr: %s", err, errOut.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("expected valid JSON, got: %s\nerr: %v", out.String(), err)
	}
	if payload["index_path"] == "" {
		t.Errorf("expected non-empty index_path in json payload")
	}
}
