package dryrun

import (
	"context"
	"testing"

	"github.com/sutrakam/graphlume/internal/graph"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/store"
	"github.com/sutrakam/graphlume/pkg/model"
)

func TestAnalyzePatchDetectsBrokenCallers(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	reg := registry.New(db)
	run, err := reg.BeginRun(ctx, "fast", "")
	if err != nil {
		t.Fatal(err)
	}

	w, err := reg.NewWriter(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}

	// File 1: target definition file (service.go)
	f1ID, err := w.AddFile(ctx, &model.File{
		Path:        "pkg/service.go",
		Language:    "go",
		ContentHash: "h1",
	})
	if err != nil {
		t.Fatal(err)
	}

	symTargetID, err := w.AddSymbol(ctx, &model.Symbol{
		FileID:     f1ID,
		Name:       "ProcessItem",
		FQN:        "pkg.ProcessItem",
		Kind:       model.KindFunction,
		Signature:  "func ProcessItem(id string) error",
		Confidence: model.ConfidenceHigh,
		Source:     model.SourceSourceCode,
		Range: model.Range{
			Start: model.Position{Line: 5},
			End:   model.Position{Line: 10},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// File 2: caller file (handler.go)
	f2ID, err := w.AddFile(ctx, &model.File{
		Path:        "pkg/handler.go",
		Language:    "go",
		ContentHash: "h2",
	})
	if err != nil {
		t.Fatal(err)
	}

	symCallerID, err := w.AddSymbol(ctx, &model.Symbol{
		FileID:     f2ID,
		Name:       "HandleRequest",
		FQN:        "pkg.HandleRequest",
		Kind:       model.KindFunction,
		Signature:  "func HandleRequest()",
		Confidence: model.ConfidenceHigh,
		Source:     model.SourceSourceCode,
		Range: model.Range{
			Start: model.Position{Line: 12},
			End:   model.Position{Line: 20},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Edge: HandleRequest -> ProcessItem
	_, err = w.AddEdge(ctx, &model.Edge{
		SrcID:      symCallerID,
		DstID:      symTargetID,
		Kind:       model.EdgeCalls,
		Confidence: model.ConfidenceHigh,
		Evidence:   model.Evidence{FilePath: "pkg/handler.go", Source: model.SourceSourceCode},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := reg.CommitRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}

	g := graph.New(db)

	// Scenario 1: Propose changing signature of ProcessItem from `func(id string)` to `func(id string, ctx context.Context)`
	newContent := `package pkg

import "context"

func ProcessItem(id string, ctx context.Context) error {
	return nil
}
`
	rep, err := AnalyzePatch(ctx, reg, g, run.ID, "pkg/service.go", newContent)
	if err != nil {
		t.Fatalf("AnalyzePatch: %v", err)
	}

	if rep.Safe {
		t.Fatalf("expected patch to be unsafe, got safe=true")
	}
	if rep.BrokenCount != 1 {
		t.Fatalf("expected 1 broken caller, got %d", rep.BrokenCount)
	}
	if rep.BrokenCallers[0].CallerName != "HandleRequest" {
		t.Fatalf("expected caller HandleRequest, got %s", rep.BrokenCallers[0].CallerName)
	}

	// Scenario 2: Propose safe content (keeping same signature)
	safeContent := `package pkg

func ProcessItem(id string) error {
	// modified body only
	return nil
}
`
	repSafe, err := AnalyzePatch(ctx, reg, g, run.ID, "pkg/service.go", safeContent)
	if err != nil {
		t.Fatalf("AnalyzePatch safe: %v", err)
	}
	if !repSafe.Safe {
		t.Fatalf("expected patch to be safe, got unsafe: %+v", repSafe)
	}
	if repSafe.BrokenCount != 0 {
		t.Fatalf("expected 0 broken callers, got %d", repSafe.BrokenCount)
	}
}
