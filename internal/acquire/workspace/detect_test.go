package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectSingleRepo(t *testing.T) {
	tmp := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module myapp\n"), 0o644)

	topo, err := DetectWorkspace(tmp)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if topo.IsWorkspace {
		t.Errorf("expected single_repo, got workspace=%v type=%s", topo.IsWorkspace, topo.Type)
	}
}

func TestDetectExplicitWorkspaceManifest(t *testing.T) {
	tmp := t.TempDir()
	wsDir := filepath.Join(tmp, ".graphlume")
	_ = os.MkdirAll(wsDir, 0o755)

	manifest := `services:
  - name: auth
    path: services/auth
    role: service
  - name: common-proto
    path: libs/proto
    role: shared_lib
`
	_ = os.WriteFile(filepath.Join(wsDir, "workspace.yaml"), []byte(manifest), 0o644)

	topo, err := DetectWorkspace(tmp)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if !topo.IsWorkspace || topo.Type != "explicit_manifest" {
		t.Fatalf("expected explicit_manifest, got %+v", topo)
	}
	if len(topo.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(topo.Services))
	}
	if topo.Services[1].Role != "shared_lib" {
		t.Errorf("expected common-proto role=shared_lib, got %s", topo.Services[1].Role)
	}
}

func TestDetectPolyrepoWorkspace(t *testing.T) {
	tmp := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmp, "service-a", ".git"), 0o755)
	_ = os.MkdirAll(filepath.Join(tmp, "service-b", ".git"), 0o755)

	topo, err := DetectWorkspace(tmp)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if !topo.IsWorkspace || topo.Type != "polyrepo" {
		t.Fatalf("expected polyrepo workspace, got %+v", topo)
	}
	if len(topo.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(topo.Services))
	}
}

func TestDetectMonorepoWorkspace(t *testing.T) {
	tmp := t.TempDir()
	_ = os.WriteFile(filepath.Join(tmp, "go.work"), []byte("go 1.26\nuse ( ./services/auth ./services/billing )\n"), 0o644)

	_ = os.MkdirAll(filepath.Join(tmp, "services", "auth"), 0o755)
	_ = os.WriteFile(filepath.Join(tmp, "services", "auth", "go.mod"), []byte("module auth\n"), 0o644)

	_ = os.MkdirAll(filepath.Join(tmp, "services", "billing"), 0o755)
	_ = os.WriteFile(filepath.Join(tmp, "services", "billing", "package.json"), []byte(`{"name":"billing"}`), 0o644)

	topo, err := DetectWorkspace(tmp)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if !topo.IsWorkspace || topo.Type != "monorepo" {
		t.Fatalf("expected monorepo workspace, got %+v", topo)
	}
	if len(topo.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(topo.Services))
	}
}
