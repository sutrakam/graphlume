package graph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/pkg/model"
)

// Neighbour is one directly connected symbol and the edge that connects it.
//
// Unlike Node this carries full evidence. Depth-1 results are small and are
// what an agent shows a human, so the file and line that justify the
// relationship are worth the extra columns — a relationship the caller cannot
// verify is an assertion.
type Neighbour struct {
	SymbolID   int64            `json:"symbol_id"`
	Kind       model.EdgeKind   `json:"kind"`
	Confidence model.Confidence `json:"confidence"`
	Evidence   model.Evidence   `json:"evidence"`
}

// NeighboursResult carries the neighbours of a symbol and whether the result was truncated.
type NeighboursResult struct {
	Neighbours      []Neighbour `json:"neighbours"`
	Truncated       bool        `json:"truncated"`
	TruncatedReason string      `json:"truncated_reason,omitempty"`
}

// ErrNeighboursTruncated is reported when neighbours query exceeds the safety ceiling.
var ErrNeighboursTruncated = errors.New("graph: neighbours capped at limit")

// NeighboursDetailed returns the symbols one edge away from symbolID with explicit truncation state.
func (g *Graph) NeighboursDetailed(
	ctx context.Context,
	symbolID int64,
	dir Direction,
	kinds []model.EdgeKind,
	minConf model.Confidence,
) (NeighboursResult, error) {
	scans, err := dir.scans()
	if err != nil {
		return NeighboursResult{}, err
	}
	runID, err := g.run(ctx)
	if err != nil {
		return NeighboursResult{}, err
	}
	confs := allowedConfidences(minConf)

	var out []Neighbour
	scanLimit := DefaultMaxNeighbours + 1
	for _, scan := range scans {
		args := make([]any, 0, 2+len(kinds)+len(confs))
		args = append(args, runID, symbolID)
		for _, k := range kinds {
			args = append(args, string(k))
		}
		for _, c := range confs {
			args = append(args, string(c))
		}

		rows, err := g.db.QueryContext(ctx, neighbourQuery(scan, len(kinds), len(confs)), args...)
		if err != nil {
			return NeighboursResult{}, fmt.Errorf("graph: neighbours of %d: %w", symbolID, err)
		}
		batch, err := scanNeighbours(rows, scanLimit-len(out))
		if err != nil {
			return NeighboursResult{}, err
		}
		out = append(out, batch...)
		if len(out) >= scanLimit {
			break
		}
	}

	// Ordering happens here before truncation to guarantee deterministic survival.
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.SymbolID != b.SymbolID:
			return a.SymbolID < b.SymbolID
		case a.Kind != b.Kind:
			return a.Kind < b.Kind
		case a.Evidence.FilePath != b.Evidence.FilePath:
			return a.Evidence.FilePath < b.Evidence.FilePath
		default:
			return a.Evidence.Range.Start.Line < b.Evidence.Range.Start.Line
		}
	})

	var truncated bool
	var truncatedReason string
	if len(out) > DefaultMaxNeighbours {
		truncated = true
		truncatedReason = fmt.Sprintf("neighbours capped at limit of %d", DefaultMaxNeighbours)
		out = out[:DefaultMaxNeighbours]
		slog.Warn("graph: neighbours query truncated at limit", "symbol_id", symbolID, "limit", DefaultMaxNeighbours)
	}

	return NeighboursResult{
		Neighbours:      out,
		Truncated:       truncated,
		TruncatedReason: truncatedReason,
	}, nil
}

// Neighbours returns the symbols one edge away from symbolID.
//
// This is a separate path from Traverse because depth 1 is overwhelmingly the
// most common request and does not need any of the traversal machinery: no
// visited set, no frontier, no path arithmetic. It is a single indexed lookup
// on idx_edges_src or idx_edges_dst.
func (g *Graph) Neighbours(
	ctx context.Context,
	symbolID int64,
	dir Direction,
	kinds []model.EdgeKind,
	minConf model.Confidence,
) ([]Neighbour, error) {
	res, err := g.NeighboursDetailed(ctx, symbolID, dir, kinds, minConf)
	return res.Neighbours, err
}

// Callers returns the symbols that call symbolID — the direction that answers
// "who breaks if I change this".
func (g *Graph) Callers(ctx context.Context, symbolID int64, minConf model.Confidence) ([]Neighbour, error) {
	return g.Neighbours(ctx, symbolID, Incoming, []model.EdgeKind{model.EdgeCalls}, minConf)
}

// Callees returns the symbols symbolID calls.
func (g *Graph) Callees(ctx context.Context, symbolID int64, minConf model.Confidence) ([]Neighbour, error) {
	return g.Neighbours(ctx, symbolID, Outgoing, []model.EdgeKind{model.EdgeCalls}, minConf)
}

// neighbourQuery selects the far side of the edge plus its provenance.
//
// There is deliberately no ORDER BY, and the index is named. Both are the same
// lesson learned the expensive way: with an `ORDER BY src_id` on the incoming
// query, SQLite (which has no ANALYZE statistics to work from) preferred
// idx_edges_src because it satisfied the sort, and scanned all 500k edges in
// the run instead of seeking 1000 rows through idx_edges_dst — 235ms for a
// query that should take microseconds. Callers get a stable order from a Go
// sort over a result set that is small by construction.
func neighbourQuery(dir edgeDir, nKinds, nConfs int) string {
	var b strings.Builder
	b.Grow(240 + 2*(nKinds+nConfs))
	b.WriteString("SELECT ")
	if dir == dirOut {
		b.WriteString("dst_id")
	} else {
		b.WriteString("src_id")
	}
	b.WriteString(", kind, confidence, evidence_file, evidence_line, evidence_col, source," +
		" COALESCE(detail,'') FROM edges INDEXED BY ")
	if dir == dirOut {
		b.WriteString("idx_edges_src WHERE index_run_id = ? AND src_id = ?")
	} else {
		b.WriteString("idx_edges_dst WHERE index_run_id = ? AND dst_id = ?")
	}
	if nKinds > 0 {
		b.WriteString(" AND kind IN (")
		writePlaceholders(&b, nKinds)
		b.WriteString(")")
	}
	if nConfs > 0 {
		b.WriteString(" AND confidence IN (")
		writePlaceholders(&b, nConfs)
		b.WriteString(")")
	}
	return b.String()
}

// DefaultMaxNeighbours is a defensive memory ceiling on single-node neighbour scans
// to prevent out-of-memory states on extreme hub symbols (e.g. Object, fmt.Stringer).
const DefaultMaxNeighbours = 50_000

func scanNeighbours(rows *sql.Rows, limit int) ([]Neighbour, error) {
	defer rows.Close()
	var out []Neighbour
	for rows.Next() {
		if limit > 0 && len(out) >= limit {
			break
		}
		var n Neighbour
		var kind, conf, source string
		if err := rows.Scan(&n.SymbolID, &kind, &conf,
			&n.Evidence.FilePath, &n.Evidence.Range.Start.Line, &n.Evidence.Range.Start.Col,
			&source, &n.Evidence.Detail); err != nil {
			return nil, fmt.Errorf("graph: scan neighbour: %w", err)
		}
		n.Kind = model.EdgeKind(kind)
		n.Confidence = model.Confidence(conf)
		n.Evidence.Source = model.SourceType(source)
		// The edge records a single position, so the evidence range is a point.
		n.Evidence.Range.End = n.Evidence.Range.Start
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("graph: read neighbours: %w", err)
	}
	return out, nil
}
