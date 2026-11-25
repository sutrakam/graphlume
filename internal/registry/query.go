package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/sutrakam/graphlume/pkg/model"
)

// SymbolHit is a symbol as returned to a query caller.
type SymbolHit struct {
	ID         int64
	Name       string
	FQN        string
	Kind       string
	FilePath   string
	StartLine  int
	EndLine    int
	Confidence string
	Source     string
	Exported   bool
}

// FindSymbolsByName looks up definitions by exact name.
//
// Exact rather than fuzzy, deliberately. An agent that asks for
// "TransactionService" wants that type, not the forty symbols containing the
// substring; fuzzy matching here would flood a context budget with near-misses.
// search exists for the exploratory case.
func (r *Registry) FindSymbolsByName(ctx context.Context, runID int64, name, kind string, limit int) ([]SymbolHit, error) {
	if limit <= 0 {
		limit = 20
	}

	q := `
		SELECT s.id, s.name, s.fqn, s.kind, f.path, s.start_line, s.end_line,
		       s.confidence, s.source, s.exported
		  FROM symbols s
		  JOIN files f ON f.id = s.file_id
		 WHERE s.index_run_id = ? AND s.name = ? AND s.kind <> 'file'`
	args := []any{runID, name}
	if kind != "" {
		q += ` AND s.kind = ?`
		args = append(args, kind)
	}
	// Exported symbols first: a caller asking by name almost always means the
	// public one, and ordering is stable so repeated calls agree.
	q += ` ORDER BY s.exported DESC, f.path, s.start_line LIMIT ?`
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("registry: find symbols %q: %w", name, err)
	}
	defer rows.Close()

	var out []SymbolHit
	for rows.Next() {
		var h SymbolHit
		var exported int
		if err := rows.Scan(&h.ID, &h.Name, &h.FQN, &h.Kind, &h.FilePath,
			&h.StartLine, &h.EndLine, &h.Confidence, &h.Source, &exported); err != nil {
			return nil, err
		}
		h.Exported = exported == 1
		out = append(out, h)
	}
	return out, rows.Err()
}

// EdgeHit is a relationship with both endpoints named.
type EdgeHit struct {
	SrcID   int64
	SrcName string
	// SrcQualified is the source as a person names it: Owner.member for a
	// method or a nested definition (Writer.AddEdge), the bare name for a
	// top-level one. It is read from the symbol's qualified name, which the
	// index already holds. An answer that printed the bare name left the
	// reader to guess the owner, and one agent in the benchmark guessed
	// Registry.AddEdge for Writer.AddEdge.
	SrcQualified string
	SrcPath      string
	DstID        int64
	DstName      string
	Kind         string
	Confidence   string
	Source       string
	Detail       string
	EvidenceFile string
	EvidenceLine int
	// EvidenceCol is edges.evidence_col. It is a HINT, not a byte offset:
	// tree-sitter edges record the start of the whole call expression (measured:
	// col 55 for `s.repo.CreateOrder(id)` whose name starts at byte 62), and
	// SCIP edges record the document's PositionEncoding, UTF-16 units for the
	// JVM and TypeScript indexers. A consumer must check what is at the column
	// before relying on it; internal/edit.Locate does.
	EvidenceCol int
}

// EdgeFilter narrows an edge query at the SOURCE, before any LIMIT is applied.
//
// This exists so a caller asking for a subset — one kind, a confidence floor —
// gets that subset filled up to its cap, rather than an unfiltered cap's worth
// of rows with the subset picked out of them afterward. Filtering after a LIMIT
// can silently empty a real answer: a symbol with a handful of CALLS edges and
// a flood of same-or-higher-confidence REFERENCES edges can have every CALLS
// edge crowded out of the capped set by REFERENCES edges that merely sort
// earlier, even though nothing about the REFERENCES edges is more relevant to
// a caller who asked for calls only. Filtering before LIMIT means the cap is
// spent only on rows the caller actually wants.
type EdgeFilter struct {
	// Kinds restricts results to these edge kinds (e.g. "CALLS"). Empty means
	// every kind.
	Kinds []string
	// MinConfidence is a floor edges must meet or exceed, ranked via
	// model.Confidence.Rank() (exact=4 down to low=1). The zero value ("")
	// ranks 0 and means no floor. Typed rather than a raw int so a caller
	// cannot pass an out-of-vocabulary rank (a bare 7 means nothing); the
	// rank is computed once, here, at query time.
	MinConfidence model.Confidence
	// Within, when set, keeps only edges whose evidence (the call site) is in
	// a file under this path: a directory ("src/") or one file. It narrows an
	// answer to the part of a repository a question is about, so that rows from
	// sub-projects, benchmarks or fixtures the question excludes neither fill
	// the page nor weaken its verdict.
	Within string
}

// withinSQL is the predicate for EdgeFilter.Within on the edges alias e, and
// its arguments; empty when no path was given.
func withinSQL(within string) (string, []any) {
	return withinColumnSQL("e.evidence_file", within)
}

// withinColumnSQL is the EdgeFilter.Within predicate on any path column.
func withinColumnSQL(col, within string) (string, []any) {
	w := strings.TrimPrefix(strings.TrimSpace(within), "./")
	if w == "" {
		return "", nil
	}
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(w)
	if strings.HasSuffix(w, "/") {
		return ` AND ` + col + ` LIKE ? ESCAPE '\'`, []any{esc + "%"}
	}
	return ` AND (` + col + ` = ? OR ` + col + ` LIKE ? ESCAPE '\')`, []any{w, esc + "/%"}
}

// confidenceRankCase is a SQL CASE expression ranking a confidence column
// strongest to weakest as an integer, matching the order of
// pkg/model.Confidence.Rank (exact=4 down to unrecognised=0). It exists here,
// not as a call into that method, because e.confidence is a bare SQL text
// column: ranking it for ORDER BY has to happen in SQL regardless of how the
// WHERE floor is expressed. internal/registry is free to import pkg/model —
// it already does, in clientcalls.go, candidates.go and completeness.go, and
// per docs/development.md §1 this package is the adapter that returns pkg/model types —
// so nothing here duplicates this table to avoid that import; it duplicates
// it because ORDER BY needs a rank on a SQL column no matter what. The WHERE
// floor itself does NOT go through this CASE a second time: EdgeFilter.
// MinConfidence.Rank() is computed once, in Go, at query time, and bound
// directly as an integer, so a new confidence tier can never diverge between
// this table and pkg/model.Confidence.Rank for the WHERE floor — see
// TestConfidenceRankCaseMatchesModelRank, which pins the ORDER BY side too.
const confidenceRankCase = `CASE %s
		            WHEN 'exact'  THEN 4
		            WHEN 'high'   THEN 3
		            WHEN 'medium' THEN 2
		            WHEN 'low'    THEN 1
		            ELSE 0 END`

// IncomingEdges returns relationships pointing AT a symbol — its callers and
// referrers.
//
// Ordered by confidence so the strongest evidence appears first. An agent
// reading a truncated list should see the results most likely to be true, not
// an arbitrary prefix.
func (r *Registry) IncomingEdges(ctx context.Context, runID, symbolID int64, limit int, filter EdgeFilter) ([]EdgeHit, error) {
	return r.edgesFor(ctx, runID, symbolID, limit, true, filter)
}

// OutgoingEdges returns relationships FROM a symbol — what it calls.
func (r *Registry) OutgoingEdges(ctx context.Context, runID, symbolID int64, limit int, filter EdgeFilter) ([]EdgeHit, error) {
	return r.edgesFor(ctx, runID, symbolID, limit, false, filter)
}

func (r *Registry) edgesFor(ctx context.Context, runID, symbolID int64, limit int, incoming bool, filter EdgeFilter) ([]EdgeHit, error) {
	if limit <= 0 {
		limit = 50
	}

	// Both directions are covered by an index — idx_edges_src and
	// idx_edges_dst — so neither is a table scan.
	where := "e.dst_id = ?"
	if !incoming {
		where = "e.src_id = ?"
	}

	args := []any{runID, symbolID}

	q := `
		SELECT e.src_id, src.name, COALESCE(src.fqn,''), srcf.path, e.dst_id, dst.name,
		       e.kind, e.confidence, e.source, COALESCE(e.detail,''),
		       e.evidence_file, e.evidence_line, e.evidence_col
		  FROM edges e
		  JOIN symbols src  ON src.id = e.src_id
		  JOIN files   srcf ON srcf.id = src.file_id
		  JOIN symbols dst  ON dst.id = e.dst_id
		 WHERE e.index_run_id = ? AND ` + where

	if len(filter.Kinds) > 0 {
		placeholders := make([]string, len(filter.Kinds))
		for i, k := range filter.Kinds {
			placeholders[i] = "?"
			args = append(args, k)
		}
		q += ` AND e.kind IN (` + strings.Join(placeholders, ",") + `)`
	}

	if rank := filter.MinConfidence.Rank(); rank > 0 {
		q += ` AND ` + fmt.Sprintf(confidenceRankCase, "e.confidence") + ` >= ?`
		args = append(args, rank)
	}
	if w, wargs := withinSQL(filter.Within); w != "" {
		q += w
		args = append(args, wargs...)
	}

	q += ` ORDER BY ` + fmt.Sprintf(confidenceRankCase, "e.confidence") + ` DESC,
		          srcf.path, e.evidence_line
		 LIMIT ?`
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("registry: edges for symbol %d: %w", symbolID, err)
	}
	defer rows.Close()

	var out []EdgeHit
	for rows.Next() {
		var e EdgeHit
		var fqn string
		if err := rows.Scan(&e.SrcID, &e.SrcName, &fqn, &e.SrcPath, &e.DstID, &e.DstName,
			&e.Kind, &e.Confidence, &e.Source, &e.Detail,
			&e.EvidenceFile, &e.EvidenceLine, &e.EvidenceCol); err != nil {
			return nil, err
		}
		e.SrcQualified = qualifiedName(e.SrcName, fqn, e.SrcPath)
		out = append(out, e)
	}
	return out, rows.Err()
}

// IncomingEdgeCount reports how many relationships point AT a symbol and pass
// filter, excluding any edge whose kind appears in excludeKinds — a
// COUNT(*), not a row fetch.
//
// This exists so a caller can report how many further relationships exist
// beyond a capped, kind-restricted fetch without that report saturating at
// the same cap the fetch used: fetching rows only to count and discard them
// caps the count at whatever LIMIT the fetch used, so a note built from it
// understates an arbitrarily large true total as if it were exact. A
// COUNT(*) has no such cap — it is exact regardless of how many rows match.
func (r *Registry) IncomingEdgeCount(ctx context.Context, runID, symbolID int64, filter EdgeFilter, excludeKinds []string) (int, error) {
	where := `e.index_run_id = ? AND e.dst_id = ?`
	args := []any{runID, symbolID}

	if len(filter.Kinds) > 0 {
		placeholders := make([]string, len(filter.Kinds))
		for i, k := range filter.Kinds {
			placeholders[i] = "?"
			args = append(args, k)
		}
		where += ` AND e.kind IN (` + strings.Join(placeholders, ",") + `)`
	}
	if len(excludeKinds) > 0 {
		placeholders := make([]string, len(excludeKinds))
		for i, k := range excludeKinds {
			placeholders[i] = "?"
			args = append(args, k)
		}
		where += ` AND e.kind NOT IN (` + strings.Join(placeholders, ",") + `)`
	}
	if rank := filter.MinConfidence.Rank(); rank > 0 {
		where += ` AND ` + fmt.Sprintf(confidenceRankCase, "e.confidence") + ` >= ?`
		args = append(args, rank)
	}
	if w, wargs := withinSQL(filter.Within); w != "" {
		where += w
		args = append(args, wargs...)
	}

	var n int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM edges e WHERE `+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("registry: count incoming edges for symbol %d: %w", symbolID, err)
	}
	return n, nil
}

// OutgoingEdgeCounts reports, for each symbol in ids, how many relationships
// leave it and pass filter: one grouped read for many symbols, where
// OutgoingEdges is one read per symbol. A symbol with none is absent from the
// map. The count is of edge rows, so it is never below the number of rows
// OutgoingEdges could return for that symbol.
func (r *Registry) OutgoingEdgeCounts(ctx context.Context, runID int64, ids []int64, filter EdgeFilter) (map[int64]int, error) {
	out := make(map[int64]int, len(ids))
	const chunk = 500
	for lo := 0; lo < len(ids); lo += chunk {
		part := ids[lo:min(lo+chunk, len(ids))]
		args := []any{runID}
		marks := make([]string, len(part))
		for i, id := range part {
			marks[i] = "?"
			args = append(args, id)
		}
		where := `e.index_run_id = ? AND e.src_id IN (` + strings.Join(marks, ",") + `)`
		if len(filter.Kinds) > 0 {
			placeholders := make([]string, len(filter.Kinds))
			for i, k := range filter.Kinds {
				placeholders[i] = "?"
				args = append(args, k)
			}
			where += ` AND e.kind IN (` + strings.Join(placeholders, ",") + `)`
		}
		if rank := filter.MinConfidence.Rank(); rank > 0 {
			where += ` AND ` + fmt.Sprintf(confidenceRankCase, "e.confidence") + ` >= ?`
			args = append(args, rank)
		}
		if w, wargs := withinSQL(filter.Within); w != "" {
			where += w
			args = append(args, wargs...)
		}
		rows, err := r.db.QueryContext(ctx, `SELECT e.src_id, COUNT(*) FROM edges e WHERE `+where+` GROUP BY e.src_id`, args...)
		if err != nil {
			return nil, fmt.Errorf("registry: count outgoing edges: %w", err)
		}
		for rows.Next() {
			var id int64
			var n int
			if err := rows.Scan(&id, &n); err != nil {
				_ = rows.Close()
				return nil, err
			}
			out[id] = n
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// LanguageCounts reports indexed files per language, for project overview.
func (r *Registry) LanguageCounts(ctx context.Context, runID int64) (map[string]int, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT language, count(*) FROM files
		 WHERE index_run_id = ? AND language <> ''
		 GROUP BY language ORDER BY count(*) DESC`, runID)
	if err != nil {
		return nil, fmt.Errorf("registry: language counts: %w", err)
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var lang string
		var n int
		if err := rows.Scan(&lang, &n); err != nil {
			return nil, err
		}
		out[lang] = n
	}
	return out, rows.Err()
}

// FileContent returns the complete indexed text of a file without line slicing.
//
// Like FileExcerpt, it reads from the indexed FTS copy rather than disk to
// ensure reproducibility and avoid arbitrary filesystem access.
func (r *Registry) FileContent(ctx context.Context, runID int64, path string) (string, error) {
	var content string
	err := r.db.QueryRowContext(ctx, `
		SELECT files_fts.content
		  FROM files_fts
		  JOIN files_fts_map m ON m.fts_rowid = files_fts.rowid
		  JOIN files f ON f.id = m.file_id
		 WHERE f.index_run_id = ? AND f.path = ?`, runID, path).Scan(&content)
	if err != nil {
		return "", fmt.Errorf("registry: content for %s: %w", path, err)
	}
	return content, nil
}

// FileExcerpt returns a bounded window of a file's indexed text.
//
// Reads from the FTS copy rather than the filesystem, on purpose: the index is
// the thing the caller asked about, and serving from disk could return content
// that differs from what was indexed — or a file that has since been replaced
// by a symlink. It also keeps MCP tools from touching arbitrary paths.
func (r *Registry) FileExcerpt(ctx context.Context, runID int64, path string, startLine, lines int) (string, error) {
	if lines <= 0 {
		lines = 20
	}
	var content string
	err := r.db.QueryRowContext(ctx, `
		SELECT files_fts.content
		  FROM files_fts
		  JOIN files_fts_map m ON m.fts_rowid = files_fts.rowid
		  JOIN files f ON f.id = m.file_id
		 WHERE f.index_run_id = ? AND f.path = ?`, runID, path).Scan(&content)
	if err != nil {
		return "", fmt.Errorf("registry: excerpt for %s: %w", path, err)
	}

	all := strings.Split(content, "\n")
	start := startLine - 1
	if start < 0 {
		start = 0
	}
	if start >= len(all) {
		return "", nil
	}
	end := start + lines
	if end > len(all) {
		end = len(all)
	}
	return strings.Join(all[start:end], "\n"), nil
}

// qualifiedName is a symbol's name with its owner, taken from its stored
// qualified name, which is the file's path, a colon, and the name as the
// language qualifies it (internal/registry/writer.go:Writer.AddEdge). The path
// is stripped as a prefix and not searched for, because a C++ name has colons
// of its own. Anything that is not in that form gives the bare name back.
func qualifiedName(name, fqn, path string) string {
	rest, ok := strings.CutPrefix(fqn, path+":")
	if !ok || rest == "" || path == "" {
		return name
	}
	return rest
}

// ConstructorClass returns the class a constructor builds, when symbolID is
// a constructor: a method defined inside a class or struct and named like it
// (Java, C#, C++, Kotlin, Dart), or named __init__ (Python) or constructor
// (JavaScript, TypeScript).
//
// It exists because `new X()` is recorded as an INSTANTIATES edge to the
// class, never as a CALLS edge to its constructor: asked for the callers of a
// constructor, the call graph alone answers "none" while every construction
// site is in the index. Callers of a constructor are therefore the
// INSTANTIATES edges into its class.
func (r *Registry) ConstructorClass(ctx context.Context, runID, symbolID int64) (int64, bool, error) {
	var classID int64
	err := r.db.QueryRowContext(ctx, `
		SELECT c.id
		  FROM symbols m
		  JOIN symbols c ON c.index_run_id = m.index_run_id AND c.file_id = m.file_id
		 WHERE m.index_run_id = ? AND m.id = ?
		   AND m.kind IN ('method', 'constructor', 'function')
		   AND c.kind IN ('class', 'struct')
		   AND m.fqn = c.fqn || '.' || m.name
		   AND (m.name = c.name OR m.name IN ('__init__', 'constructor'))
		 ORDER BY c.id
		 LIMIT 1`, runID, symbolID).Scan(&classID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("registry: constructor class of %d: %w", symbolID, err)
	}
	return classID, true, nil
}

// ConstructionSites returns the INSTANTIATES edges into the class a
// constructor builds, or into symbolID itself when it is a class or struct
// (the callers of a type are the sites that construct it). Nothing for any
// other symbol. The filter's confidence floor applies; its kinds are replaced.
func (r *Registry) ConstructionSites(ctx context.Context, runID, symbolID int64, limit int, filter EdgeFilter) ([]EdgeHit, error) {
	var kind string
	if err := r.db.QueryRowContext(ctx,
		`SELECT kind FROM symbols WHERE index_run_id = ? AND id = ?`, runID, symbolID).Scan(&kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("registry: kind of %d: %w", symbolID, err)
	}
	classID := symbolID
	if kind != "class" && kind != "struct" {
		id, ok, err := r.ConstructorClass(ctx, runID, symbolID)
		if err != nil || !ok {
			return nil, err
		}
		classID = id
	}
	filter.Kinds = []string{string(model.EdgeInstantiates)}
	return r.IncomingEdges(ctx, runID, classID, limit, filter)
}
