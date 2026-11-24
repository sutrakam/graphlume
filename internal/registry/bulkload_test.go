package registry

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/pkg/model"
)

// schemaObjects returns every index and trigger of the database as
// name -> CREATE statement, the way SQLite stores it.
func schemaObjects(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(),
		`SELECT name, sql FROM sqlite_master WHERE type IN ('index','trigger') AND sql IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var name, stmt string
		if err := rows.Scan(&name, &stmt); err != nil {
			t.Fatal(err)
		}
		out[name] = stmt
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDeferAndRestoreReturnTheSchemaExactly(t *testing.T) {
	ctx := context.Background()
	r, db := newTestRegistry(t)
	before := schemaObjects(t, db.DB)

	ok, err := r.BulkLoadEligible(ctx)
	if err != nil || !ok {
		t.Fatalf("an empty index must be eligible for a bulk load: ok=%v err=%v", ok, err)
	}
	n, err := r.DeferBulkObjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n < len(deferrableIndexes) {
		t.Fatalf("deferred %d objects, want at least the %d indexes", n, len(deferrableIndexes))
	}

	during := schemaObjects(t, db.DB)
	for _, name := range deferrableIndexes {
		if _, still := during[name]; still {
			t.Errorf("index %s is still present after it was deferred", name)
		}
	}
	for name := range during {
		if deferrableTrigger(name) {
			t.Errorf("trigger %s is still present after it was deferred", name)
		}
	}
	listed, err := r.DeferredObjects(ctx)
	if err != nil || len(listed) != n {
		t.Fatalf("DeferredObjects = %v, %v; want %d objects", listed, err, n)
	}

	restored, err := r.RestoreDeferred(ctx)
	if err != nil || restored != n {
		t.Fatalf("RestoreDeferred = %d, %v; want %d", restored, err, n)
	}
	if after := schemaObjects(t, db.DB); !reflect.DeepEqual(after, before) {
		t.Errorf("the schema after a restore differs from the schema before the defer")
		for name, stmt := range before {
			if after[name] != stmt {
				t.Errorf("  %s: before %q, after %q", name, stmt, after[name])
			}
		}
	}
	if left, _ := r.DeferredObjects(ctx); len(left) != 0 {
		t.Errorf("objects still recorded as deferred after a restore: %v", left)
	}
	// A second restore has nothing to do and says so.
	if again, err := r.RestoreDeferred(ctx); err != nil || again != 0 {
		t.Errorf("a second RestoreDeferred = %d, %v; want 0, nil", again, err)
	}
}

// TestAnInterruptedLoadIsRepairedByTheNextIndex is the failure the design must
// survive: the process dies after the drop and before the restore. Nothing but
// the database file survives, and the next index (which begins with
// ReclaimAbandoned) must put every object back.
func TestAnInterruptedLoadIsRepairedByTheNextIndex(t *testing.T) {
	ctx := context.Background()
	r, db := newTestRegistry(t)
	before := schemaObjects(t, db.DB)

	run, err := r.BeginRun(ctx, "auto", "rev")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DeferBulkObjects(ctx); err != nil {
		t.Fatal(err)
	}
	w, err := r.NewWriter(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddFile(ctx, &model.File{Path: "a.go", Language: "go", ContentHash: "h", ParseStatus: "ok"}); err != nil {
		t.Fatal(err)
	}
	// "Crash": the batch in flight is lost with the process (closing the
	// writer rolls it back, which is what SQLite does to a dead process's open
	// transaction), and a new process is a new Registry over the same database.
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	next := New(db)
	if deferred, err := next.DeferredObjects(ctx); err != nil || len(deferred) == 0 {
		t.Fatalf("a new process must see what the dead one left set aside: %v, %v", deferred, err)
	}

	if _, err := next.ReclaimAbandoned(ctx); err != nil {
		t.Fatal(err)
	}
	if after := schemaObjects(t, db.DB); !reflect.DeepEqual(after, before) {
		t.Error("ReclaimAbandoned did not restore the schema an interrupted load left incomplete")
	}
	if deferred, _ := next.DeferredObjects(ctx); len(deferred) != 0 {
		t.Errorf("still deferred after the next index's recovery: %v", deferred)
	}
}

func TestAbortingABulkRunRestoresTheSchema(t *testing.T) {
	ctx := context.Background()
	r, db := newTestRegistry(t)
	before := schemaObjects(t, db.DB)

	run, err := r.BeginRun(ctx, "auto", "rev")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DeferBulkObjects(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.AbortRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if after := schemaObjects(t, db.DB); !reflect.DeepEqual(after, before) {
		t.Error("AbortRun left the schema without the objects the bulk load set aside")
	}
}

// TestAnIndexSomeoneMayBeReadingIsNeverStripped: once a revision is published
// a later full index replaces it while readers use it, so nothing may be
// dropped from under them.
func TestAnIndexSomeoneMayBeReadingIsNeverStripped(t *testing.T) {
	ctx := context.Background()
	r, run := seedStatsRun(t)
	if err := r.CommitRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	ok, err := r.BulkLoadEligible(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("an index with a committed run was eligible for a bulk load")
	}
}

// TestRestoredIndexesAnswerFromTheIndex: the plan a relationship answer relies
// on is back after a restore, not merely a schema that looks right.
func TestRestoredIndexesAnswerFromTheIndex(t *testing.T) {
	ctx := context.Background()
	r, db := newTestRegistry(t)
	if _, err := r.DeferBulkObjects(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RestoreDeferred(ctx); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join(queryPlan(t, db,
		`SELECT resolution, resolved_symbol_id FROM symbol_references WHERE index_run_id = ? AND name = ?`, 1, "x"), " | ")
	if !strings.Contains(plan, "idx_refs_name_binding") {
		t.Errorf("name-binding read does not use idx_refs_name_binding after a restore: %s", plan)
	}
}
