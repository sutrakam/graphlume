package registry

import (
	"context"
	"fmt"

	"github.com/sutrakam/graphlume/pkg/model"
)

// This file holds the one read behind community detection
// (internal/capability/communities): the run's relationships folded down to
// "this file points at that file, this many times".
//
// The fold happens in SQL, and the result is streamed. A caller that wants to
// group files has no use for individual edges, and there are far fewer file
// pairs than edges (this repository: about 20,000 edges, under 3,000 pairs).
// Handing over the edges so the caller can count them would cost the memory of
// the whole graph to produce an answer one page long.

// FileLink is the relationships of one confidence that start in one file and
// end in another, counted. It is directed: a link from a to b and one from b
// to a are separate rows, and so are the high- and low-confidence
// relationships between the same two files.
type FileLink struct {
	SrcFileID  int64
	DstFileID  int64
	Confidence model.Confidence
	// Relationships is how many edges the row stands for, of every kind
	// except CONTAINS.
	Relationships int
}

// fileLinksSQL folds a run's edges into file-to-file links.
//
// The scan is of every edge in the run, which is the size of the question: a
// partition of the whole graph has to read the whole graph once. What is
// pinned is HOW it is read: each edge once. Edges are reached through
// idx_edges_conf (index_run_id, confidence), one range for the run, and each
// endpoint is then one primary-key probe. The index is named and the join
// order forced (CROSS JOIN) so the plan does not depend on whether the
// database has statistics; fileStructureSQL records what the planner chose
// for a query of this shape when it was left to decide.
// TestFileLinksReadsEachEdgeOnce pins the plan. Measured on the scale
// fixture, the fold costs about two microseconds an edge.
//
// Confidence is returned, not filtered on. Which tiers count as a link is the
// caller's policy, and a caller that leaves some out has to be able to say how
// many: a filter here would make that a second scan.
//
// CONTAINS records nesting, not use, DOCUMENTS a mention in prose, and an
// edge inside one file says nothing about how files relate, so all are left
// out.
const fileLinksSQL = `
	SELECT ss.file_id, ds.file_id, e.confidence, COUNT(*)
	  FROM edges e INDEXED BY idx_edges_conf
	  CROSS JOIN symbols ss ON ss.id = e.src_id
	  CROSS JOIN symbols ds ON ds.id = e.dst_id
	 WHERE e.index_run_id = ?
	   AND e.kind NOT IN ('CONTAINS', 'DOCUMENTS')
	   AND ss.file_id <> ds.file_id
	 GROUP BY ss.file_id, ds.file_id, e.confidence
	 ORDER BY ss.file_id, ds.file_id, e.confidence`

// EachFileLink calls fn once for every (source file, destination file,
// confidence) in runID joined by at least one relationship, in ascending order
// of those three, so the rows for one pair of files arrive together.
//
// Streamed, so the caller decides what to keep: memory here does not grow with
// the number of links. Returning an error from fn stops the scan and is
// returned unchanged, which is how a caller enforces a bound of its own.
//
// fn runs while the cursor is open and MUST NOT call back into the registry: a
// store opened with a single connection would wait on itself.
func (r *Registry) EachFileLink(ctx context.Context, runID int64, fn func(FileLink) error) error {
	rows, err := r.db.QueryContext(ctx, fileLinksSQL, runID)
	if err != nil {
		return fmt.Errorf("registry: file links for run %d: %w", runID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var l FileLink
		var conf string
		if err := rows.Scan(&l.SrcFileID, &l.DstFileID, &conf, &l.Relationships); err != nil {
			return fmt.Errorf("registry: scan file link: %w", err)
		}
		l.Confidence = model.Confidence(conf)
		if err := fn(l); err != nil {
			return err
		}
	}
	return rows.Err()
}
