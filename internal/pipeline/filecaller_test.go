package pipeline

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/store"
	"github.com/sutrakam/graphlume/pkg/model"
)

// TestResolveAttributesCodeOutsideDefinitionsToTheFile: a call written in a
// test file's callback, outside every definition, is a call of the file's own
// code, and a file whose outside references bind to nothing gets no caller.
//
//	util.ts        defines target()
//	app.test.ts    defines helper() on lines 10-20; calls target at line 12
//	               (inside it) and line 30 (outside it); mentions nothing()
//	               at line 31, which names no definition
func TestResolveAttributesCodeOutsideDefinitionsToTheFile(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(t.TempDir(), "index.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := store.Migrate(ctx, db.DB, func() int64 { return 0 }); err != nil {
		t.Fatal(err)
	}
	reg := registry.New(db)
	run, err := reg.BeginRun(ctx, "auto", "fc")
	if err != nil {
		t.Fatal(err)
	}
	w, err := reg.NewWriter(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	file := func(path string) int64 {
		id, err := w.AddFile(ctx, &model.File{Path: path, Language: "typescript", ContentHash: "h" + path, ParseStatus: "ok"})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	sym := func(fileID int64, path, name string, start, end int) int64 {
		id, err := w.AddSymbol(ctx, &model.Symbol{
			FileID: fileID, Kind: model.KindFunction, Name: name, FQN: path + ":" + name, Exported: true,
			Range:      model.Range{Start: model.Position{Line: start}, End: model.Position{Line: end}},
			Confidence: model.ConfidenceHigh, Source: model.SourceSourceCode,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	util := file("util.ts")
	app := file("app.test.ts")
	target := sym(util, "util.ts", "target", 1, 5)
	helper := sym(app, "app.test.ts", "helper", 10, 20)
	for _, r := range []struct {
		name string
		line int
	}{{"target", 12}, {"target", 30}, {"nothing", 31}} {
		if err := w.AddReference(ctx, app, registry.Reference{
			Name: r.name, Kind: "reference.call", Line: r.line, ArgCount: model.ArityUnknown}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := reg.CommitRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := Resolve(ctx, reg, run.ID, ResolveOptions{}); err != nil {
		t.Fatal(err)
	}

	callers, err := reg.FileCallerIDs(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	fc, ok := callers["app.test.ts"]
	if !ok || len(callers) != 1 {
		t.Fatalf("file callers = %v, want exactly app.test.ts's", callers)
	}
	src := map[int]int64{}
	rows, err := db.QueryContext(ctx, `SELECT evidence_line, src_id FROM edges WHERE dst_id = ? AND index_run_id = ?`, target, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var line int
		var s int64
		if err := rows.Scan(&line, &s); err != nil {
			t.Fatal(err)
		}
		src[line] = s
	}
	if src[12] != helper {
		t.Errorf("the call inside helper comes from %d, want helper %d", src[12], helper)
	}
	if src[30] != fc {
		t.Errorf("the call outside every definition comes from %d, want the file caller %d", src[30], fc)
	}
	if _, ok := src[31]; ok {
		t.Errorf("an unbound reference produced an edge")
	}

	// A second resolve (nothing pending) keeps the caller and the edge.
	if _, err := Resolve(ctx, reg, run.ID, ResolveOptions{}); err != nil {
		t.Fatal(err)
	}
	if again, err := reg.FileCallerIDs(ctx, run.ID); err != nil || again["app.test.ts"] != fc {
		t.Errorf("a second resolve changed the file caller: %v (%v)", again, err)
	}
}
