package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sutrakam/graphlume/pkg/model"
)

// Folder roll-ups: the reads behind the `structure` tool. Each returns rows
// that are already grouped (one per file, or one per pair of files), so no
// caller walks the edge table. A 740,000-edge index is answered from
// file_edges, which sums edges to files at index time.

// FolderFile is one file of a run with its definition count.
type FolderFile struct {
	ID       int64
	Path     string
	Language string
	Symbols  int64
}

// FolderFiles lists every file of the run with the number of definitions it
// holds, from the per-file counts the index keeps (file_symbols).
func (r *Registry) FolderFiles(ctx context.Context, runID int64) ([]FolderFile, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT f.id, f.path, f.language, COALESCE(s.n, 0)
		  FROM files f LEFT JOIN file_symbols s ON s.file_id = f.id
		 WHERE f.index_run_id = ?`, runID)
	if err != nil {
		return nil, fmt.Errorf("registry: folder files: %w", err)
	}
	defer rows.Close()
	var out []FolderFile
	for rows.Next() {
		var f FolderFile
		if err := rows.Scan(&f.ID, &f.Path, &f.Language, &f.Symbols); err != nil {
			return nil, fmt.Errorf("registry: folder files: scan: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FilePairEdges is the number of relationships from one file to another, by
// confidence tier.
type FilePairEdges struct {
	SrcID, DstID             int64
	Exact, High, Medium, Low int64
}

// FileEdgeRollup reads file_edges for the given relationship kinds at or above
// the floor, summed over kind: one row per pair of files and tier, never one
// per edge. file_edges is itself the sum of the edge table by file, kept at
// index time, so on an index of 740,000 edges this reads about 50,000 rows and
// takes tens of milliseconds. The caller sums them again by folder.
func (r *Registry) FileEdgeRollup(ctx context.Context, runID int64, kinds []string, floor model.Confidence) ([]FilePairEdges, error) {
	q := `
		SELECT src_file_id, dst_file_id, confidence, sum(n)
		  FROM file_edges
		 WHERE index_run_id = ?1 AND kind IN (` + sqlList(kinds) + `)
		   AND confidence IN (` + sqlList(tiersAtLeast(floor)) + `)
		 GROUP BY 1, 2, 3`
	rows, err := r.db.QueryContext(ctx, q, runID)
	if err != nil {
		return nil, fmt.Errorf("registry: file edge rollup: %w", err)
	}
	defer rows.Close()

	var out []FilePairEdges
	for rows.Next() {
		var e FilePairEdges
		var conf string
		var n int64
		if err := rows.Scan(&e.SrcID, &e.DstID, &conf, &n); err != nil {
			return nil, fmt.Errorf("registry: file edge rollup: scan: %w", err)
		}
		switch model.Confidence(conf) {
		case model.ConfidenceExact:
			e.Exact = n
		case model.ConfidenceHigh:
			e.High = n
		case model.ConfidenceMedium:
			e.Medium = n
		default:
			e.Low = n
		}
		// Rows of one pair come together (GROUP BY src, dst, confidence), so
		// the tiers of a pair merge into the row before it.
		if i := len(out) - 1; i >= 0 && out[i].SrcID == e.SrcID && out[i].DstID == e.DstID {
			out[i].Exact += e.Exact
			out[i].High += e.High
			out[i].Medium += e.Medium
			out[i].Low += e.Low
			continue
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FilesReachedFrom returns the ids of the files that at least one of srcIDs
// has a relationship into, for the given kinds and at or above the floor.
func (r *Registry) FilesReachedFrom(ctx context.Context, runID int64, srcIDs []int64, kinds []string, floor model.Confidence) ([]int64, error) {
	if len(srcIDs) == 0 {
		return nil, nil
	}
	list, err := json.Marshal(srcIDs)
	if err != nil {
		return nil, err
	}
	q := `
		SELECT DISTINCT dst_file_id FROM file_edges
		 WHERE index_run_id = ?1
		   AND src_file_id IN (SELECT value FROM json_each(?2))
		   AND kind IN (` + sqlList(kinds) + `)
		   AND confidence IN (` + sqlList(tiersAtLeast(floor)) + `)`
	rows, err := r.db.QueryContext(ctx, q, runID, string(list))
	if err != nil {
		return nil, fmt.Errorf("registry: files reached: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("registry: files reached: scan: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// tiersAtLeast lists the confidence tiers at or above floor.
func tiersAtLeast(floor model.Confidence) []string {
	var out []string
	for _, c := range []model.Confidence{model.ConfidenceExact, model.ConfidenceHigh, model.ConfidenceMedium, model.ConfidenceLow} {
		if c.AtLeast(floor) {
			out = append(out, string(c))
		}
	}
	return out
}

// sqlList renders constants as a quoted SQL list. Its inputs are the fixed
// kind and tier names of this package's callers, never user text; a quote in
// one is doubled regardless.
func sqlList(vals []string) string {
	q := make([]string, len(vals))
	for i, v := range vals {
		q[i] = "'" + strings.ReplaceAll(v, "'", "''") + "'"
	}
	return strings.Join(q, ",")
}
