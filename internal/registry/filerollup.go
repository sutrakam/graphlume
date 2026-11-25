package registry

import (
	"context"
	"database/sql"
	"fmt"
)

// File rollups: the edge graph summed to files (file_edges) and each file's
// symbol count (file_symbols). See the FILE ROLLUPS section of migration 0001.
//
// Both are caches of `edges` and `symbols`. RefreshFileRollup rewrites a run's
// rows from scratch; UpdateFileRollup rewrites only the rows of the files the
// file_rollup_* triggers recorded as touched since the last update, and falls
// back to the full rewrite when that is more than a quarter of the run.

// rollupFullShare is the share of a run's files above which an update
// rewrites every row instead of the touched files' rows. The scoped rewrite
// reads each touched file's edges through two indexes; past a quarter of the
// files one sequential pass over the edges is cheaper.
const rollupFullShare = 4

// execer is the part of *sql.DB and *sql.Tx the rollup statements need.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// RefreshFileRollup rewrites the run's file rollups from its edges and
// symbols, in one transaction. `resolve`, `scip import` and CommitRun call it
// when they finish writing.
func (r *Registry) RefreshFileRollup(ctx context.Context, runID int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("registry: file rollup: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := rebuildFileRollup(ctx, tx, runID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("registry: file rollup: commit: %w", err)
	}
	return nil
}

// MarkFileRollupStale records that the run's file rollups will be summed again
// whole, which stops the triggers recording touched files until they are. A
// writer that is about to rewrite most of a run's edges calls it first and
// RefreshFileRollup when it finishes; if it never finishes, the next update
// sums the run whole.
func (r *Registry) MarkFileRollupStale(ctx context.Context, runID int64) error {
	if _, err := r.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO file_rollup_dirty (index_run_id, file_id) VALUES (?, 0)`, runID); err != nil {
		return fmt.Errorf("registry: file rollup: mark stale: %w", err)
	}
	return nil
}

// UpdateFileRollup brings the run's file rollups up to date with the writes
// recorded since the last update, and reports whether it rewrote the whole
// run. The incremental update calls it when it finishes, whether or not it
// succeeded, so the rollup describes whatever was written.
func (r *Registry) UpdateFileRollup(ctx context.Context, runID int64) (full bool, err error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("registry: file rollup: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var touched, files, stale int64
	if err := tx.QueryRowContext(ctx, `
		SELECT (SELECT count(*) FROM file_rollup_dirty d JOIN files f ON f.id = d.file_id
		         WHERE d.index_run_id = ?1 AND f.index_run_id = ?1),
		       (SELECT count(*) FROM files WHERE index_run_id = ?1),
		       (SELECT count(*) FROM file_rollup_dirty WHERE index_run_id = ?1 AND file_id = 0)`,
		runID).Scan(&touched, &files, &stale); err != nil {
		return false, fmt.Errorf("registry: file rollup: count touched files: %w", err)
	}
	switch {
	case stale > 0:
		// A writer marked the run for a full sum and did not get to it.
		full = true
		if err := rebuildFileRollup(ctx, tx, runID); err != nil {
			return false, err
		}
	case touched == 0:
		// Nothing to rewrite: only deleted files, whose rows the cascade took.
		if _, err := tx.ExecContext(ctx, `DELETE FROM file_rollup_dirty WHERE index_run_id = ?`, runID); err != nil {
			return false, fmt.Errorf("registry: file rollup: %w", err)
		}
	case touched*rollupFullShare > files:
		full = true
		if err := rebuildFileRollup(ctx, tx, runID); err != nil {
			return false, err
		}
	default:
		if err := updateTouchedFiles(ctx, tx, runID); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("registry: file rollup: commit: %w", err)
	}
	return full, nil
}

// rebuildFileRollup replaces every rollup row of the run and forgets the
// touched files, which the rewrite has covered.
func rebuildFileRollup(ctx context.Context, tx execer, runID int64) error {
	for _, stmt := range []struct{ what, sql string }{
		{"clear file edges", `DELETE FROM file_edges WHERE index_run_id = ?`},
		{"sum file edges", `
			INSERT INTO file_edges (index_run_id, src_file_id, dst_file_id, kind, confidence, n)
			SELECT e.index_run_id, s.file_id, d.file_id, e.kind, e.confidence, count(*)
			  FROM edges e
			  JOIN symbols s ON s.id = e.src_id
			  JOIN symbols d ON d.id = e.dst_id
			 WHERE e.index_run_id = ?1 AND s.file_id <> d.file_id
			 GROUP BY s.file_id, d.file_id, e.kind, e.confidence`},
		{"clear file symbols", `DELETE FROM file_symbols WHERE index_run_id = ?`},
		{"count file symbols", `
			INSERT INTO file_symbols (file_id, index_run_id, n)
			SELECT file_id, index_run_id, count(*) FROM symbols
			 WHERE index_run_id = ?1
			 GROUP BY file_id`},
		{"forget touched files", `DELETE FROM file_rollup_dirty WHERE index_run_id = ?`},
	} {
		if _, err := tx.ExecContext(ctx, stmt.sql, runID); err != nil {
			return fmt.Errorf("registry: file rollup: %s: %w", stmt.what, err)
		}
	}
	return nil
}

// updateTouchedFiles rewrites the rollup rows that have a touched file at
// either end. An edge is read once even when both of its files were touched:
// the two halves of the UNION select the edge's id with it.
func updateTouchedFiles(ctx context.Context, tx execer, runID int64) error {
	const touched = `SELECT d.file_id FROM file_rollup_dirty d JOIN files f ON f.id = d.file_id
	                  WHERE d.index_run_id = ?1 AND f.index_run_id = ?1`
	for _, stmt := range []struct{ what, sql string }{
		{"clear rows from touched files", `DELETE FROM file_edges WHERE index_run_id = ?1 AND src_file_id IN (` + touched + `)`},
		{"clear rows into touched files", `DELETE FROM file_edges WHERE index_run_id = ?1 AND dst_file_id IN (` + touched + `)`},
		{"sum touched files' edges", `
			WITH t(id) AS (` + touched + `),
			     x AS (
			       SELECT e.id, e.kind, e.confidence, s.file_id AS sf, d.file_id AS df
			         FROM t JOIN symbols s ON s.file_id = t.id
			         JOIN edges e ON e.index_run_id = ?1 AND e.src_id = s.id
			         JOIN symbols d ON d.id = e.dst_id
			       UNION
			       SELECT e.id, e.kind, e.confidence, s.file_id, d.file_id
			         FROM t JOIN symbols d ON d.file_id = t.id
			         JOIN edges e ON e.index_run_id = ?1 AND e.dst_id = d.id
			         JOIN symbols s ON s.id = e.src_id)
			INSERT INTO file_edges (index_run_id, src_file_id, dst_file_id, kind, confidence, n)
			SELECT ?1, sf, df, kind, confidence, count(*) FROM x
			 WHERE sf <> df
			 GROUP BY sf, df, kind, confidence`},
		{"clear touched files' symbol counts", `DELETE FROM file_symbols WHERE file_id IN (` + touched + `)`},
		{"count touched files' symbols", `
			INSERT INTO file_symbols (file_id, index_run_id, n)
			SELECT s.file_id, ?1, count(*) FROM symbols s
			 WHERE s.file_id IN (` + touched + `)
			 GROUP BY s.file_id`},
		{"forget touched files", `DELETE FROM file_rollup_dirty WHERE index_run_id = ?1`},
	} {
		if _, err := tx.ExecContext(ctx, stmt.sql, runID); err != nil {
			return fmt.Errorf("registry: file rollup: %s: %w", stmt.what, err)
		}
	}
	return nil
}
