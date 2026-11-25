package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/acquire/workspace"
	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/multirepo"
)

func TestWorkspaceDetectCLI(t *testing.T) {
	tmp := t.TempDir()

	// 1. Single repo test
	g := &Global{
		cfg: config.Config{RepoRoot: tmp},
	}
	cmd := newWorkspaceDetectCmd(g)
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("detect run error: %v", err)
	}
	if !strings.Contains(out.String(), "Single repository") {
		t.Errorf("expected 'Single repository', got: %s", out.String())
	}

	// 2. Monorepo partitioned test with JSON
	svcDir1 := filepath.Join(tmp, "services", "auth")
	if err := os.MkdirAll(svcDir1, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcDir1, "go.mod"), []byte("module auth\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	svcDir2 := filepath.Join(tmp, "services", "billing")
	if err := os.MkdirAll(svcDir2, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcDir2, "go.mod"), []byte("module billing\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	g.JSON = true
	cmdJSON := newWorkspaceDetectCmd(g)
	var jsonOut bytes.Buffer
	cmdJSON.SetOut(&jsonOut)
	if err := cmdJSON.RunE(cmdJSON, nil); err != nil {
		t.Fatalf("detect run JSON error: %v", err)
	}

	var topo workspace.WorkspaceTopology
	if err := json.Unmarshal(jsonOut.Bytes(), &topo); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if !topo.IsWorkspace || len(topo.Services) != 2 {
		t.Errorf("expected 2 services detected, got: %+v", topo)
	}
}

func TestWorkspaceListCLI(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	// Pre-populate registry
	entry := multirepo.ProjectEntry{
		ID:            "my-service",
		RootPath:      filepath.Join(fakeHome, "src", "my-service"),
		DBPath:        filepath.Join(fakeHome, "src", "my-service", ".graphlume", "index.db"),
		Mode:          "single_repo",
		LastIndexedAt: "2026-11-21T12:00:00Z",
	}
	if err := multirepo.RegisterProject(context.Background(), fakeHome, entry); err != nil {
		t.Fatalf("register project: %v", err)
	}

	g := &Global{}
	cmd := newWorkspaceListCmd(g)
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("workspace list error: %v", err)
	}
	if !strings.Contains(out.String(), "my-service") || !strings.Contains(out.String(), "indexed") {
		t.Errorf("expected my-service in output, got: %s", out.String())
	}

	// Test JSON output
	g.JSON = true
	var jsonOut bytes.Buffer
	cmdJSON := newWorkspaceListCmd(g)
	cmdJSON.SetOut(&jsonOut)
	if err := cmdJSON.RunE(cmdJSON, nil); err != nil {
		t.Fatalf("workspace list JSON error: %v", err)
	}

	var reg multirepo.GlobalRegistry
	if err := json.Unmarshal(jsonOut.Bytes(), &reg); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if len(reg.Projects) != 1 || reg.Projects[0].ID != "my-service" {
		t.Errorf("expected my-service project entry, got: %+v", reg)
	}
}
