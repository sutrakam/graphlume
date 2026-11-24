package registry

import (
	"context"
	"testing"

	"github.com/sutrakam/graphlume/internal/store"
	"github.com/sutrakam/graphlume/pkg/model"
)

func TestPruneImpossibleEdges(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reg := New(db)

	run, err := reg.BeginRun(ctx, "auto", "test")
	if err != nil {
		t.Fatalf("BeginRun: %v", err)
	}
	runID := run.ID

	w, err := reg.NewWriter(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}

	fID, err := w.AddFile(ctx, &model.File{Path: "src/main.ts", Language: "typescript", ContentHash: "h1", ParseStatus: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	cfgFID, err := w.AddFile(ctx, &model.File{Path: "config/settings.json", Language: "json", ContentHash: "h2", ParseStatus: "ok"})
	if err != nil {
		t.Fatal(err)
	}

	callerID, err := w.AddSymbol(ctx, &model.Symbol{
		FileID: fID, Kind: model.KindFunction, Name: "main", FQN: "src/main.ts:main",
		Range:      model.Range{Start: model.Position{Line: 1}, End: model.Position{Line: 10}},
		Confidence: model.ConfidenceHigh, Source: model.SourceSourceCode,
	})
	if err != nil {
		t.Fatal(err)
	}

	cfgSymID, err := w.AddSymbol(ctx, &model.Symbol{
		FileID: cfgFID, Kind: model.KindConfiguration, Name: "window", FQN: "config/settings.json:window",
		Range:      model.Range{Start: model.Position{Line: 1}, End: model.Position{Line: 1}},
		Confidence: model.ConfidenceHigh, Source: model.SourceSourceCode,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := w.AddReference(ctx, fID, Reference{
		Name: "window", Kind: "reference.call", Line: 5, ArgCount: model.ArityUnknown,
	}); err != nil {
		t.Fatal(err)
	}

	if err := w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// Mark reference resolved to the configuration symbol
	if _, err := db.ExecContext(ctx, `
		UPDATE symbol_references 
		   SET resolution = 'resolved', resolved_symbol_id = ?, resolution_source = 'source_code'
		 WHERE index_run_id = ?`, cfgSymID, runID); err != nil {
		t.Fatal(err)
	}

	if _, err := reg.AddResolvedEdges(ctx, runID, []model.Edge{
		{
			SrcID: callerID, DstID: cfgSymID, Kind: model.EdgeCalls,
			Confidence: model.ConfidenceLow,
			Evidence: model.Evidence{
				FilePath: "src/main.ts",
				Range:    model.Range{Start: model.Position{Line: 5}},
				Source:   model.SourceSourceCode,
			},
		},
	}); err != nil {
		t.Fatalf("AddResolvedEdges: %v", err)
	}

	pruned, err := reg.PruneImpossibleEdges(ctx, runID)
	if err != nil {
		t.Fatalf("PruneImpossibleEdges: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1", pruned)
	}

	var edgeCount int
	if err := reg.db.QueryRowContext(ctx, `SELECT count(*) FROM edges WHERE index_run_id = ?`, runID).Scan(&edgeCount); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if edgeCount != 0 {
		t.Fatalf("edgeCount = %d, want 0", edgeCount)
	}

	var res string
	if err := reg.db.QueryRowContext(ctx, `SELECT resolution FROM symbol_references WHERE index_run_id = ?`, runID).Scan(&res); err != nil {
		t.Fatalf("get resolution: %v", err)
	}
	if res != "unresolved" {
		t.Fatalf("resolution = %q, want unresolved", res)
	}
}
