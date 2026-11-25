package registry

import (
	"context"
	"fmt"
)

// EdgeKey identifies an edge the way the edges table's unique constraint does.
type EdgeKey struct {
	Src, Dst int64
	Kind     string
	File     string
	Line     int
}

// RetractStats reports what RetractStale did.
type RetractStats struct {
	// Deleted are edges the compiler pass had written and no longer proves,
	// which no heuristic ever claimed.
	Deleted int
	// Restored are edges the compiler pass had upgraded from a heuristic tier
	// and no longer proves; they go back to the tier and source they had.
	Restored int
}

// RetractStale withdraws the edges a previous compiler pass wrote under detail
// and the current pass did not write again (keep is the current pass's set).
//
// WHY THIS EXISTS. A compiler pass that re-runs over a repository whose files
// have changed must not leave an exact edge behind that it can no longer
// prove. Replacing a changed file already deletes the edges that touch its
// symbols, but an edge between two UNCHANGED files can go stale too: file A
// calls pkg.M, and an edit elsewhere makes the call resolve to another M. The
// old edge's two endpoints are untouched, so nothing removes it. An exact edge
// that is wrong is worse than a missing one, so every pass settles its own
// previous output.
//
// An edge the pass UPGRADED keeps the heuristic claim it replaced in
// prior_confidence/prior_source; retracting it restores that claim instead of
// deleting a relationship the heuristic resolver had found.
func (r *Registry) RetractStale(ctx context.Context, runID int64, detail string, keep map[EdgeKey]struct{}) (RetractStats, error) {
	var st RetractStats
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, src_id, dst_id, kind, evidence_file, evidence_line, prior_confidence
		  FROM edges
		 WHERE index_run_id = ? AND source = 'compiler' AND detail = ?`, runID, detail)
	if err != nil {
		return st, fmt.Errorf("registry: list compiler edges: %w", err)
	}
	type stale struct {
		id       int64
		upgraded bool
	}
	var drop []stale
	for rows.Next() {
		var (
			id, src, dst int64
			line         int
			kind, file   string
			prior        string
		)
		if err := rows.Scan(&id, &src, &dst, &kind, &file, &line, &prior); err != nil {
			_ = rows.Close()
			return st, fmt.Errorf("registry: list compiler edges: scan: %w", err)
		}
		if _, ok := keep[EdgeKey{Src: src, Dst: dst, Kind: kind, File: file, Line: line}]; ok {
			continue
		}
		drop = append(drop, stale{id: id, upgraded: prior != ""})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return st, err
	}
	_ = rows.Close()
	if len(drop) == 0 {
		return st, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return st, fmt.Errorf("registry: retract: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, d := range drop {
		if d.upgraded {
			if _, err := tx.ExecContext(ctx, `
				UPDATE edges SET confidence = prior_confidence, source = prior_source,
				       detail = 'restored: the compiler pass no longer proves this edge',
				       prior_confidence = '', prior_source = ''
				 WHERE id = ?`, d.id); err != nil {
				return st, fmt.Errorf("registry: restore edge %d: %w", d.id, err)
			}
			st.Restored++
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM edges WHERE id = ?`, d.id); err != nil {
			return st, fmt.Errorf("registry: delete edge %d: %w", d.id, err)
		}
		st.Deleted++
	}
	if err := tx.Commit(); err != nil {
		return st, fmt.Errorf("registry: retract: commit: %w", err)
	}
	return st, nil
}
