package cli

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/sutrakam/graphlume/internal/store"
)

// shipNextMigration makes the rest of the test run as the next release of
// graphlume: the migrations this build ships plus one more. An index written
// before the call is then exactly what an upgrading user has: a real index at
// the previous schema version, not a fresh or hand-rewound database.
//
// The extra migration adds a column with a CHECK, a table with foreign keys
// and an index, the shapes a real migration uses. It returns the new version.
func shipNextMigration(t *testing.T) int {
	t.Helper()
	latest, err := store.LatestVersion()
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{}
	shipped := store.EmbeddedMigrations()
	entries, err := fs.ReadDir(shipped, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		body, err := fs.ReadFile(shipped, e.Name())
		if err != nil {
			t.Fatal(err)
		}
		fsys[e.Name()] = &fstest.MapFile{Data: body}
	}
	next := latest + 1
	fsys[fmt.Sprintf("%04d_upgrade_probe.sql", next)] = &fstest.MapFile{Data: []byte(`
ALTER TABLE index_runs ADD COLUMN upgrade_probe INTEGER NOT NULL DEFAULT 0
    CHECK (upgrade_probe IN (0,1));
CREATE TABLE upgrade_probe_rows (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    index_run_id INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    symbol_id    INTEGER REFERENCES symbols(id) ON DELETE CASCADE
);
CREATE INDEX idx_upgrade_probe_rows_symbol ON upgrade_probe_rows (symbol_id);
`)}
	t.Cleanup(store.WithMigrationsForTesting(fsys))
	return next
}

func schemaVersionOf(t *testing.T, dbPath string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var v int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestUpgradeAppliesPendingMigrations pins the upgrade path.
//
// If only `graphlume init` migrated, every other command would open the
// database and use it as-is, so upgrading the binary would leave an existing
// user on their old schema permanently, and a query expecting a newer table
// would fail with a raw driver error from deep inside it. Every user who keeps
// the tool longer than one release goes through this path.
func TestUpgradeAppliesPendingMigrations(t *testing.T) {
	root := isolate(t)

	if err := os.WriteFile(filepath.Join(root, "a.go"),
		[]byte("package a\n\nfunc A() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"index"}} {
		if out, err := run(t, root, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}

	dbPath := filepath.Join(root, ".graphlume", "index.db")
	previous := schemaVersionOf(t, dbPath)
	next := shipNextMigration(t)
	if next <= previous {
		t.Fatalf("next migration v%d is not newer than the index's v%d", next, previous)
	}

	// A writable command must upgrade in place.
	if out, err := run(t, root, "index"); err != nil {
		t.Fatalf("index on an outdated schema failed: %v\n%s", err, out)
	}
	if got := schemaVersionOf(t, dbPath); got != next {
		t.Fatalf("index did not upgrade the schema: still v%d, wanted v%d", got, next)
	}
	// The upgrade kept the published run and gave it the new column's default.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var runs, probe int
	if err := db.QueryRow(`SELECT count(*), COALESCE(MAX(upgrade_probe), -1) FROM index_runs WHERE committed = 1`).
		Scan(&runs, &probe); err != nil {
		t.Fatalf("read upgraded run: %v", err)
	}
	if runs != 1 || probe != 0 {
		t.Errorf("after upgrade: %d committed runs, upgrade_probe=%d; want 1 run reading 0", runs, probe)
	}
	if out, err := run(t, root, "symbol", "A"); err != nil {
		t.Fatalf("query after upgrade failed: %v\n%s", err, out)
	}
}

// TestOutdatedSchemaIsReportedNotCrashed pins the read-only path.
//
// Queries open the database read-only and so cannot migrate. Without the check, they
// would fail wherever the missing object is first touched, with a message naming a
// table the user has never heard of and no indication that the fix is one
// command. The mismatch is asserted at open.
func TestOutdatedSchemaIsReportedNotCrashed(t *testing.T) {
	root := isolate(t)

	if err := os.WriteFile(filepath.Join(root, "a.go"),
		[]byte("package a\n\nfunc A() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"index"}} {
		if out, err := run(t, root, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	shipNextMigration(t)

	out, err := run(t, root, "symbol", "A")
	if err == nil {
		t.Fatalf("a query against an outdated schema succeeded; expected a refusal:\n%s", out)
	}
	// The message has to name the fix, not just the problem.
	if !strings.Contains(err.Error()+out, "graphlume index") {
		t.Errorf("error does not tell the user how to fix it: %v\n%s", err, out)
	}

	// status is what someone runs to diagnose, so it must still work and say
	// what is wrong rather than refusing alongside everything else.
	st, err := run(t, root, "status")
	if err != nil {
		t.Fatalf("status refused to run on an outdated index: %v\n%s", err, st)
	}
	if !strings.Contains(st, "OUTDATED") {
		t.Errorf("status did not report the outdated schema:\n%s", st)
	}
}

func TestMigrateCommandUpgradesOutdatedIndex(t *testing.T) {
	root := isolate(t)
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc A() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{{"init"}, {"index"}} {
		if out, err := run(t, root, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	shipNextMigration(t)

	// Verify symbol query refuses because of outdated schema
	if _, err := run(t, root, "symbol", "A"); err == nil {
		t.Fatal("query succeeded on outdated schema")
	}

	// Run migrate command to bring index schema up to date
	out, err := run(t, root, "migrate")
	if err != nil {
		t.Fatalf("migrate failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "upgraded index schema") {
		t.Errorf("migrate output missing expected message: %s", out)
	}

	// Query should now succeed
	if out, err = run(t, root, "symbol", "A"); err != nil {
		t.Fatalf("symbol query failed after migrate: %v\n%s", err, out)
	}
}
