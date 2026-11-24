package registry

import (
	"context"
	"fmt"
)

// In-place updates to the committed run.
//
// By default a committed run is immutable: ReplaceFile and NewWriter both
// refuse it, so the two-phase swap is the only way new content becomes visible.
// That is the right default, and full indexing keeps it.
//
// Incremental indexing needs something different, and the reason is arithmetic.
// The alternative — seed a fresh staging run from the committed one, apply the
// changes, then swap — copies every unchanged row on every update. At the
// measured write throughput (~35k rows/s) a 50k-file repository costs seconds
// per pass, so a file watcher reacting to a single keystroke-save would spend
// its time copying rows that did not change. That defeats the entire purpose of
// incremental indexing, which spec §12 defines as scaling with the number of
// changed files rather than repository size.
//
// So in-place update is supported, but only when the caller asks for it by
// name. What you keep and what you give up:
//
//   KEEP: per-file atomicity. Each ReplaceFile or DeleteFile is a single
//   transaction, so a reader observes a file either entirely before or
//   entirely after the change. The half-replaced file the guard warns about
//   cannot happen — SQLite prevents it.
//
//   GIVE UP: cross-file consistency during a pass. An incremental update
//   touching twenty files is twenty transactions, so a reader running
//   concurrently may see some updated and some not. The graph stays
//   internally valid throughout; it simply reflects a moment in the middle of
//   the pass.
//
// That trade is acceptable here because the index describes a working tree
// that is itself being edited. A developer who saves a file while an agent is
// querying does not expect the index to represent an instant that never
// existed on disk — they expect it to catch up quickly.
//
// Full indexing must never use this. It builds a staging run and swaps, which
// is why `graphlume index` remains all-or-nothing and an interrupted full index
// leaves the previous revision untouched.

// AllowCommittedRun permits mutating the currently committed run.
//
// Required for incremental updates. Read the package comment above before
// using it: it exchanges cross-file consistency for update cost, deliberately.
func AllowCommittedRun() ReplaceOption {
	return func(o *replaceOpts) { o.allowCommitted = true }
}

// FileStat is a file's size and modification time as a pass last observed
// them.
type FileStat struct {
	Path        string
	SizeBytes   int64
	ModTimeUnix int64
}

// RestatFiles records new sizes and modification times for files whose
// content a pass read and found unchanged.
//
// The next pass decides whether to read a file by comparing its size and time
// with these columns (internal/acquire/fs/trust.go). A file that was touched
// but not changed (a branch switch and back, a formatter that rewrote the same
// bytes) has a new time and the old hash; left alone, its row would never
// match again and every later pass would re-read it. Nothing else about the
// file is written: its symbols, edges and search row describe the same bytes
// they did before. The registry digest does not cover modification time, so
// this cannot move it.
//
// One transaction for the whole list. Paths the run does not hold are ignored.
func (r *Registry) RestatFiles(ctx context.Context, runID int64, stats []FileStat) error {
	if len(stats) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("registry: restat files: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx,
		`UPDATE files SET size_bytes = ?, mod_time_unix = ? WHERE index_run_id = ? AND path = ?`)
	if err != nil {
		return fmt.Errorf("registry: restat files: prepare: %w", err)
	}
	defer stmt.Close()
	for _, s := range stats {
		if _, err := stmt.ExecContext(ctx, s.SizeBytes, s.ModTimeUnix, runID, s.Path); err != nil {
			return fmt.Errorf("registry: restat %s: %w", s.Path, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE index_runs SET mutation_seq = mutation_seq + 1 WHERE id = ?`, runID); err != nil {
		return fmt.Errorf("registry: restat files: bump mutation_seq: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("registry: restat files: commit: %w", err)
	}
	return nil
}

// MarkUpdated records that an in-place pass has brought the committed run up
// to date: the run's started_unix becomes the moment that pass began and its
// finished_unix the moment it ended.
//
// Both columns are written once, by BeginRun and CommitRun, so an index kept
// current by `graphlume index` and `graphlume watch` for a month would still say
// it was a month old: `status` would print the first full run's time as "Last
// indexed", and the age every tool answer carries would count from it. The run row
// describes the pass that last made the run true, and after an in-place pass
// that is not the full one.
//
// passStartedUnix must be the wall-clock time taken BEFORE the pass began
// discovering files, never after. It is also the anchor the next pass uses to
// decide which files it can leave unread: a file is trusted only when its
// times are safely older than this moment, which is sound only if every file
// was in fact looked at after it. See internal/acquire/fs/trust.go.
//
// Call it only after a pass succeeds. A pass that stopped part-way has not
// looked at every file, and moving the anchor would say that it had.
func (r *Registry) MarkUpdated(ctx context.Context, runID, passStartedUnix int64) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE index_runs SET started_unix = ?, finished_unix = ? WHERE id = ? AND committed = 1`,
		passStartedUnix, r.now(), runID)
	if err != nil {
		return fmt.Errorf("registry: mark run %d updated: %w", runID, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return fmt.Errorf("registry: mark run %d updated: %w", runID, ErrRunNotFound)
	}
	return nil
}
