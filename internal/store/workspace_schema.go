package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
)

// WorkspaceSchemaVersion is the workspace.db schema this build writes.
//
// v1 is the original schema, which recorded no version at all: a database with
// workspace tables and no schema_version row is v1. v2 adds
// workspace_services.links_truncated (a service whose contract links were
// capped says so instead of looking complete) and the exported_contracts
// match_key / seg_count columns, which let link recomputation find a
// service's counterparts with an indexed join instead of scanning every
// contract in the workspace.
const WorkspaceSchemaVersion = 2

// WorkspaceSchemaDDL defines the SQLite schema for .graphlume/workspace.db.
//
// It carries no PRAGMAs. A pragma issued through Exec applies to one pooled
// connection only, and the DSN already sets foreign_keys on every connection
// (see connectionPragmas); repeating it here would look like it mattered while
// configuring one connection of eight.
const WorkspaceSchemaDDL = `
CREATE TABLE IF NOT EXISTS workspace_services (
    service_id        TEXT PRIMARY KEY,
    rel_path          TEXT NOT NULL UNIQUE,
    role              TEXT NOT NULL CHECK (role IN ('service', 'shared_lib', 'gateway', 'infra')),
    db_path           TEXT NOT NULL,
    committed_run_id  INTEGER NOT NULL,
    mutation_seq      INTEGER NOT NULL,
    schema_version    INTEGER NOT NULL,
    digest            TEXT NOT NULL,
    updated_at        TEXT NOT NULL,
    links_truncated   INTEGER NOT NULL DEFAULT 0,
    run_started_unix  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS exported_contracts (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    service_id        TEXT NOT NULL REFERENCES workspace_services(service_id) ON DELETE CASCADE,
    direction         TEXT NOT NULL CHECK (direction IN ('provides', 'consumes', 'reads', 'writes')),
    contract_kind     TEXT NOT NULL CHECK (contract_kind IN (
                          'http_route', 'grpc_method', 'graphql_op',
                          'queue_topic', 'db_table', 'cache_key',
                          'config_key', 'shared_pkg'
                      )),
    canonical_key     TEXT NOT NULL,
    symbol_fqn        TEXT NOT NULL,
    file_path         TEXT NOT NULL,
    line              INTEGER NOT NULL,
    confidence        TEXT NOT NULL CHECK (confidence IN ('exact', 'high', 'medium', 'low')),
    source            TEXT NOT NULL,
    match_key         TEXT NOT NULL DEFAULT '',
    seg_count         INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_ec_kind_key ON exported_contracts(contract_kind, canonical_key, direction);
CREATE INDEX IF NOT EXISTS idx_ec_service  ON exported_contracts(service_id);

CREATE TABLE IF NOT EXISTS contract_links (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    src_contract_id   INTEGER NOT NULL REFERENCES exported_contracts(id) ON DELETE CASCADE,
    dst_contract_id   INTEGER NOT NULL REFERENCES exported_contracts(id) ON DELETE CASCADE,
    src_service_id    TEXT NOT NULL,
    dst_service_id    TEXT NOT NULL,
    link_kind         TEXT NOT NULL CHECK (link_kind IN (
                          'http_call', 'grpc_call', 'event_flow',
                          'shared_table', 'shared_cache', 'shared_config', 'pkg_import'
                      )),
    confidence        TEXT NOT NULL CHECK (confidence IN ('exact', 'high', 'medium', 'low')),
    match_rule        TEXT NOT NULL,
    UNIQUE (src_contract_id, dst_contract_id, link_kind)
);

CREATE INDEX IF NOT EXISTS idx_cl_src ON contract_links(src_service_id, src_contract_id);
CREATE INDEX IF NOT EXISTS idx_cl_dst ON contract_links(dst_service_id, dst_contract_id);

CREATE TABLE IF NOT EXISTS schema_version (
    id      INTEGER PRIMARY KEY CHECK (id = 1),
    version INTEGER NOT NULL
);
`

// ErrWorkspaceSchemaNewer reports a workspace.db written by a newer build.
// An older build cannot know what a newer migration changed, so it neither
// reads nor writes it.
type ErrWorkspaceSchemaNewer struct {
	Found  int
	Latest int
}

func (e *ErrWorkspaceSchemaNewer) Error() string {
	return fmt.Sprintf("workspace.db schema is v%d but this build only understands up to v%d; upgrade graphlume",
		e.Found, e.Latest)
}

// ErrWorkspaceSchemaOutdated reports a workspace.db older than this build that
// a read-only open cannot upgrade.
type ErrWorkspaceSchemaOutdated struct {
	Found  int
	Wanted int
}

func (e *ErrWorkspaceSchemaOutdated) Error() string {
	return fmt.Sprintf("workspace.db schema is v%d but this build expects v%d; run a workspace sync to upgrade it",
		e.Found, e.Wanted)
}

// querier is the read surface shared by *sql.DB and *sql.Tx.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// workspaceVersion reads the recorded schema version: 0 for an empty database,
// 1 for the original unversioned schema, otherwise the stored row.
func workspaceVersion(ctx context.Context, q querier) (int, error) {
	var n int
	if err := q.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_version'`).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		var v int
		err := q.QueryRowContext(ctx, `SELECT version FROM schema_version WHERE id = 1`).Scan(&v)
		if err == nil {
			return v, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	if err := q.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'workspace_services'`).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 1, nil
	}
	return 0, nil
}

// workspaceMigrations upgrades from version N-1 to N, keyed by N. Every step
// must be idempotent: a crash between a step and the version write re-runs it.
var workspaceMigrations = map[int]func(ctx context.Context, tx *sql.Tx) error{
	2: func(ctx context.Context, tx *sql.Tx) error {
		has := func(table, col string) (bool, error) {
			var n int
			err := tx.QueryRowContext(ctx,
				`SELECT count(*) FROM pragma_table_info('`+table+`') WHERE name = ?`, col).Scan(&n)
			return n > 0, err
		}
		ok, err := has("workspace_services", "links_truncated")
		if err != nil {
			return err
		}
		if !ok {
			if _, err := tx.ExecContext(ctx,
				`ALTER TABLE workspace_services ADD COLUMN links_truncated INTEGER NOT NULL DEFAULT 0`); err != nil {
				return err
			}
		}
		// run_started_unix leads the sync ordering: run ids restart at 1 when
		// a member's index is rebuilt, and only the start time tells a rebuilt
		// index's first run from the old index's last one.
		ok, err = has("workspace_services", "run_started_unix")
		if err != nil {
			return err
		}
		if !ok {
			if _, err := tx.ExecContext(ctx,
				`ALTER TABLE workspace_services ADD COLUMN run_started_unix INTEGER NOT NULL DEFAULT 0`); err != nil {
				return err
			}
		}
		ok, err = has("exported_contracts", "match_key")
		if err != nil {
			return err
		}
		if !ok {
			for _, ddl := range []string{
				`ALTER TABLE exported_contracts ADD COLUMN match_key TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE exported_contracts ADD COLUMN seg_count INTEGER NOT NULL DEFAULT 0`,
				// Backfill from canonical_key so rows written before v2 join
				// correctly until their service is next synced (which rewrites
				// them with the exact normalisation the matcher uses).
				`UPDATE exported_contracts SET match_key = lower(trim(canonical_key))`,
				`UPDATE exported_contracts SET match_key = trim(substr(match_key, 7))
				   WHERE contract_kind = 'queue_topic' AND match_key LIKE 'kafka:%'`,
				`UPDATE exported_contracts SET match_key = trim(substr(match_key, 7))
				   WHERE contract_kind = 'queue_topic' AND match_key LIKE 'topic:%'`,
				`UPDATE exported_contracts SET seg_count = (
				   SELECT CASE WHEN q.p = '' THEN 0 ELSE length(q.p) - length(replace(q.p, '/', '')) + 1 END
				   FROM (SELECT trim(CASE WHEN instr(trim(e.canonical_key), ' ') > 0
				                          THEN substr(trim(e.canonical_key), instr(trim(e.canonical_key), ' ') + 1)
				                          ELSE trim(e.canonical_key) END, '/ ') AS p
				         FROM exported_contracts e WHERE e.id = exported_contracts.id) q)
				   WHERE contract_kind = 'http_route'`,
			} {
				if _, err := tx.ExecContext(ctx, ddl); err != nil {
					return err
				}
			}
		}
		// Created here and not in the baseline DDL: on a v1 database the
		// columns it names do not exist until the step above has run.
		_, err = tx.ExecContext(ctx,
			`CREATE INDEX IF NOT EXISTS idx_ec_match ON exported_contracts(contract_kind, seg_count, match_key)`)
		return err
	},
}

// InitWorkspaceDB opens or creates the workspace federation database, applies
// its schema and migrates an older one. A database written by a newer build is
// refused with *ErrWorkspaceSchemaNewer before anything is changed.
func InitWorkspaceDB(ctx context.Context, path string) (*DB, error) {
	db, err := Open(ctx, Options{Path: path})
	if err != nil {
		return nil, fmt.Errorf("open workspace db %s: %w", path, err)
	}
	if err := migrateWorkspace(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply workspace schema %s: %w", path, err)
	}
	return db, nil
}

func migrateWorkspace(ctx context.Context, db *DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	found, err := workspaceVersion(ctx, tx)
	if err != nil {
		return err
	}
	if found > WorkspaceSchemaVersion {
		return &ErrWorkspaceSchemaNewer{Found: found, Latest: WorkspaceSchemaVersion}
	}
	if _, err := tx.ExecContext(ctx, WorkspaceSchemaDDL); err != nil {
		return err
	}
	// A fresh database starts at the baseline and is walked through the same
	// idempotent steps, so there is one upgrade path and not two.
	for v := max(found, 1) + 1; v <= WorkspaceSchemaVersion; v++ {
		if err := workspaceMigrations[v](ctx, tx); err != nil {
			return fmt.Errorf("migrate workspace schema to v%d: %w", v, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_version (id, version) VALUES (1, ?)
		 ON CONFLICT(id) DO UPDATE SET version = excluded.version`, WorkspaceSchemaVersion); err != nil {
		return err
	}
	return tx.Commit()
}

// OpenWorkspaceDBReadOnly opens an existing workspace.db for queries. It never
// creates or migrates: a missing file is an error, a newer schema is
// *ErrWorkspaceSchemaNewer and an older one *ErrWorkspaceSchemaOutdated.
func OpenWorkspaceDBReadOnly(ctx context.Context, path string) (*DB, error) {
	// Open creates the parent directory, which a query must not do.
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("open workspace db %s: %w", path, err)
	}
	db, err := Open(ctx, Options{Path: path, ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("open workspace db %s: %w", path, err)
	}
	v, err := workspaceVersion(ctx, db)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("read workspace schema version %s: %w", path, err)
	}
	switch {
	case v > WorkspaceSchemaVersion:
		_ = db.Close()
		return nil, &ErrWorkspaceSchemaNewer{Found: v, Latest: WorkspaceSchemaVersion}
	case v < WorkspaceSchemaVersion:
		_ = db.Close()
		return nil, &ErrWorkspaceSchemaOutdated{Found: v, Wanted: WorkspaceSchemaVersion}
	}
	return db, nil
}
