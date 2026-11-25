package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInitWorkspaceDBRecordsVersionAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "workspace.db")
	for i := 0; i < 2; i++ {
		db, err := InitWorkspaceDB(ctx, path)
		if err != nil {
			t.Fatalf("init #%d: %v", i, err)
		}
		v, err := workspaceVersion(ctx, db)
		if err != nil || v != WorkspaceSchemaVersion {
			t.Fatalf("version = %d, %v; want %d", v, err, WorkspaceSchemaVersion)
		}
		_ = db.Close()
	}
}

func TestInitWorkspaceDBRefusesNewerVersion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "workspace.db")
	db, err := InitWorkspaceDB(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE schema_version SET version = ?`, WorkspaceSchemaVersion+1); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	_, err = InitWorkspaceDB(ctx, path)
	var newer *ErrWorkspaceSchemaNewer
	if !errors.As(err, &newer) || newer.Found != WorkspaceSchemaVersion+1 {
		t.Fatalf("err = %v, want ErrWorkspaceSchemaNewer", err)
	}
	if _, err := OpenWorkspaceDBReadOnly(ctx, path); !errors.As(err, &newer) {
		t.Fatalf("read-only err = %v, want ErrWorkspaceSchemaNewer", err)
	}
}

// TestInitWorkspaceDBMigratesUnversionedV1 builds the original schema (no
// schema_version table, no links_truncated column) and checks it is upgraded
// in place with its rows intact.
func TestInitWorkspaceDBMigratesUnversionedV1(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "workspace.db")

	raw, err := Open(ctx, Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	v1 := `
CREATE TABLE workspace_services (
    service_id TEXT PRIMARY KEY, rel_path TEXT NOT NULL UNIQUE,
    role TEXT NOT NULL, db_path TEXT NOT NULL, committed_run_id INTEGER NOT NULL,
    mutation_seq INTEGER NOT NULL, schema_version INTEGER NOT NULL,
    digest TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE exported_contracts (
    id INTEGER PRIMARY KEY AUTOINCREMENT, service_id TEXT NOT NULL,
    direction TEXT NOT NULL, contract_kind TEXT NOT NULL, canonical_key TEXT NOT NULL,
    symbol_fqn TEXT NOT NULL, file_path TEXT NOT NULL, line INTEGER NOT NULL,
    confidence TEXT NOT NULL, source TEXT NOT NULL);
INSERT INTO exported_contracts (service_id, direction, contract_kind, canonical_key, symbol_fqn, file_path, line, confidence, source)
  VALUES ('a','consumes','http_route','POST /v1/pay/charge','s','f',1,'high','x'),
         ('a','writes','queue_topic','kafka:Pay.Done','s','f',1,'high','x');
`
	if _, err := raw.ExecContext(ctx, v1); err != nil {
		t.Fatalf("build v1: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO workspace_services VALUES ('a','a','service','/a.db',1,0,1,'d','t')`); err != nil {
		t.Fatal(err)
	}
	if v, _ := workspaceVersion(ctx, raw.DB); v != 1 {
		t.Fatalf("unversioned db read as v%d, want v1", v)
	}
	_ = raw.Close()

	if _, err := OpenWorkspaceDBReadOnly(ctx, path); err == nil {
		t.Fatal("read-only open of an old schema must refuse, not migrate")
	} else {
		var old *ErrWorkspaceSchemaOutdated
		if !errors.As(err, &old) {
			t.Fatalf("err = %v, want ErrWorkspaceSchemaOutdated", err)
		}
	}

	db, err := InitWorkspaceDB(ctx, path)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer db.Close()
	var key string
	var segs int
	if err := db.QueryRowContext(ctx, `SELECT seg_count FROM exported_contracts WHERE contract_kind = 'http_route'`).Scan(&segs); err != nil || segs != 3 {
		t.Errorf("backfilled seg_count = %d, %v; want 3", segs, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT match_key FROM exported_contracts WHERE contract_kind = 'queue_topic'`).Scan(&key); err != nil || key != "pay.done" {
		t.Errorf("backfilled match_key = %q, %v; want pay.done", key, err)
	}
	var trunc int
	if err := db.QueryRowContext(ctx, `SELECT links_truncated FROM workspace_services WHERE service_id = 'a'`).Scan(&trunc); err != nil {
		t.Fatalf("row lost or column missing: %v", err)
	}
	if v, _ := workspaceVersion(ctx, db.DB); v != WorkspaceSchemaVersion {
		t.Errorf("version after migrate = %d", v)
	}
}

func TestOpenWorkspaceDBReadOnlyDoesNotCreateAndRejectsWrites(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope", "workspace.db")
	if _, err := OpenWorkspaceDBReadOnly(ctx, missing); err == nil {
		t.Fatal("missing file must be an error")
	}
	if _, err := os.Stat(filepath.Dir(missing)); err == nil {
		t.Fatal("a query-path open created the parent directory")
	}

	path := filepath.Join(dir, "workspace.db")
	w, err := InitWorkspaceDB(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	ro, err := OpenWorkspaceDBReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if _, err := ro.ExecContext(ctx, `DELETE FROM workspace_services`); err == nil {
		t.Fatal("write through a read-only handle succeeded")
	}
}
