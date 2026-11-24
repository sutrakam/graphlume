package cli

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/store"
)

func schemaOf(t *testing.T, dbPath string) map[string]string {
	t.Helper()
	db, err := store.Open(context.Background(), store.Options{Path: dbPath, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	rows, err := db.QueryContext(context.Background(),
		`SELECT name, sql FROM sqlite_master WHERE type IN ('index','trigger') AND sql IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var n, s string
		if err := rows.Scan(&n, &s); err != nil {
			t.Fatal(err)
		}
		out[n] = s
	}
	return out
}

// TestAnInterruptedFirstIndexIsReportedAndThenRepaired: a first index sets
// indexes and triggers aside while it loads. If the process dies there, status
// and doctor must say so, and the next `index` must put everything back.
func TestAnInterruptedFirstIndexIsReportedAndThenRepaired(t *testing.T) {
	root := seedMixedRepo(t)
	if out, err := run(t, root, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	dbPath := filepath.Join(root, IndexDir, IndexFile)
	whole := schemaOf(t, dbPath)

	// The state a killed first index leaves behind: objects dropped and
	// recorded, nothing committed.
	db, err := store.Open(context.Background(), store.Options{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.New(db).DeferBulkObjects(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if reflect.DeepEqual(schemaOf(t, dbPath), whole) {
		t.Fatal("setup did not drop anything")
	}

	status, err := run(t, root, "status")
	if err != nil || !strings.Contains(oneLine(status), "idx_refs_name_binding") ||
		!strings.Contains(oneLine(status), "Run 'graphlume index' to restore") {
		t.Errorf("status does not report the missing schema objects (err %v):\n%s", err, status)
	}
	doctor, err := run(t, root, "doctor")
	if err != nil || !strings.Contains(oneLine(doctor), "schema objects") ||
		!strings.Contains(oneLine(doctor), "idx_refs_name_binding") {
		t.Errorf("doctor does not report the missing schema objects (err %v):\n%s", err, doctor)
	}

	if out, err := run(t, root, "index"); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}
	if after := schemaOf(t, dbPath); !reflect.DeepEqual(after, whole) {
		t.Error("the index after the repairing run does not have the schema of a fresh one")
	}
	status, err = run(t, root, "status")
	if err != nil || strings.Contains(status, "restore") {
		t.Errorf("status still reports missing objects after the repair (err %v):\n%s", err, status)
	}
}

// TestAFirstIndexLeavesTheWholeSchema: the bulk load is invisible afterwards.
func TestAFirstIndexLeavesTheWholeSchema(t *testing.T) {
	root := seedMixedRepo(t)
	if out, err := run(t, root, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	dbPath := filepath.Join(root, IndexDir, IndexFile)
	whole := schemaOf(t, dbPath)
	if out, err := run(t, root, "index"); err != nil {
		t.Fatalf("index: %v\n%s", err, out)
	}
	if after := schemaOf(t, dbPath); !reflect.DeepEqual(after, whole) {
		t.Error("a first index changed the set of indexes and triggers")
	}
}
