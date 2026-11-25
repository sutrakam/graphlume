package registry

import (
	"context"
	"fmt"
)

// Fingerprint identifies the content of a run cheaply enough to check on every
// query.
//
// It exists so callers can cache expensive derived structures — the context
// engine's in-memory catalogue is the motivating case, at 55 ms per build on a
// 10,000-symbol run and over a second at 94,000 — without risking a stale
// answer.
//
// KEYING ON runID ALONE WOULD BE WRONG. A committed run is not immutable:
// incremental indexing updates it IN PLACE (see registry/inplace.go), so the
// same run id can describe different content minute to minute while a developer
// edits. A cache keyed only on the id would serve pre-edit results indefinitely,
// which is the exact staleness this product exists to eliminate.
type Fingerprint struct {
	RunID       int64
	Files       int64
	Symbols     int64
	MaxSymbolID int64
	MaxFileID   int64
	MutationSeq int64
}

// CatalogueFingerprint reads a run's fingerprint.
//
// Two indexed aggregate scans — one per table — plus a monotonic sequence lookup,
// paid on every request in exchange for skipping the rebuild.
func (r *Registry) CatalogueFingerprint(ctx context.Context, runID int64) (Fingerprint, error) {
	fp := Fingerprint{RunID: runID}
	err := r.db.QueryRowContext(ctx, `
		SELECT f.n, s.n, s.m, f.m, COALESCE(r.mutation_seq, 0)
		  FROM (SELECT count(*) AS n, COALESCE(max(id),0) AS m
		          FROM files   WHERE index_run_id = ?) AS f,
		       (SELECT count(*) AS n, COALESCE(max(id),0) AS m
		          FROM symbols WHERE index_run_id = ?) AS s,
		       (SELECT mutation_seq
		          FROM index_runs WHERE id = ?) AS r`,
		runID, runID, runID,
	).Scan(&fp.Files, &fp.Symbols, &fp.MaxSymbolID, &fp.MaxFileID, &fp.MutationSeq)
	if err != nil {
		return fp, fmt.Errorf("registry: catalogue fingerprint for run %d: %w", runID, err)
	}
	return fp, nil
}
