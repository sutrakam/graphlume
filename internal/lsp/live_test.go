package lsp

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	afs "github.com/sutrakam/graphlume/internal/acquire/fs"
	"github.com/sutrakam/graphlume/pkg/model"
)

// liveServer returns the path of a language server that is on PATH and
// actually runs. A rustup proxy for a component that is not installed is on
// PATH but exits with an error, so presence alone is not enough.
func liveServer(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s not found on PATH, skipping live LSP test", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput(); err != nil { //nolint:gosec // G204: a language server found on PATH, run with a fixed flag
		t.Skipf("%s is on PATH but does not run (%v: %s), skipping live LSP test", name, err, out)
	}
	return path
}

func TestLiveClangdIntegration(t *testing.T) {
	clangdPath := liveServer(t, "clangd")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	tmpDir := t.TempDir()

	// Create header.h and main.cpp
	headerPath := filepath.Join(tmpDir, "header.h")
	mainPath := filepath.Join(tmpDir, "main.cpp")
	compileCommandsPath := filepath.Join(tmpDir, "compile_commands.json")

	headerContent := `int computeAnswer(int x);
`
	mainContent := `#include "header.h"

int main() {
    int res = computeAnswer(42);
    return res;
}
`
	compileCommands := `[
  {
    "directory": "` + tmpDir + `",
    "command": "/usr/bin/clang++ -I. -c main.cpp",
    "file": "main.cpp"
  }
]
`
	if err := os.WriteFile(headerPath, []byte(headerContent), 0o644); err != nil {
		t.Fatalf("write header.h: %v", err)
	}
	if err := os.WriteFile(mainPath, []byte(mainContent), 0o644); err != nil {
		t.Fatalf("write main.cpp: %v", err)
	}
	if err := os.WriteFile(compileCommandsPath, []byte(compileCommands), 0o644); err != nil {
		t.Fatalf("write compile_commands.json: %v", err)
	}

	reg, cleanup := setupTestRegistry(t)
	defer cleanup()

	run, err := reg.BeginRun(ctx, "auto", "rev1")
	if err != nil {
		t.Fatalf("BeginRun: %v", err)
	}
	runID := run.ID

	w, err := reg.NewWriter(ctx, runID)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	headerFileID, err := w.AddFile(ctx, &model.File{
		Path:        "header.h",
		Language:    "cpp",
		ContentHash: afs.HashBytes([]byte(headerContent)),
		SizeBytes:   int64(len(headerContent)),
		ModTimeUnix: time.Now().UnixNano(),
		ParseStatus: "ok",
	})
	if err != nil {
		t.Fatalf("AddFile header.h: %v", err)
	}

	ansSymID, err := w.AddSymbol(ctx, &model.Symbol{
		FileID:     headerFileID,
		Kind:       model.KindFunction,
		Name:       "computeAnswer",
		FQN:        "header.h#computeAnswer",
		Range:      model.Range{Start: model.Position{Line: 1, Col: 4}, End: model.Position{Line: 1, Col: 17}},
		Exported:   true,
		Confidence: model.ConfidenceHigh,
		Source:     model.SourceSourceCode,
	})
	if err != nil {
		t.Fatalf("AddSymbol computeAnswer: %v", err)
	}

	mainFileID, err := w.AddFile(ctx, &model.File{
		Path:        "main.cpp",
		Language:    "cpp",
		ContentHash: afs.HashBytes([]byte(mainContent)),
		SizeBytes:   int64(len(mainContent)),
		ModTimeUnix: time.Now().UnixNano(),
		ParseStatus: "ok",
	})
	if err != nil {
		t.Fatalf("AddFile main.cpp: %v", err)
	}

	mainSymID, err := w.AddSymbol(ctx, &model.Symbol{
		FileID:     mainFileID,
		Kind:       model.KindFunction,
		Name:       "main",
		FQN:        "main.cpp#main",
		Range:      model.Range{Start: model.Position{Line: 3, Col: 0}, End: model.Position{Line: 6, Col: 0}},
		Exported:   true,
		Confidence: model.ConfidenceHigh,
		Source:     model.SourceSourceCode,
	})
	if err != nil {
		t.Fatalf("AddSymbol main: %v", err)
	}

	// Add heuristic edge from main() to computeAnswer()
	// Line 4 in main.cpp: "    int res = computeAnswer(42);"
	// "computeAnswer" is at col 14
	_, err = w.AddEdge(ctx, &model.Edge{
		SrcID:      mainSymID,
		DstID:      ansSymID,
		Kind:       model.EdgeCalls,
		Confidence: model.ConfidenceHigh,
		Evidence: model.Evidence{
			FilePath: "main.cpp",
			Range:    model.Range{Start: model.Position{Line: 4, Col: 14}},
			Source:   model.SourceSourceCode,
		},
	})
	if err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	if err := w.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	_ = w.Close()

	if err := reg.CommitRun(ctx, runID); err != nil {
		t.Fatalf("CommitRun: %v", err)
	}

	t.Logf("Running LSP bridge with real clangd (%s) on %s", clangdPath, tmpDir)
	outcome := RunLSPBridge(ctx, reg, runID, BridgeOptions{
		Root:           tmpDir,
		Logger:         slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})),
		AllowRepoBuild: true,
	})

	if outcome.Err != nil {
		t.Fatalf("RunLSPBridge err: %v", outcome.Err)
	}

	if len(outcome.Steps) == 0 {
		t.Fatalf("expected at least 1 step, got 0")
	}

	step := outcome.Steps[0]
	t.Logf("LSP Step: Server=%s, Ran=%v, Queried=%d, Resolved=%d, Upgraded=%d, SkippedReason=%s",
		step.Server, step.Ran, step.Queried, step.Resolved, step.EdgesUpgraded, step.SkippedReason)

	if !step.Ran {
		t.Fatalf("step did not run: %s", step.SkippedReason)
	}

	if step.EdgesUpgraded != 1 {
		t.Fatalf("expected 1 edge upgraded by clangd, got %d", step.EdgesUpgraded)
	}

	// Check registry edge
	edges, err := reg.Edges(ctx, runID)
	if err != nil {
		t.Fatalf("Edges: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	if edges[0].Confidence != model.ConfidenceExact {
		t.Errorf("expected exact confidence, got %s", edges[0].Confidence)
	}
	if edges[0].Evidence.Source != model.SourceCompiler {
		t.Errorf("expected source compiler, got %s", edges[0].Evidence.Source)
	}
	if edges[0].Evidence.Detail != "lsp: clangd" {
		t.Errorf("expected detail 'lsp: clangd', got %s", edges[0].Evidence.Detail)
	}
}

func TestLiveRustAnalyzerIntegration(t *testing.T) {
	raPath := liveServer(t, "rust-analyzer")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	tmpDir := t.TempDir()
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}

	cargoToml := `[package]
name = "test_crate"
version = "0.1.0"
edition = "2021"
`
	mainRs := `fn helper() -> i32 {
    42
}

fn main() {
    let val = helper();
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "Cargo.toml"), []byte(cargoToml), 0o644); err != nil {
		t.Fatalf("write Cargo.toml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.rs"), []byte(mainRs), 0o644); err != nil {
		t.Fatalf("write main.rs: %v", err)
	}

	// Warm up Cargo metadata
	cmd := exec.CommandContext(ctx, "cargo", "check")
	cmd.Dir = tmpDir
	_ = cmd.Run()

	reg, cleanup := setupTestRegistry(t)
	defer cleanup()

	run, err := reg.BeginRun(ctx, "auto", "rev1")
	if err != nil {
		t.Fatalf("BeginRun: %v", err)
	}
	runID := run.ID

	w, err := reg.NewWriter(ctx, runID)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	mainFileID, err := w.AddFile(ctx, &model.File{
		Path:        "src/main.rs",
		Language:    "rust",
		ContentHash: afs.HashBytes([]byte(mainRs)),
		SizeBytes:   int64(len(mainRs)),
		ModTimeUnix: time.Now().UnixNano(),
		ParseStatus: "ok",
	})
	if err != nil {
		t.Fatalf("AddFile main.rs: %v", err)
	}

	helperSymID, err := w.AddSymbol(ctx, &model.Symbol{
		FileID:     mainFileID,
		Kind:       model.KindFunction,
		Name:       "helper",
		FQN:        "src/main.rs#helper",
		Range:      model.Range{Start: model.Position{Line: 1, Col: 3}, End: model.Position{Line: 1, Col: 9}},
		Exported:   true,
		Confidence: model.ConfidenceHigh,
		Source:     model.SourceSourceCode,
	})
	if err != nil {
		t.Fatalf("AddSymbol helper: %v", err)
	}

	mainSymID, err := w.AddSymbol(ctx, &model.Symbol{
		FileID:     mainFileID,
		Kind:       model.KindFunction,
		Name:       "main",
		FQN:        "src/main.rs#main",
		Range:      model.Range{Start: model.Position{Line: 5, Col: 0}, End: model.Position{Line: 7, Col: 0}},
		Exported:   true,
		Confidence: model.ConfidenceHigh,
		Source:     model.SourceSourceCode,
	})
	if err != nil {
		t.Fatalf("AddSymbol main: %v", err)
	}

	// Line 6 in main.rs: "    let val = helper();"
	// "helper" starts at col 14
	_, err = w.AddEdge(ctx, &model.Edge{
		SrcID:      mainSymID,
		DstID:      helperSymID,
		Kind:       model.EdgeCalls,
		Confidence: model.ConfidenceHigh,
		Evidence: model.Evidence{
			FilePath: "src/main.rs",
			Range:    model.Range{Start: model.Position{Line: 6, Col: 14}},
			Source:   model.SourceSourceCode,
		},
	})
	if err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	if err := w.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	_ = w.Close()

	if err := reg.CommitRun(ctx, runID); err != nil {
		t.Fatalf("CommitRun: %v", err)
	}

	t.Logf("Running LSP bridge with real rust-analyzer (%s) on %s", raPath, tmpDir)
	outcome := RunLSPBridge(ctx, reg, runID, BridgeOptions{
		Root:           tmpDir,
		Logger:         slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})),
		AllowRepoBuild: true,
	})

	if outcome.Err != nil {
		t.Fatalf("RunLSPBridge err: %v", outcome.Err)
	}

	if len(outcome.Steps) == 0 {
		t.Fatalf("expected at least 1 step, got 0")
	}

	step := outcome.Steps[0]
	t.Logf("LSP Step: Server=%s, Ran=%v, Queried=%d, Resolved=%d, Upgraded=%d, SkippedReason=%s",
		step.Server, step.Ran, step.Queried, step.Resolved, step.EdgesUpgraded, step.SkippedReason)

	if !step.Ran {
		t.Fatalf("step did not run: %s", step.SkippedReason)
	}

	// Verify that unresolved LSP queries do not break anything and keep existing edges intact
	edges, err := reg.Edges(ctx, runID)
	if err != nil {
		t.Fatalf("Edges: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(edges))
	}
	// The heuristic edge is safely preserved at ConfidenceHigh
	if edges[0].Confidence != model.ConfidenceHigh {
		t.Errorf("expected edge to remain at ConfidenceHigh when LSP doesn't settle, got %s", edges[0].Confidence)
	}
}
