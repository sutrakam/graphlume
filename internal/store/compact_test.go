package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fillAndFree writes about mb MiB into a scratch table and deletes it, which
// leaves that much of the file on the free list.
func fillAndFree(t *testing.T, db *DB, mb int) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS scratch(b BLOB)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM c WHERE i < ?)
		INSERT INTO scratch SELECT randomblob(1024) FROM c`, mb*1024); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM scratch`); err != nil {
		t.Fatal(err)
	}
}

func openMigrated(t *testing.T, path string) *DB {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := Migrate(ctx, db.DB, func() int64 { return 0 }); err != nil {
		t.Fatal(err)
	}
	return db
}

// The mode can only be chosen before the file's first page is written, and
// Open switches a new file to WAL, which writes it. So the setting has to be
// in the connection string ahead of journal_mode, not in Migrate.
func TestNewDatabaseUsesIncrementalAutoVacuum(t *testing.T) {
	ctx := context.Background()
	db := openMigrated(t, filepath.Join(t.TempDir(), "new.db"))
	fs, err := ReadFreeSpace(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if fs.AutoVacuum != AutoVacuumIncremental {
		t.Fatalf("auto_vacuum = %q, want %q", fs.AutoVacuum, AutoVacuumIncremental)
	}
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q (%v), want wal", mode, err)
	}
}

func TestCompactReclaimsAndShrinksFile(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "idx.db")
	db := openMigrated(t, path)
	fillAndFree(t, db, 8)

	before, err := ReadFreeSpace(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if before.FreeBytes() < 7<<20 {
		t.Fatalf("free space %d, want about 8 MiB", before.FreeBytes())
	}

	res, err := compact(ctx, db, 1<<20, 0.2)
	if err != nil {
		t.Fatal(err)
	}
	if res.ReclaimedBytes != before.FreeBytes() {
		t.Fatalf("reclaimed %d, want %d", res.ReclaimedBytes, before.FreeBytes())
	}
	if res.Checkpoint != "truncate" {
		t.Fatalf("checkpoint = %q, want truncate with no reader", res.Checkpoint)
	}
	after, _ := ReadFreeSpace(ctx, db)
	if after.FreePages != 0 {
		t.Fatalf("free pages after = %d", after.FreePages)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() != after.FileBytes() {
		t.Fatalf("file is %d bytes, database is %d", fi.Size(), after.FileBytes())
	}
	if wal, err := os.Stat(path + "-wal"); err == nil && wal.Size() != 0 {
		t.Fatalf("-wal is %d bytes after a truncate checkpoint", wal.Size())
	}
}

func TestCompactBelowThresholdDoesNothing(t *testing.T) {
	ctx := context.Background()
	db := openMigrated(t, filepath.Join(t.TempDir(), "idx.db"))
	fillAndFree(t, db, 2)
	// 2 MiB is below the real 64 MiB floor.
	res, err := Compact(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if res.ReclaimedBytes != 0 || res.Checkpoint != "" {
		t.Fatalf("Compact = %+v, want nothing below the threshold", res)
	}
	// And the fraction alone holds it back too.
	res, err = compact(ctx, db, 1, 0.99)
	if err != nil || res.ReclaimedBytes != 0 {
		t.Fatalf("compact at 99%% = %+v, %v; want nothing", res, err)
	}
}

func TestWorthReclaimingNeedsBothThresholds(t *testing.T) {
	const page = 4096
	mib := func(n int64) int64 { return n << 20 / page }
	for _, tc := range []struct {
		name        string
		free, total int64
		want        bool
	}{
		{"big and a large share", mib(100), mib(300), true},
		{"exactly at both", mib(64), mib(320), true},
		{"big but a small share", mib(100), mib(1000), false},
		{"a large share but small", mib(10), mib(20), false},
		{"empty", 0, 0, false},
	} {
		f := FreeSpace{AutoVacuum: AutoVacuumIncremental, PageSize: page, FreePages: tc.free, PageCount: tc.total}
		if got := f.WorthReclaiming(); got != tc.want {
			t.Errorf("%s: WorthReclaiming = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A database made without auto_vacuum (a hand-built fixture) keeps working:
// Compact leaves it alone and says nothing.
func TestCompactSkipsDatabaseWithoutAutoVacuum(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "plain.db")
	// Created through a connection string without the auto_vacuum pragma.
	plain, err := openPlain(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.ExecContext(ctx, `CREATE TABLE t(x)`); err != nil {
		t.Fatal(err)
	}
	_ = plain.Close()

	db := openMigrated(t, path)
	fillAndFree(t, db, 4)
	fs, _ := ReadFreeSpace(ctx, db)
	if fs.AutoVacuum != AutoVacuumNone {
		t.Fatalf("auto_vacuum = %q, want none: Open must not change an existing database", fs.AutoVacuum)
	}
	res, err := compact(ctx, db, 1, 0.01)
	if err != nil {
		t.Fatal(err)
	}
	if res.ReclaimedBytes != 0 || res.Checkpoint != "" {
		t.Fatalf("compact on auto_vacuum=none = %+v, want nothing", res)
	}
}

// A reader in the middle of a transaction (the MCP server, the portal) must
// neither fail nor hold the writer for the busy timeout: the checkpoint falls
// back to PASSIVE at once.
func TestCompactWithOpenReader(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "idx.db")
	db := openMigrated(t, path)
	fillAndFree(t, db, 8)

	reader, err := Open(ctx, Options{Path: path, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	tx, err := reader.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	res, err := compact(ctx, db, 1<<20, 0.2)
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("compact waited %s behind a reader", took)
	}
	if res.ReclaimedBytes == 0 || res.Checkpoint != "passive" {
		t.Fatalf("compact with a reader = %+v, want pages reclaimed and a passive checkpoint", res)
	}
	// The reader's snapshot still reads.
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("reader after compact: %v", err)
	}
	_ = tx.Rollback()

	// With the reader gone the next checkpoint truncates the log.
	if mode, err := checkpointWAL(ctx, db); err != nil || mode != "truncate" {
		t.Fatalf("checkpoint after the reader left = %q, %v", mode, err)
	}
}

// openPlain opens path with none of Open's pragmas, the way a test fixture or
// another tool would create a database.
func openPlain(path string) (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
}
