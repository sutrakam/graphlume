package registry

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

// seedStatsRun writes a small run with references in two languages and returns
// the registry and the run id.
func seedStatsRun(t *testing.T) (*Registry, int64) {
	t.Helper()
	r, _ := newTestRegistry(t)
	run := seedResolutions(t, r, []refSpec{
		{path: "a.go", name: "A", line: 1, col: 1, resolution: ResolutionResolved},
		{path: "a.go", name: "B", line: 2, col: 1, resolution: ResolutionAmbiguous},
		{path: "b.go", name: "C", line: 1, col: 1, resolution: ResolutionUnresolved},
	})
	return r, run
}

// TestStoredStatsEqualTheLiveFigures: what resolve stores is what a live read
// returns, for both the per-language figures and the run totals.
func TestStoredStatsEqualTheLiveFigures(t *testing.T) {
	ctx := context.Background()
	r, run := seedStatsRun(t)

	liveLangs, err := r.computeLanguageBindings(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	liveTotals, err := r.computeResolutionStats(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := r.storedLanguageBindings(ctx, run); ok {
		t.Fatal("stored figures reported valid before anything stored them")
	}

	if err := r.RefreshRunStats(ctx, run); err != nil {
		t.Fatal(err)
	}
	langs, ok, err := r.storedLanguageBindings(ctx, run)
	if err != nil || !ok {
		t.Fatalf("stored language bindings: ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(langs, liveLangs) {
		t.Errorf("stored language bindings differ from the live read:\n stored %+v\n live   %+v", langs, liveLangs)
	}
	totals, ok, err := r.storedResolutionStats(ctx, run)
	if err != nil || !ok {
		t.Fatalf("stored resolution stats: ok=%v err=%v", ok, err)
	}
	if totals != liveTotals {
		t.Errorf("stored totals %+v differ from the live read %+v", totals, liveTotals)
	}
}

// TestAWriteClearsTheStoredFiguresSoAnAnswerIsNeverStale: the stored rows are a
// cache of the tables, and a cache that outlives a write would state a coverage
// the index no longer has.
func TestAWriteClearsTheStoredFiguresSoAnAnswerIsNeverStale(t *testing.T) {
	ctx := context.Background()

	// Each write gets an index of its own. Sharing one and running in
	// map order would make the outcome vary: when the file is deleted first its references go with
	// it, the later DELETE of a reference removes no row, no trigger fires,
	// and the test fails about one run in twelve.
	writes := []struct{ name, stmt string }{
		{"a reference is re-resolved", `UPDATE symbol_references SET resolution = 'unresolved'
		                                WHERE index_run_id = ` + "%d" + ` AND name = 'B'`},
		{"a reference is deleted", `DELETE FROM symbol_references WHERE index_run_id = ` + "%d" + ` AND name = 'C'`},
		{"a file is deleted", `DELETE FROM files WHERE index_run_id = ` + "%d" + ` AND path = 'b.go'`},
	}
	for _, w := range writes {
		name, stmt := w.name, w.stmt
		t.Run(name, func(t *testing.T) {
			r, run := seedStatsRun(t)
			if err := r.RefreshRunStats(ctx, run); err != nil {
				t.Fatal(err)
			}
			if _, ok, _ := r.storedResolutionStats(ctx, run); !ok {
				t.Fatal("refresh did not make the stored figures valid")
			}
			res, err := r.db.ExecContext(ctx, fmt.Sprintf(stmt, run))
			if err != nil {
				t.Fatal(err)
			}
			if n, _ := res.RowsAffected(); n == 0 {
				t.Fatalf("%s changed no row, so the test proves nothing", name)
			}
			if _, ok, _ := r.storedResolutionStats(ctx, run); ok {
				t.Errorf("stored figures still valid after %s", name)
			}
			// And the public read falls back to the live figures.
			live, _ := r.computeResolutionStats(ctx, run)
			got, err := r.ResolutionStats(ctx, run)
			if err != nil || got != live {
				t.Errorf("ResolutionStats after %s = %+v (err %v), want the live %+v", name, got, err, live)
			}
		})
	}
}

// TestAnIndexWithNoStoredFiguresStillAnswers is an index written by an older
// build: no run_stats row. The reads compute, as they did before the table.
func TestAnIndexWithNoStoredFiguresStillAnswers(t *testing.T) {
	ctx := context.Background()
	r, run := seedStatsRun(t)
	if _, err := r.db.ExecContext(ctx, `DELETE FROM run_stats`); err != nil {
		t.Fatal(err)
	}
	got, err := r.LanguageBindings(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	live, _ := r.computeLanguageBindings(ctx, run)
	if !reflect.DeepEqual(got, live) || got["go"].Total != 3 {
		t.Errorf("LanguageBindings = %+v, want the live figures with 3 references", got)
	}
}
