package registry

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sutrakam/graphlume/internal/store"
)

// The plan gate for AccuracyAdjudication's CTE.
//
// It lives beside the query, so the
// gate can see it: a plan test that cannot see the string it is about is a
// plan test about nothing.
//
// Without the indexes the accuracy report takes twenty minutes on a 91,548-symbol repository and
// four seconds with them. The obvious guard — run it in CI and
// assert a wall-clock budget — is a guard that CANNOT FAIL: CI runs against
// this repository's own 425 files, where the slow version is also fast. The
// slowdown only exists at scale.
//
// So the gate is the query PLAN, which is size-independent. It fails on a
// laptop, on a fixture, and in CI the moment either half is missing:
// the indexes from migration 0001, or the join order that lets them be used.
//
// What goes wrong without them, so the assertions below read as more than magic strings:
//
//   - edges(evidence_file, evidence_line) without an index means adjudicating a call
//     site against the compiler's edge at the same file and line scans every
//     edge in the run.
//   - A CTE that builds every parsed call site (305,237 rows) and joins it to the
//     compiler-adjudicated set (5,587). SQLite cannot index a CTE, so the plan
//     is a nested loop over the product — `SCAN c` then `SCAN u`.

// planFor returns EXPLAIN QUERY PLAN output for the accuracy query, one step
// per line.
func planFor(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	db, err := store.Open(ctx, store.Options{Path: filepath.Join(t.TempDir(), "plan.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := store.Migrate(ctx, db.DB, time.Now().Unix); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+accuracyCTE+
		" SELECT lang, correct, any_rank, hit_rank FROM scored", 1, 1, 1, 1)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var b strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		b.WriteString(detail)
		b.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}
	if b.Len() == 0 {
		t.Fatal("empty query plan; this test is not checking anything")
	}
	return b.String()
}

// TestAccuracyQueryUsesItsIndexes fails if idx_edges_evidence or idx_refs_file_line
// is dropped or renamed out from under the query.
func TestAccuracyQueryUsesItsIndexes(t *testing.T) {
	plan := planFor(t)
	for _, idx := range []string{"idx_edges_evidence", "idx_refs_file_line"} {
		if !strings.Contains(plan, idx) {
			t.Errorf("the accuracy query no longer uses %s.\n"+
				"Without it, adjudicating one call site scans every edge or every "+
				"reference in the run — 160s for a single query on a 91,548-symbol "+
				"repository.\nPlan was:\n%s", idx, plan)
		}
	}
}

// TestAccuracyQueryDoesNotNestLoopTheBigSide fails if the join is inverted back.
//
// The specific shape that cost twenty minutes: two CTEs, neither indexable,
// joined by scanning both. SQLite reports that as a bare `SCAN <cte>` with no
// USING INDEX. A `SCAN` of a small materialised subquery is fine; a SCAN paired
// with another SCAN over the product is not, which is why this asserts on the
// count rather than on any single line.
func TestAccuracyQueryDoesNotNestLoopTheBigSide(t *testing.T) {
	plan := planFor(t)

	var bare []string
	for _, line := range strings.Split(strings.TrimSpace(plan), "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "SCAN") && !strings.Contains(l, "USING") {
			bare = append(bare, l)
		}
	}
	// One unindexed scan is the outer driver and is expected. Two means the
	// planner is walking the product of two relations it cannot index.
	if len(bare) > 1 {
		t.Errorf("the accuracy query nest-loops %d unindexed relations:\n  %s\n\n"+
			"This is the shape that took 160s per query: a CTE of every parsed call "+
			"site joined to the compiler-adjudicated set, neither indexable. Drive "+
			"from the SMALL side and probe the large one through an index.\n"+
			"Full plan:\n%s", len(bare), strings.Join(bare, "\n  "), plan)
	}
}
