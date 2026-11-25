package registry

import (
	"context"
	"fmt"
)

// contradictedSQL selects the heuristic call edges a compiler has proven
// false: in a file a compiler read, on a line where the compiler proved a call
// of the same name to a different definition, and proved as many calls of
// that name on the line as the parser recorded, so no call on the line is left
// for the heuristic edge to stand for; and with no compiler edge on the line
// to the heuristic's own target.
//
// Edges are unique per evidence line, so the line is the unit. Measured on
// honojs/hono: `c.header(...)` at middleware/cors/index.ts:161 had an exact
// edge to Context.header and a medium heuristic edge to HonoRequest.header,
// and callers listed both.
const contradictedSQL = `
	SELECT h.id
	  FROM edges h
	  JOIN symbols hd ON hd.id = h.dst_id
	  JOIN files hf ON hf.index_run_id = h.index_run_id AND hf.path = h.evidence_file
	  JOIN compiler_coverage cc ON cc.index_run_id = h.index_run_id AND cc.file_id = hf.id
	 WHERE h.index_run_id = ?
	   AND h.source <> 'compiler'
	   AND h.kind IN ('CALLS','INSTANTIATES')
	   AND NOT EXISTS (
	       SELECT 1 FROM edges x
	        WHERE x.index_run_id = h.index_run_id AND x.evidence_file = h.evidence_file
	          AND x.evidence_line = h.evidence_line AND x.source = 'compiler'
	          AND x.kind IN ('CALLS','INSTANTIATES') AND x.dst_id = h.dst_id)
	   AND (SELECT COUNT(*) FROM edges x JOIN symbols xd ON xd.id = x.dst_id
	         WHERE x.index_run_id = h.index_run_id AND x.evidence_file = h.evidence_file
	           AND x.evidence_line = h.evidence_line AND x.source = 'compiler'
	           AND x.kind IN ('CALLS','INSTANTIATES') AND xd.name = hd.name) >=
	       (SELECT COUNT(*) FROM symbol_references r
	         WHERE r.index_run_id = h.index_run_id AND r.file_id = hf.id
	           AND r.start_line = h.evidence_line AND r.name = hd.name)
	   AND EXISTS (SELECT 1 FROM edges x JOIN symbols xd ON xd.id = x.dst_id
	         WHERE x.index_run_id = h.index_run_id AND x.evidence_file = h.evidence_file
	           AND x.evidence_line = h.evidence_line AND x.source = 'compiler'
	           AND x.kind IN ('CALLS','INSTANTIATES') AND xd.name = hd.name)`

// WithdrawContradictedCalls removes the heuristic call edges a compiler
// proved false (see contradictedSQL) and re-binds each reference that pointed
// at a removed edge's target, on that line, to the one definition of the name
// the compiler proved there. It returns how many edges went and how many
// references were re-bound.
func (r *Registry) WithdrawContradictedCalls(ctx context.Context, runID int64) (withdrawn, rebound int, err error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("registry: withdraw contradicted calls: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS contradicted_edge (id INTEGER PRIMARY KEY)`); err != nil {
		return 0, 0, fmt.Errorf("registry: withdraw contradicted calls: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM contradicted_edge`); err != nil {
		return 0, 0, fmt.Errorf("registry: withdraw contradicted calls: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO contradicted_edge (id) `+contradictedSQL, runID); err != nil {
		return 0, 0, fmt.Errorf("registry: withdraw contradicted calls: select: %w", err)
	}

	// Re-bind first, while the removed edges still say which line and which
	// wrong target: a reference on that line spelled like the target and bound
	// to it moves to the single definition of the name the compiler proved
	// there. Where the compiler proved more than one, it is left alone.
	//
	// Driven from the removed edges, the small side, into a table keyed by
	// (file, line, wrong target); the references are then found through
	// idx_refs_file_line. A single UPDATE with a correlated
	// subquery per reference would scan every reference of the run for
	// each removed edge once a compiler withdraws hundreds of them. The joins
	// are CROSS JOINs on purpose, as in structure.go: with no statistics the
	// planner starts from the large edges table and probes the withdrawn ones,
	// 18 s on honojs/hono with nothing to withdraw.
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS temp.contradicted_rebind`); err != nil {
		return 0, 0, fmt.Errorf("registry: withdraw contradicted calls: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		CREATE TEMP TABLE contradicted_rebind (
		       file_id INTEGER NOT NULL, line INTEGER NOT NULL, old INTEGER NOT NULL, new INTEGER NOT NULL,
		       PRIMARY KEY (file_id, line, old)) WITHOUT ROWID`); err != nil {
		return 0, 0, fmt.Errorf("registry: withdraw contradicted calls: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO temp.contradicted_rebind (file_id, line, old, new)
		SELECT hf.id, h.evidence_line, h.dst_id, MIN(x.dst_id)
		  FROM contradicted_edge c
		 CROSS JOIN edges h ON h.id = c.id
		 CROSS JOIN files hf ON hf.index_run_id = h.index_run_id AND hf.path = h.evidence_file
		 CROSS JOIN symbols hd ON hd.id = h.dst_id
		 CROSS JOIN edges x INDEXED BY idx_edges_evidence
		         ON x.index_run_id = h.index_run_id AND x.evidence_file = h.evidence_file
		        AND x.evidence_line = h.evidence_line AND x.source = 'compiler'
		        AND x.kind IN ('CALLS','INSTANTIATES')
		 CROSS JOIN symbols xd ON xd.id = x.dst_id AND xd.name = hd.name
		 GROUP BY hf.id, h.evidence_line, h.dst_id
		HAVING COUNT(DISTINCT x.dst_id) = 1`); err != nil {
		return 0, 0, fmt.Errorf("registry: withdraw contradicted calls: re-bind targets: %w", err)
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE symbol_references
		   SET prior_resolution  = CASE WHEN prior_resolution = '' THEN resolution ELSE prior_resolution END,
		       resolution_source = 'compiler',
		       resolved_symbol_id = (SELECT rb.new FROM temp.contradicted_rebind rb
		                              WHERE rb.file_id = symbol_references.file_id
		                                AND rb.line = symbol_references.start_line
		                                AND rb.old = symbol_references.resolved_symbol_id)
		 WHERE id IN (
		       SELECT r.id FROM temp.contradicted_rebind rb
		         CROSS JOIN symbol_references r INDEXED BY idx_refs_file_line
		                ON r.file_id = rb.file_id AND r.start_line = rb.line
		        WHERE r.index_run_id = ? AND r.resolution = 'resolved'
		          AND r.resolved_symbol_id = rb.old)`, runID)
	if err != nil {
		return 0, 0, fmt.Errorf("registry: withdraw contradicted calls: re-bind: %w", err)
	}
	if n, e := res.RowsAffected(); e == nil {
		rebound = int(n)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS temp.contradicted_rebind`); err != nil {
		return 0, 0, fmt.Errorf("registry: withdraw contradicted calls: %w", err)
	}
	res, err = tx.ExecContext(ctx, `DELETE FROM edges WHERE id IN (SELECT id FROM contradicted_edge)`)
	if err != nil {
		return 0, 0, fmt.Errorf("registry: withdraw contradicted calls: delete: %w", err)
	}
	if n, e := res.RowsAffected(); e == nil {
		withdrawn = int(n)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM contradicted_edge`); err != nil {
		return 0, 0, fmt.Errorf("registry: withdraw contradicted calls: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("registry: withdraw contradicted calls: commit: %w", err)
	}
	return withdrawn, rebound, nil
}
