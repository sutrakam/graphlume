package registry

import (
	"context"
	"fmt"
)

// HeuristicCall is a call edge a heuristic resolver wrote, with what a
// compiler comparison needs to judge it.
type HeuristicCall struct {
	ID       int64
	DstID    int64
	DstPath  string
	DstName  string
	File     string
	Line     int
	Language string
}

// HeuristicCalls lists the CALLS and INSTANTIATES edges that the built-in
// checks and indexers may judge, whose evidence is in a file of the given
// language, in a deterministic order: the ones no compiler vouched for, and
// those written from an imported scip-go index. The second kind is there so
// that when the Go type check and a scip-go index disagree about a call the
// type check, which runs later and judges every call of the file, wins.
func (r *Registry) HeuristicCalls(ctx context.Context, runID int64, language string) ([]HeuristicCall, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT e.id, e.dst_id, df.path, d.name, e.evidence_file, e.evidence_line
		  FROM edges e
		  JOIN files ef ON ef.index_run_id = e.index_run_id AND ef.path = e.evidence_file
		  JOIN symbols d ON d.id = e.dst_id
		  JOIN files df ON df.id = d.file_id
		 WHERE e.index_run_id = ?
		   AND (e.source <> 'compiler' OR e.detail LIKE 'scip: scip-go%')
		   AND e.kind IN ('CALLS','INSTANTIATES') AND ef.language = ?
		 ORDER BY e.id`, runID, language)
	if err != nil {
		return nil, fmt.Errorf("registry: list heuristic calls: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []HeuristicCall
	for rows.Next() {
		var h HeuristicCall
		if err := rows.Scan(&h.ID, &h.DstID, &h.DstPath, &h.DstName, &h.File, &h.Line); err != nil {
			return nil, fmt.Errorf("registry: list heuristic calls: scan: %w", err)
		}
		h.Language = language
		out = append(out, h)
	}
	return out, rows.Err()
}

// DeleteEdges removes edges by id in one transaction and returns how many
// went. It is for edges a compiler has PROVEN false; it is not a general
// purpose delete.
func (r *Registry) DeleteEdges(ctx context.Context, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("registry: delete edges: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	n := 0
	for _, id := range ids {
		res, err := tx.ExecContext(ctx, `DELETE FROM edges WHERE id = ?`, id)
		if err != nil {
			return n, fmt.Errorf("registry: delete edge %d: %w", id, err)
		}
		if c, err := res.RowsAffected(); err == nil {
			n += int(c)
		}
	}
	if err := tx.Commit(); err != nil {
		return n, fmt.Errorf("registry: delete edges: commit: %w", err)
	}
	return n, nil
}

// GoTypesCoverage reports how much of a run's Go code the built-in type check
// stands behind: the Go files indexed, the files that carry at least one edge
// written under detail, and the edge count.
func (r *Registry) GoTypesCoverage(ctx context.Context, runID int64, detail string) (goFiles, checkedFiles, edges int64, err error) {
	if err = r.db.QueryRowContext(ctx,
		`SELECT count(*) FROM files WHERE index_run_id = ? AND language = 'go'`, runID).Scan(&goFiles); err != nil {
		return 0, 0, 0, fmt.Errorf("registry: count go files: %w", err)
	}
	if err = r.db.QueryRowContext(ctx, `
		SELECT count(DISTINCT evidence_file), count(*) FROM edges
		 WHERE index_run_id = ? AND source = 'compiler' AND detail = ?`, runID, detail).Scan(&checkedFiles, &edges); err != nil {
		return 0, 0, 0, fmt.Errorf("registry: count type-checked edges: %w", err)
	}
	return goFiles, checkedFiles, edges, nil
}
