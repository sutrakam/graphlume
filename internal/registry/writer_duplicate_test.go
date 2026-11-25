package registry

import (
	"context"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// The same edge written twice is one fact stated twice. The writer used to
// fail on the second insert, and a failed writer failed the whole file.
func TestWriterAcceptsTheSameEdgeTwiceAndStoresItOnce(t *testing.T) {
	ctx := context.Background()
	r, _ := newTestRegistry(t)
	run, err := r.BeginRun(ctx, "auto", "rev1")
	if err != nil {
		t.Fatal(err)
	}
	files := []fileSpec{{path: "a.go", lang: "go", hash: "h", syms: []symSpec{
		{fqn: "a.Pub", name: "Pub", line: 1, kind: model.KindFunction},
		{fqn: "event:kafka:orders", name: "orders", line: 2, kind: model.KindEvent},
	}}}
	dup := edgeSpec{srcFQN: "a.Pub", dstFQN: "event:kafka:orders", kind: model.EdgePublishes,
		conf: model.ConfidenceHigh, source: model.SourceSourceCode, evFile: "a.go", evLine: 2}
	writeContent(t, r, run.ID, files, []edgeSpec{dup, dup})

	edges, err := r.Edges(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("want the edge stored once, got %d", len(edges))
	}
}

// Tolerating a duplicate must not tolerate a broken invariant: an exact edge
// a compiler did not produce is still refused.
func TestWriterStillRefusesAnInvalidEdge(t *testing.T) {
	ctx := context.Background()
	r, _ := newTestRegistry(t)
	run, err := r.BeginRun(ctx, "auto", "rev1")
	if err != nil {
		t.Fatal(err)
	}
	ids := writeContent(t, r, run.ID, []fileSpec{{path: "a.go", lang: "go", hash: "h", syms: []symSpec{
		{fqn: "a.F", name: "F", line: 1, kind: model.KindFunction},
		{fqn: "a.G", name: "G", line: 5, kind: model.KindFunction},
	}}}, nil)
	w, err := r.NewWriter(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	e := &model.Edge{SrcID: ids["a.F"], DstID: ids["a.G"], Kind: model.EdgeCalls, Confidence: model.ConfidenceExact,
		Evidence: model.Evidence{FilePath: "a.go", Range: model.Range{Start: model.Position{Line: 2}}, Source: model.SourceSourceCode}}
	if _, err := w.AddEdge(ctx, e); err == nil {
		t.Fatal("an exact edge from source code was accepted")
	}
}
