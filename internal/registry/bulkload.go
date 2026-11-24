package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Bulk loading: setting indexes and triggers aside for a first index.
//
// A first index writes every symbol and reference row once into tables that
// hold nothing else. Maintaining the secondary indexes and the run_stats
// triggers row by row is the dominant cost of that write: every insert lands at
// a random place in each index, and each first touch of a page inside the
// per-file savepoint copies the page into the statement journal. On the VS Code
// tree it made the sink, the one serialised stage of the pipeline, twice as
// expensive as it needed to be. Building the same indexes from the finished
// table is one sort each.
//
// The objects are dropped and created again, never altered, and the CREATE
// statement of each is stored in deferred_schema (migration 0001) in the same
// transaction as its drop. So the schema and the table agree at every instant,
// whatever kills the process: a row there IS an object the schema lacks, and
// RestoreDeferred recreates it from the statement SQLite itself kept.

// deferrableIndexes are the secondary indexes a first index builds after the
// load. Indexes that support a foreign key (cascades from files, symbols and
// index_runs look rows up through them) stay in place, so a run that has to be
// aborted or reclaimed never turns a cascade into a table scan per row.
var deferrableIndexes = []string{
	"idx_refs_run_name",
	"idx_refs_status",
	"idx_refs_file_line",
	"idx_refs_name_binding",
	"idx_refs_bound_sites",
	"idx_symbols_name",
	"idx_symbols_fqn",
	"idx_symbols_file_name",
}

// deferrableTriggerPrefixes select the run_stats and file_rollup triggers
// (migration 0001). They clear a run's stored coverage figures, or record the
// files whose rollup rows went stale, when the run's rows change. A staging run
// has no figures to clear and is summed whole when it is published, so for the
// duration of its load they do nothing at a cost per row.
var deferrableTriggerPrefixes = []string{"run_stats_", "file_rollup_"}

// deferrableTrigger reports whether a trigger is one a first index sets aside.
func deferrableTrigger(name string) bool {
	for _, p := range deferrableTriggerPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// DeferredObject is a schema object that is currently dropped.
type DeferredObject struct {
	Name string
	Kind string // "index" or "trigger"
}

// BulkLoadEligible reports whether the index holds nothing a reader could be
// using, which is the one case where dropping its secondary indexes costs no
// answer anything. Any committed run makes it false: a reader of that run would
// find its indexes gone for as long as the load took.
func (r *Registry) BulkLoadEligible(ctx context.Context) (bool, error) {
	var committed int
	err := r.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM index_runs WHERE committed = 1)`).Scan(&committed)
	if err != nil {
		return false, fmt.Errorf("registry: bulk load: look for a committed run: %w", err)
	}
	return committed == 0, nil
}

// DeferBulkObjects drops the deferrable indexes and triggers and
// records how to create them again. It returns how many objects it set aside.
// Call it only when BulkLoadEligible is true, and pair it with RestoreDeferred
// before the run is published.
func (r *Registry) DeferBulkObjects(ctx context.Context) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("registry: defer schema objects: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().Unix()
	n := 0
	setAside := func(kind, name, createSQL string) error {
		if createSQL == "" {
			return nil
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO deferred_schema (name, kind, sql, deferred_unix) VALUES (?,?,?,?)`,
			name, kind, createSQL, now); err != nil {
			return fmt.Errorf("record %s %s: %w", kind, name, err)
		}
		if _, err := tx.ExecContext(ctx, `DROP `+strings.ToUpper(kind)+` IF EXISTS "`+name+`"`); err != nil {
			return fmt.Errorf("drop %s %s: %w", kind, name, err)
		}
		n++
		return nil
	}

	for _, name := range deferrableIndexes {
		var stmt sql.NullString
		err := tx.QueryRowContext(ctx,
			`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&stmt)
		if errors.Is(err, sql.ErrNoRows) {
			continue // already absent; nothing to defer
		}
		if err != nil {
			return 0, fmt.Errorf("registry: defer schema objects: read %s: %w", name, err)
		}
		if err := setAside("index", name, stmt.String); err != nil {
			return 0, fmt.Errorf("registry: defer schema objects: %w", err)
		}
	}

	type trig struct{ name, stmt string }
	var trigs []trig
	rows, err := tx.QueryContext(ctx,
		`SELECT name, sql FROM sqlite_master WHERE type = 'trigger' ORDER BY name`)
	if err != nil {
		return 0, fmt.Errorf("registry: defer schema objects: list triggers: %w", err)
	}
	for rows.Next() {
		var t trig
		if err := rows.Scan(&t.name, &t.stmt); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("registry: defer schema objects: scan trigger: %w", err)
		}
		if deferrableTrigger(t.name) {
			trigs = append(trigs, t)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("registry: defer schema objects: list triggers: %w", err)
	}
	_ = rows.Close()
	for _, t := range trigs {
		if err := setAside("trigger", t.name, t.stmt); err != nil {
			return 0, fmt.Errorf("registry: defer schema objects: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("registry: defer schema objects: commit: %w", err)
	}
	return n, nil
}

// DeferredObjects lists the schema objects that are currently set aside, in
// the order RestoreDeferred recreates them. An index with no deferred_schema
// table has none.
func (r *Registry) DeferredObjects(ctx context.Context) ([]DeferredObject, error) {
	return listDeferred(ctx, r.db)
}

type rowsQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func listDeferred(ctx context.Context, q rowsQuerier) ([]DeferredObject, error) {
	// Indexes before triggers, so a trigger never exists without the indexes
	// its statements would have used.
	rows, err := q.QueryContext(ctx,
		`SELECT name, kind FROM deferred_schema ORDER BY CASE kind WHEN 'index' THEN 0 ELSE 1 END, name`)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return nil, nil
		}
		return nil, fmt.Errorf("registry: list deferred schema objects: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []DeferredObject
	for rows.Next() {
		var o DeferredObject
		if err := rows.Scan(&o.Name, &o.Kind); err != nil {
			return nil, fmt.Errorf("registry: list deferred schema objects: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// RestoreDeferred creates every set-aside object again and forgets it. It is
// idempotent and resumable: each object is created and its record removed in
// one transaction, so a restore that is itself interrupted leaves the rest
// recorded for the next call. It returns how many objects it created.
//
// It is called before a bulk-loaded run is published, at the start of every
// index (a process that died mid-load left rows behind), and after a run is
// aborted or reclaimed. It needs the single-writer guarantee ReclaimAbandoned
// already asks for: restoring while another process is still loading would
// build indexes under its feet.
func (r *Registry) RestoreDeferred(ctx context.Context) (int, error) {
	todo, err := r.DeferredObjects(ctx)
	if err != nil || len(todo) == 0 {
		return 0, err
	}
	n := 0
	for _, o := range todo {
		if err := ctx.Err(); err != nil {
			return n, fmt.Errorf("registry: restore deferred schema objects: %w", err)
		}
		if err := r.restoreOne(ctx, o); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (r *Registry) restoreOne(ctx context.Context, o DeferredObject) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("registry: restore %s %s: begin: %w", o.Kind, o.Name, err)
	}
	defer func() { _ = tx.Rollback() }()

	var stmt string
	if err := tx.QueryRowContext(ctx, `SELECT sql FROM deferred_schema WHERE name = ?`, o.Name).Scan(&stmt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil // another restore got there first
		}
		return fmt.Errorf("registry: restore %s %s: read: %w", o.Kind, o.Name, err)
	}
	// The recorded text is the object's own CREATE statement. IF NOT EXISTS
	// makes a retry after a crash between the CREATE and the commit harmless.
	stmt = ifNotExists(stmt)
	if _, err := tx.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("registry: restore %s %s: %w", o.Kind, o.Name, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM deferred_schema WHERE name = ?`, o.Name); err != nil {
		return fmt.Errorf("registry: restore %s %s: forget: %w", o.Kind, o.Name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("registry: restore %s %s: commit: %w", o.Kind, o.Name, err)
	}
	return nil
}

// ifNotExists inserts IF NOT EXISTS after CREATE [UNIQUE] INDEX or CREATE
// TRIGGER when the statement does not already carry it.
func ifNotExists(stmt string) string {
	upper := strings.ToUpper(stmt)
	if strings.Contains(strings.SplitN(upper, " ON ", 2)[0], "IF NOT EXISTS") {
		return stmt
	}
	for _, kw := range []string{"CREATE UNIQUE INDEX ", "CREATE INDEX ", "CREATE TRIGGER "} {
		if strings.HasPrefix(upper, kw) {
			return stmt[:len(kw)-1] + " IF NOT EXISTS " + stmt[len(kw):]
		}
	}
	return stmt
}
