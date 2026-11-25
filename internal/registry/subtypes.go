package registry

import (
	"context"
	"fmt"
	"strings"

	"github.com/sutrakam/graphlume/pkg/model"
)

// SubtypeHit is one type that reaches a root through extends or implements
// edges, with how far down it is.
//
// The embedded EdgeHit is the edge that connects it to its parent on the
// shortest path: SrcName is the subtype, DstName the type it extends or
// implements directly, which is the root itself at depth 1 and another subtype
// below that.
type SubtypeHit struct {
	EdgeHit

	// Depth is 1 for a direct subtype, 2 for a subtype of a direct subtype.
	Depth int

	// PathConfidence is the WEAKEST edge on the shortest path from the root, so
	// a type reached through a guess is never reported more strongly than that
	// guess. The edge's own tier is EdgeHit.Confidence.
	PathConfidence model.Confidence
}

// Subtypes is a page of the subtypes of one type.
type Subtypes struct {
	// Hits are the rows of this page, direct subtypes first.
	Hits []SubtypeHit

	// Total is how many rows the query pages over: every subtype, or the direct
	// ones when the query asked for those alone.
	Total int

	// Direct and All are the two counts the first line states: the types that
	// extend or implement the root themselves, and every type that reaches it.
	Direct int
	All    int

	// Capped is true when the walk stopped at its row bound, so All is a floor.
	Capped bool

	// Local counts the extends or implements edges whose SOURCE is a function,
	// a method or a test and not a type: an anonymous class expression or a
	// class declared inside a function body, which the extractor attributes to
	// the function that contains it. They are subtypes of the root and they are
	// not types, so no row carries them; LocalHits are the first few, with the
	// enclosing function as SrcName. Local is a floor when Capped.
	Local     int
	LocalHits []SubtypeHit
}

// nonTypeKinds are the kinds of symbol that can hold a class body but are not
// themselves a type. A hierarchy edge whose source is one of them belongs to a
// local or anonymous type.
const nonTypeKinds = `('function','method','test')`

// maxLocalHits is how many such edges are returned as examples.
const maxLocalHits = 5

const (
	// maxSubtypeDepth bounds how far below the root the walk goes. A hierarchy
	// deeper than this is a chain nobody reads, and the bound is what makes a
	// cycle in bad data terminate.
	maxSubtypeDepth = 8

	// maxSubtypeRows bounds the walk. A root with tens of thousands of subtypes
	// (a language's base object) is answered with a floor, not a long wait.
	maxSubtypeRows = 20000
)

// SubtypeClosure returns the types that extend or implement root, directly or
// through other types, as one page ordered direct first, then by the strength
// of the path, the file and the line.
//
// Each type appears once, at its SHORTEST depth, on its strongest shortest path.
// directOnly pages over the direct subtypes alone and still counts the rest,
// because a first line that says "4" without saying that 100 more exist behind
// them is the answer this exists to replace.
func (r *Registry) SubtypeClosure(ctx context.Context, runID, root int64, filter EdgeFilter, directOnly bool, limit, offset int) (Subtypes, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	kinds := filter.Kinds
	if len(kinds) == 0 {
		kinds = []string{string(model.EdgeImplements), string(model.EdgeExtends)}
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",")
	rank := fmt.Sprintf(confidenceRankCase, "e.confidence")
	floor := ""
	var floorArgs []any
	if fr := filter.MinConfidence.Rank(); fr > 0 {
		floor = ` AND ` + rank + ` >= ?`
		floorArgs = []any{fr}
	}

	// The same predicate seeds the walk and extends it: an edge of one of the
	// kinds, at or above the floor, in this run.
	var args []any
	args = append(args, runID, root)
	for _, k := range kinds {
		args = append(args, k)
	}
	args = append(args, floorArgs...)
	args = append(args, runID)
	for _, k := range kinds {
		args = append(args, k)
	}
	args = append(args, floorArgs...)
	args = append(args, maxSubtypeDepth, maxSubtypeRows+1)

	with := subtypeWalkSQL(ph, rank, floor)

	var out Subtypes
	if err := r.db.QueryRowContext(ctx, with+`
		SELECT COUNT(*), COALESCE(SUM(depth = 1), 0) FROM best WHERE id <> ?`,
		append(append([]any{}, args...), root)...).Scan(&out.All, &out.Direct); err != nil {
		return Subtypes{}, fmt.Errorf("registry: subtypes of symbol %d: %w", root, err)
	}
	if err := r.db.QueryRowContext(ctx, with+`
		SELECT COUNT(*) FROM local WHERE id <> ?`,
		append(append([]any{}, args...), root)...).Scan(&out.Local); err != nil {
		return Subtypes{}, fmt.Errorf("registry: local subtypes of symbol %d: %w", root, err)
	}
	if out.Local > 0 {
		lq, err := r.db.QueryContext(ctx, with+`
			SELECT e.src_id, src.name, srcf.path, e.dst_id, dst.name,
			       e.kind, e.confidence, e.source, COALESCE(e.detail,''),
			       e.evidence_file, e.evidence_line, e.evidence_col, local.depth, local.crank
			  FROM local
			  JOIN edges e      ON e.id = local.eid
			  JOIN symbols src  ON src.id = e.src_id
			  JOIN files   srcf ON srcf.id = src.file_id
			  JOIN symbols dst  ON dst.id = e.dst_id
			 WHERE local.id <> ?
			 ORDER BY srcf.path, e.evidence_line, e.id
			 LIMIT ?`,
			append(append([]any{}, args...), root, maxLocalHits)...)
		if err != nil {
			return Subtypes{}, fmt.Errorf("registry: local subtypes of symbol %d: %w", root, err)
		}
		for lq.Next() {
			var h SubtypeHit
			var crank int
			if err := lq.Scan(&h.SrcID, &h.SrcName, &h.SrcPath, &h.DstID, &h.DstName,
				&h.Kind, &h.Confidence, &h.Source, &h.Detail,
				&h.EvidenceFile, &h.EvidenceLine, &h.EvidenceCol, &h.Depth, &crank); err != nil {
				_ = lq.Close()
				return Subtypes{}, fmt.Errorf("registry: local subtypes of symbol %d: scan: %w", root, err)
			}
			h.PathConfidence = confidenceOfRank(crank)
			out.LocalHits = append(out.LocalHits, h)
		}
		err = lq.Err()
		_ = lq.Close()
		if err != nil {
			return Subtypes{}, err
		}
	}
	out.Capped = out.All+out.Local >= maxSubtypeRows
	out.Total = out.All
	if directOnly {
		out.Total = out.Direct
	}
	if out.Total == 0 || offset >= out.Total {
		return out, nil
	}

	only := ""
	if directOnly {
		only = ` AND best.depth = 1`
	}
	q, err := r.db.QueryContext(ctx, with+`
		SELECT e.src_id, src.name, srcf.path, e.dst_id, dst.name,
		       e.kind, e.confidence, e.source, COALESCE(e.detail,''),
		       e.evidence_file, e.evidence_line, e.evidence_col, best.depth, best.crank
		  FROM best
		  JOIN edges e      ON e.id = best.eid
		  JOIN symbols src  ON src.id = e.src_id
		  JOIN files   srcf ON srcf.id = src.file_id
		  JOIN symbols dst  ON dst.id = e.dst_id
		 WHERE best.id <> ?`+only+`
		 ORDER BY best.depth, best.crank DESC, srcf.path, e.evidence_line, e.id
		 LIMIT ? OFFSET ?`,
		append(append([]any{}, args...), root, limit, offset)...)
	if err != nil {
		return Subtypes{}, fmt.Errorf("registry: subtypes of symbol %d: %w", root, err)
	}
	defer q.Close()
	for q.Next() {
		var h SubtypeHit
		var crank int
		if err := q.Scan(&h.SrcID, &h.SrcName, &h.SrcPath, &h.DstID, &h.DstName,
			&h.Kind, &h.Confidence, &h.Source, &h.Detail,
			&h.EvidenceFile, &h.EvidenceLine, &h.EvidenceCol, &h.Depth, &crank); err != nil {
			return Subtypes{}, fmt.Errorf("registry: subtypes of symbol %d: scan: %w", root, err)
		}
		h.PathConfidence = confidenceOfRank(crank)
		out.Hits = append(out.Hits, h)
	}
	return out, q.Err()
}

// confidenceOfRank is the inverse of model.Confidence.Rank.
func confidenceOfRank(n int) model.Confidence {
	switch n {
	case 4:
		return model.ConfidenceExact
	case 3:
		return model.ConfidenceHigh
	case 2:
		return model.ConfidenceMedium
	case 1:
		return model.ConfidenceLow
	}
	return ""
}

// subtypeWalkSQL is the WITH clause every query of SubtypeClosure starts from:
// the walk below a root, each type once at its shortest depth, split into the
// types (best) and the functions that hold a local type (local). ph is the
// placeholder list for the edge kinds, rank the confidence rank expression and
// floor an optional predicate on it.
//
// The recursive step is written "sub CROSS JOIN edges" on purpose. CROSS
// JOIN fixes the join order in SQLite, so each type taken off the queue
// looks its subtypes up through idx_edges_dst. Written as "edges JOIN sub"
// the planner read every edge of the run once per queued type: on VS Code
// (450,986 edges, 5,819 types below IDisposable) one answer was still
// running after 30 minutes, and takes 0.1 s this way.
func subtypeWalkSQL(ph, rank, floor string) string {
	return `
		WITH RECURSIVE sub(id, depth, crank, eid) AS (
			SELECT e.src_id, 1, ` + rank + `, e.id
			  FROM edges e
			 WHERE e.index_run_id = ? AND e.dst_id = ? AND e.kind IN (` + ph + `)` + floor + `
			UNION
			SELECT e.src_id, sub.depth + 1, MIN(sub.crank, ` + rank + `), e.id
			  FROM sub CROSS JOIN edges e ON e.dst_id = sub.id
			 WHERE e.index_run_id = ? AND e.kind IN (` + ph + `)` + floor + ` AND sub.depth < ?
			LIMIT ?
		), ranked AS (
			SELECT id, depth, crank, eid FROM (
				SELECT id, depth, crank, eid,
				       ROW_NUMBER() OVER (PARTITION BY id ORDER BY depth, crank DESC, eid) AS rn
				  FROM sub
			) WHERE rn = 1
		), best AS (
			SELECT r.id, r.depth, r.crank, r.eid
			  FROM ranked r JOIN symbols t ON t.id = r.id
			 WHERE t.kind NOT IN ` + nonTypeKinds + `
		), local AS (
			SELECT r.id, r.depth, r.crank, r.eid
			  FROM ranked r JOIN symbols t ON t.id = r.id
			 WHERE t.kind IN ` + nonTypeKinds + `
		)`
}
