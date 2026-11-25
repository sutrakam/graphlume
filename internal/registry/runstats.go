package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Binding statistics stored with the run (migration 0001).
//
// LanguageBindings and ResolutionStats summarise every reference and every edge
// of a run. They answer the same thing for every question asked of one index
// revision, and recomputing them per process made each relationship answer on a
// 1.9-million-reference index cost seconds that had nothing to do with the
// question. They are computed once, when the index is written, and read back.

// RefreshRunStats recomputes the run's binding statistics and stores them.
//
// Called when a writer has finished with the run: `resolve`, `scip import` and
// the incremental update. It is safe to call at any time and costs the same as
// one live read of the figures, which is the cost it exists to move from every
// question to one write.
//
// Nothing here is a correctness mechanism: a reader trusts the stored rows only
// while run_stats.valid is set, and every write to files, symbols, references
// or edges clears it (the triggers in the migration). Forgetting to call this
// after a new kind of write makes answers slower, never wrong.
func (r *Registry) RefreshRunStats(ctx context.Context, runID int64) error {
	langs, err := r.computeLanguageBindings(ctx, runID)
	if err != nil {
		return err
	}
	// A plain scan: with the index the planner chose, each of the 3.8 million
	// rows of a large run was fetched from the table in name order, 24 s of
	// random reads for a count that is zero unless a compiler index was imported.
	var byCompiler int64
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM symbol_references NOT INDEXED WHERE index_run_id = ? AND resolution_source = 'compiler'`,
		runID).Scan(&byCompiler); err != nil {
		return fmt.Errorf("registry: run stats: %w", err)
	}

	unreferenced, err := r.countUnreferenced(ctx, runID)
	if err != nil {
		return err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("registry: run stats: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM run_language_stats WHERE index_run_id = ?`, runID); err != nil {
		return fmt.Errorf("registry: run stats: %w", err)
	}
	for lang, l := range langs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO run_language_stats (index_run_id, language, files, definitions, relationships,
			       hierarchy, total, resolved, ambiguous, unresolved, ignored, pending)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			runID, lang, l.Files, l.Definitions, l.Relationships, l.Hierarchy,
			l.Total, l.Resolved, l.Ambiguous, l.Unresolved, l.Ignored, l.Pending); err != nil {
			return fmt.Errorf("registry: run stats: %w", err)
		}
	}
	// Written last, in the same transaction, so a reader never sees valid = 1
	// beside rows from an earlier computation.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO run_stats (index_run_id, valid, by_compiler, computed_unix, unreferenced) VALUES (?, 1, ?, ?, ?)
		ON CONFLICT(index_run_id) DO UPDATE SET valid = 1, by_compiler = excluded.by_compiler,
		       computed_unix = excluded.computed_unix, unreferenced = excluded.unreferenced`,
		runID, byCompiler, time.Now().Unix(), unreferenced); err != nil {
		return fmt.Errorf("registry: run stats: %w", err)
	}
	return tx.Commit()
}

// storedRunStats reports whether the run has valid stored statistics. A missing
// table (a database older than this migration, opened by a build that does not
// migrate) is "not stored", not an error.
func (r *Registry) storedRunStats(ctx context.Context, runID int64) (byCompiler int64, ok bool, err error) {
	var valid int
	err = r.db.QueryRowContext(ctx,
		`SELECT valid, by_compiler FROM run_stats WHERE index_run_id = ?`, runID).Scan(&valid, &byCompiler)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, false, nil
	case err != nil && strings.Contains(err.Error(), "no such table"):
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("registry: stored run stats: %w", err)
	}
	return byCompiler, valid == 1, nil
}

// storedLanguageBindings returns the stored per-language figures, or false when
// there are none that still describe the run.
func (r *Registry) storedLanguageBindings(ctx context.Context, runID int64) (map[string]LanguageBinding, bool, error) {
	if _, ok, err := r.storedRunStats(ctx, runID); err != nil || !ok {
		return nil, false, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT language, files, definitions, relationships, hierarchy,
		       total, resolved, ambiguous, unresolved, ignored, pending
		  FROM run_language_stats WHERE index_run_id = ? ORDER BY language`, runID)
	if err != nil {
		return nil, false, fmt.Errorf("registry: stored language bindings: %w", err)
	}
	defer rows.Close()
	out := map[string]LanguageBinding{}
	for rows.Next() {
		var l LanguageBinding
		if err := rows.Scan(&l.Language, &l.Files, &l.Definitions, &l.Relationships, &l.Hierarchy,
			&l.Total, &l.Resolved, &l.Ambiguous, &l.Unresolved, &l.Ignored, &l.Pending); err != nil {
			return nil, false, fmt.Errorf("registry: stored language bindings: %w", err)
		}
		out[l.Language] = l
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	// A computed run with no languages (an empty index) stores no rows and is
	// still valid; the live read returns the same empty map.
	return out, true, nil
}

// storedResolutionStats returns the run-wide totals from the stored figures.
func (r *Registry) storedResolutionStats(ctx context.Context, runID int64) (ResolutionStats, bool, error) {
	byCompiler, ok, err := r.storedRunStats(ctx, runID)
	if err != nil || !ok {
		return ResolutionStats{}, false, err
	}
	var s ResolutionStats
	if err := r.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(total),0), COALESCE(SUM(resolved),0), COALESCE(SUM(ambiguous),0),
		       COALESCE(SUM(unresolved),0), COALESCE(SUM(ignored),0), COALESCE(SUM(pending),0)
		  FROM run_language_stats WHERE index_run_id = ?`, runID).
		Scan(&s.Total, &s.Resolved, &s.Ambiguous, &s.Unresolved, &s.Ignored, &s.Pending); err != nil {
		return ResolutionStats{}, false, fmt.Errorf("registry: stored resolution stats: %w", err)
	}
	s.ByCompiler = byCompiler
	return s, true, nil
}

// countUnreferenced is how many definitions UnreferencedSymbols would return for
// the run: the number a full dead-code scan examines.
func (r *Registry) countUnreferenced(ctx context.Context, runID int64) (int64, error) {
	args := make([]any, 0, len(unreferencedKinds)+1)
	args = append(args, runID)
	for _, k := range unreferencedKinds {
		args = append(args, k)
	}
	var n int64
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM symbols s NOT INDEXED
		 WHERE s.index_run_id = ?
		   AND s.kind IN (`+placeholders(len(unreferencedKinds))+`)
		   AND NOT EXISTS (SELECT 1 FROM edges e INDEXED BY idx_edges_dst
		                    WHERE `+incomingEdge+`)`, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("registry: count unreferenced symbols: %w", err)
	}
	return n, nil
}

// StoredUnreferencedCount is how many definitions a full scan for unreferenced
// ones would examine, when the run's stored figures are valid. False means
// "not stored": an index written by an older build, or changed since.
func (r *Registry) StoredUnreferencedCount(ctx context.Context, runID int64) (int, bool, error) {
	var valid int
	var n int64
	err := r.db.QueryRowContext(ctx,
		`SELECT valid, unreferenced FROM run_stats WHERE index_run_id = ?`, runID).Scan(&valid, &n)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, false, nil
	case err != nil && (strings.Contains(err.Error(), "no such table") || strings.Contains(err.Error(), "no such column")):
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("registry: stored unreferenced count: %w", err)
	}
	return int(n), valid == 1 && n >= 0, nil
}
