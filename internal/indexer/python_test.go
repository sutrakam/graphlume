package indexer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPythonIndexerNoPathPrefixAndEnvWiring(t *testing.T) {
	repo := t.TempDir()
	backendDir := filepath.Join(repo, "backend", "python")
	svcDir := filepath.Join(backendDir, "orders-service")
	sharedDir := filepath.Join(backendDir, "shared-service")
	venvDir := filepath.Join(svcDir, ".venv")
	binDir := filepath.Join(venvDir, "bin")

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create mock pip in venv
	pipFile := filepath.Join(binDir, "pip")
	if err := os.WriteFile(pipFile, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	exec := Executor{RunnerKind: "native"}
	inst := Installed{Indexer: SCIPPython, Path: "scip-python"}
	p := Project{Language: LangPython, Dir: "backend/python/orders-service"}
	outDir := filepath.Join(repo, ".graphlume", "scip")
	indexPath := filepath.Join(outDir, "python-orders-service.scip")

	cmd, err := exec.command(inst, p, p.Dir, svcDir, repo, outDir, indexPath)
	if err != nil {
		t.Fatalf("exec.Command failed: %v", err)
	}

	// 1. Verify --path-prefix is NOT in arguments
	for _, arg := range cmd.Args {
		if arg == "--path-prefix" {
			t.Errorf("scip-python arguments must not include --path-prefix, got args: %v", cmd.Args)
		}
	}

	// 2. Verify VIRTUAL_ENV is set to venvDir
	var hasVenv, hasPath, hasPyPath bool
	for _, e := range cmd.Env {
		if e == "VIRTUAL_ENV="+venvDir {
			hasVenv = true
		}
		if strings.HasPrefix(e, "PATH=") && strings.Contains(e, binDir) {
			hasPath = true
		}
		if strings.HasPrefix(e, "PYTHONPATH=") && strings.Contains(e, sharedDir) {
			hasPyPath = true
		}
	}
	if !hasVenv {
		t.Errorf("missing or incorrect VIRTUAL_ENV in env: %v", cmd.Env)
	}
	if !hasPath {
		t.Errorf("missing venv bin in PATH in env: %v", cmd.Env)
	}
	if !hasPyPath {
		t.Errorf("missing shared-service in PYTHONPATH in env: %v", cmd.Env)
	}

	// 3. Since pip is present in venv, --environment [] should NOT be passed
	for _, arg := range cmd.Args {
		if arg == "--environment" {
			t.Errorf("scip-python with venv pip should not suppress environment, got args: %v", cmd.Args)
		}
	}
}

func TestPythonIndexerSiblingVenvFallback(t *testing.T) {
	repo := t.TempDir()
	backendDir := filepath.Join(repo, "backend", "python")
	svcDir := filepath.Join(backendDir, "accounts-service")
	siblingDir := filepath.Join(backendDir, "catalog-service")
	siblingVenv := filepath.Join(siblingDir, ".venv")
	siblingBin := filepath.Join(siblingVenv, "bin")

	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(siblingBin, 0o755); err != nil {
		t.Fatal(err)
	}

	found := findPythonVenv(svcDir, repo)
	if found != siblingVenv {
		t.Errorf("findPythonVenv = %q, want sibling venv %q", found, siblingVenv)
	}
}
