package scale

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// TestTheCoverageFiguresAreReadNotRecomputed covers the figure every
// relationship answer opens with. Computing it reads every reference and every
// edge of the run, which on a million-reference index was most of the seconds a
// `callers` took before anything about the question was looked at. Stored
// when `resolve` finishes, it must cost a fixed handful of small reads whatever
// the size of the repository.
func TestTheCoverageFiguresAreReadNotRecomputed(t *testing.T) {
	f := load(t)
	ctx := context.Background()

	live, err := f.reg.LanguageBindings(ctx, f.runID)
	if err != nil {
		t.Fatal(err)
	}
	liveTotals, err := f.reg.ResolutionStats(ctx, f.runID)
	if err != nil {
		t.Fatal(err)
	}

	if err := f.reg.RefreshRunStats(ctx, f.runID); err != nil {
		t.Fatalf("store the figures: %v", err)
	}

	var samples []time.Duration
	for range 9 {
		m := measure(func() {
			langs, err := f.reg.LanguageBindings(ctx, f.runID)
			if err != nil {
				t.Fatal(err)
			}
			totals, err := f.reg.ResolutionStats(ctx, f.runID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(langs, live) || totals != liveTotals {
				t.Fatalf("the stored figures differ from the live ones:\n stored %+v %+v\n live   %+v %+v",
					langs, totals, live, liveTotals)
			}
		})
		samples = append(samples, m.elapsed)
	}
	// No term for the size of the repository: a few indexed reads of a table
	// with one row per language. The every-tool budget's fixed term is 1.5s;
	// this is a small fraction of it, so a regression to a scan of the
	// reference table cannot hide inside the budget.
	requireWithin(t, "stored coverage figures", measurement{elapsed: p95(samples)},
		budget{fixed: 100 * time.Millisecond}, f.info.Symbols)
}
