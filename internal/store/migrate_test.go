package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func fixedClock() func() int64 { return func() int64 { return 1_700_000_000 } }

func TestLoadMigrationsOrderedAndChecksummed(t *testing.T) {
	ms, err := LoadMigrations()
	if err != nil {
		t.Fatalf("LoadMigrations: %v", err)
	}
	if len(ms) == 0 {
		t.Fatal("no migrations embedded — embed directive broken")
	}
	for i, m := range ms {
		if i > 0 && m.Version <= ms[i-1].Version {
			t.Fatalf("migrations out of order: %d after %d", m.Version, ms[i-1].Version)
		}
		if m.Checksum == "" || len(m.Checksum) != 64 {
			t.Fatalf("migration %d: bad checksum %q", m.Version, m.Checksum)
		}
		if m.SQL == "" {
			t.Fatalf("migration %d: empty SQL", m.Version)
		}
	}
}

func TestMigrateFreshInstall(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	applied, err := Migrate(ctx, db.DB, fixedClock())
	if err != nil {
		t.Fatalf("fresh migrate: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("fresh install applied nothing")
	}

	v, err := SchemaVersion(ctx, db.DB)
	if err != nil {
		t.Fatal(err)
	}
	if v != applied[len(applied)-1] {
		t.Fatalf("SchemaVersion = %d, want %d", v, applied[len(applied)-1])
	}

	for _, table := range []string{"index_runs", "files", "symbols", "edges", "files_fts", "files_fts_map"} {
		var n int
		if err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM sqlite_master WHERE name = ?`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			t.Errorf("table %q missing after migration", table)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	first, err := Migrate(ctx, db.DB, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Migrate(ctx, db.DB, fixedClock())
	if err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("second migrate applied %v, want none", second)
	}
	if len(first) == 0 {
		t.Fatal("first migrate applied nothing")
	}
}

// TestMigrateDetectsEditedMigration guards a subtle, high-cost failure: someone
// edits a shipped migration, and two installs silently end up with different
// schemas.
func TestMigrateDetectsEditedMigration(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := Migrate(ctx, db.DB, fixedClock()); err != nil {
		t.Fatal(err)
	}
	// Simulate an edit by corrupting the recorded checksum.
	if _, err := db.ExecContext(ctx,
		`UPDATE schema_migrations SET checksum = 'deadbeef' WHERE version = 1`); err != nil {
		t.Fatal(err)
	}

	_, err = Migrate(ctx, db.DB, fixedClock())
	if err == nil {
		t.Fatal("expected checksum mismatch to be rejected")
	}
	t.Logf("correctly rejected: %v", err)
}

// TestMigrateInterrupted proves an interrupted migration leaves no partial
// schema. Each migration is transactional, so a failure must roll back
// completely and re-running must succeed.
func TestMigrateInterrupted(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "index.db")

	db, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}

	// Force failure partway: pre-create a table that the schema also creates,
	// so the migration errors mid-statement.
	if _, err := db.ExecContext(ctx, `CREATE TABLE symbols (bogus INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(ctx, db.DB, fixedClock()); err == nil {
		t.Fatal("expected migration to fail against a conflicting table")
	}

	// Nothing should have been recorded, and the partial schema must be absent.
	v, err := SchemaVersion(ctx, db.DB)
	if err != nil {
		t.Fatal(err)
	}
	if v != 0 {
		t.Fatalf("schema version = %d after failed migration, want 0", v)
	}
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE name = 'index_runs'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("index_runs survived a rolled-back migration — transaction boundary is wrong")
	}

	// Remove the conflict; a retry must now fully succeed.
	if _, err := db.ExecContext(ctx, `DROP TABLE symbols`); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(ctx, db.DB, fixedClock()); err != nil {
		t.Fatalf("retry after interruption failed: %v", err)
	}
	if v, _ := SchemaVersion(ctx, db.DB); v == 0 {
		t.Fatal("retry did not apply migrations")
	}
	db.Close()
}

func TestSchemaVersionOnVirginDB(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	v, err := SchemaVersion(ctx, db.DB)
	if err != nil {
		t.Fatalf("SchemaVersion on virgin db: %v", err)
	}
	if v != 0 {
		t.Fatalf("virgin schema version = %d, want 0", v)
	}
}

func TestVerifyFTS5(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := VerifyFTS5(ctx, db.DB); err != nil {
		t.Fatalf("FTS5 unavailable — the search layer cannot work: %v", err)
	}
}

// TestExactRequiresCompilerSource proves the SQL CHECK enforces the same
// invariant as model.Edge.Validate. Defence in depth: a buggy writer must not
// be able to record a guess as compiler-proven.
func TestExactRequiresCompilerSource(t *testing.T) {
	ctx := context.Background()
	db, err := OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	seedRunFileSymbols(t, db.DB)

	_, err = db.ExecContext(ctx, `
		INSERT INTO edges(index_run_id, src_id, dst_id, kind, confidence, source)
		VALUES(1, 1, 2, 'CALLS', 'exact', 'inferred')`)
	if err == nil {
		t.Fatal("SQL CHECK allowed confidence=exact with source=inferred")
	}
	t.Logf("correctly rejected by CHECK: %v", err)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO edges(index_run_id, src_id, dst_id, kind, confidence, source)
		VALUES(1, 1, 2, 'CALLS', 'exact', 'compiler')`); err != nil {
		t.Fatalf("legitimate exact edge rejected: %v", err)
	}
}

func TestForeignKeysCascade(t *testing.T) {
	ctx := context.Background()
	db, err := OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	seedRunFileSymbols(t, db.DB)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO edges(index_run_id, src_id, dst_id, kind, confidence, source)
		VALUES(1, 1, 2, 'CALLS', 'high', 'source_code')`); err != nil {
		t.Fatal(err)
	}

	// Deleting the run must cascade to files, symbols and edges. Without this
	// a discarded staging run would leak rows into every later query.
	if _, err := db.ExecContext(ctx, `DELETE FROM index_runs WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"files", "symbols", "edges"} {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s has %d orphaned rows after run delete; cascade is broken", table, n)
		}
	}
}

func seedRunFileSymbols(t *testing.T, db *sql.DB) {
	t.Helper()
	stmts := []string{
		`INSERT INTO index_runs(id, status, started_unix) VALUES(1, 'indexing', 0)`,
		`INSERT INTO files(id, index_run_id, path, content_hash) VALUES(1, 1, 'a.go', 'h1')`,
		`INSERT INTO symbols(id, index_run_id, file_id, kind, name, fqn, confidence, source)
		 VALUES(1, 1, 1, 'function', 'A', 'pkg.A', 'high', 'source_code')`,
		`INSERT INTO symbols(id, index_run_id, file_id, kind, name, fqn, confidence, source)
		 VALUES(2, 1, 1, 'function', 'B', 'pkg.B', 'high', 'source_code')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed %.40q: %v", s, err)
		}
	}
}

// withNextMigration makes this process carry one migration more than the
// build ships, numbered after the newest embedded one, for the rest of the
// test. It returns the new version.
func withNextMigration(t *testing.T, name, sql string) int {
	t.Helper()
	latest, err := LatestVersion()
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{}
	embedded := EmbeddedMigrations()
	entries, err := fs.ReadDir(embedded, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		body, err := fs.ReadFile(embedded, e.Name())
		if err != nil {
			t.Fatal(err)
		}
		fsys[e.Name()] = &fstest.MapFile{Data: body}
	}
	next := latest + 1
	fsys[fmt.Sprintf("%04d_%s.sql", next, name)] = &fstest.MapFile{Data: []byte(sql)}
	t.Cleanup(WithMigrationsForTesting(fsys))
	return next
}

// TestMigrateUpgradesAnExistingIndex is the upgrade every user makes when a
// release adds a migration: a published index written by the previous build,
// opened by one that carries a new migration. The existing run must survive,
// a new column must read as its default on old rows, a CHECK must arrive with
// its column, a new table's foreign keys must cascade from rows that already
// existed, and a second Migrate must be a no-op.
func TestMigrateUpgradesAnExistingIndex(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := Migrate(ctx, db.DB, fixedClock()); err != nil {
		t.Fatal(err)
	}
	shipped, err := SchemaVersion(ctx, db.DB)
	if err != nil {
		t.Fatal(err)
	}
	seedRunFileSymbols(t, db.DB)
	if _, err := db.ExecContext(ctx,
		`UPDATE index_runs SET status='ready', committed=1, files_indexed=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}

	next := withNextMigration(t, "upgrade_probe", `
ALTER TABLE index_runs ADD COLUMN probe_state TEXT NOT NULL DEFAULT 'none'
    CHECK (probe_state IN ('none','some'));
CREATE TABLE probe_rows (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    index_run_id INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    symbol_id    INTEGER REFERENCES symbols(id) ON DELETE CASCADE
);
CREATE INDEX idx_probe_rows_symbol ON probe_rows (symbol_id);
`)
	if next != shipped+1 {
		t.Fatalf("next migration is v%d, want v%d", next, shipped+1)
	}

	// Read paths see the index as outdated until it is migrated.
	var outdated *ErrSchemaOutdated
	if err := CheckVersion(ctx, db.DB); !errors.As(err, &outdated) {
		t.Fatalf("CheckVersion before upgrade = %v, want ErrSchemaOutdated", err)
	}

	applied, err := Migrate(ctx, db.DB, fixedClock())
	if err != nil {
		t.Fatalf("upgrade an existing index: %v", err)
	}
	if len(applied) != 1 || applied[0] != next {
		t.Fatalf("applied %v, want [%d]", applied, next)
	}
	if v, err := SchemaVersion(ctx, db.DB); err != nil || v != next {
		t.Fatalf("schema version %d (%v), want %d", v, err, next)
	}
	if err := CheckVersion(ctx, db.DB); err != nil {
		t.Fatalf("CheckVersion after upgrade: %v", err)
	}

	// The published run is still published, and reads the new column's default.
	var status, state string
	var committed, indexed int64
	if err := db.QueryRowContext(ctx, `
		SELECT status, committed, files_indexed, probe_state FROM index_runs WHERE id = 1`).
		Scan(&status, &committed, &indexed, &state); err != nil {
		t.Fatalf("read upgraded run: %v", err)
	}
	if status != "ready" || committed != 1 || indexed != 1 {
		t.Fatalf("upgrade disturbed the published run: status=%q committed=%d indexed=%d",
			status, committed, indexed)
	}
	if state != "none" {
		t.Errorf("probe_state on an upgraded run = %q, want the default %q", state, "none")
	}

	// The CHECK arrived with the column, not merely the column.
	if _, err := db.ExecContext(ctx,
		`UPDATE index_runs SET probe_state = 'mostly' WHERE id = 1`); err == nil {
		t.Error("probe_state accepted a value outside its CHECK; the CHECK did not survive ADD COLUMN")
	}

	// The new table's foreign keys cascade from rows written before it existed.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO probe_rows(index_run_id, symbol_id) VALUES(1, 1)`); err != nil {
		t.Fatalf("new table unusable after upgrade: %v", err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM index_runs WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	var orphans int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM probe_rows`).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Errorf("probe_rows left %d orphaned rows after the run was deleted", orphans)
	}

	if again, err := Migrate(ctx, db.DB, fixedClock()); err != nil || len(again) != 0 {
		t.Errorf("re-running Migrate applied %v (%v); want nothing", again, err)
	}
}

// TestMigrateRefusesANewerIndex: a build that knows fewer migrations than the
// index records must leave the index exactly as found.
func TestMigrateRefusesANewerIndex(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	next := withNextMigration(t, "future", `CREATE TABLE future_rows (id INTEGER PRIMARY KEY);`)
	if _, err := Migrate(ctx, db.DB, fixedClock()); err != nil {
		t.Fatal(err)
	}
	// Back to the shipped migrations: this build is now older than the index.
	t.Cleanup(WithMigrationsForTesting(EmbeddedMigrations()))

	var newer *ErrSchemaNewer
	if _, err := Migrate(ctx, db.DB, fixedClock()); !errors.As(err, &newer) {
		t.Fatalf("Migrate on a newer index = %v, want ErrSchemaNewer", err)
	}
	if v, _ := SchemaVersion(ctx, db.DB); v != next {
		t.Fatalf("schema version changed to %d, want %d left as found", v, next)
	}
}
