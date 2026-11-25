package registry

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/sutrakam/graphlume/pkg/model"
)

// This file holds the read queries behind the two whole-repository structural
// reports: unreferenced definitions and definitions referenced only from one
// set of files (internal/capability/deadcode), and per-file structure for a
// set of paths (internal/capability/hotspots).
//
// Both are streamed or bounded by the caller, never materialised for the whole
// run. A repository with a million symbols has a few hundred thousand
// definitions nothing points at before any filtering, and loading them to
// return the first fifty would break the bounded-memory invariant for a
// question whose answer is one page long.

// UnreferencedSymbol is a definition no relationship in the run points at.
//
// It is a CANDIDATE, not a verdict. The three boolean signals are carried
// because "nothing points at it" has three ordinary explanations that are not
// "it is dead", and the capability layer needs each one to grade the row.
type UnreferencedSymbol struct {
	ID        int64
	Kind      string
	Name      string
	FQN       string
	FilePath  string
	Language  string
	StartLine int
	EndLine   int
	Exported  bool

	// PartialParse is true when the defining file did not parse cleanly, so
	// references INSIDE that file may be missing from the index.
	PartialParse bool

	// NameUnbound is true when at least one reference spelled exactly like
	// this symbol was extracted and never bound to a definition. Any such
	// reference might be a use of this symbol; the index cannot say.
	NameUnbound bool

	// Overrides is true when the symbol declares that it implements or extends
	// something. A method reached only through its interface has no direct
	// caller by construction.
	Overrides bool

	// ConsumesEvent is true when the symbol consumes a message-broker topic or
	// queue. A listener is invoked by the broker's client library, which is a
	// caller no call expression in the repository names.
	ConsumesEvent bool
}

// unreferencedKinds are the definition kinds worth reporting.
//
// Fields, constants and variables are left out on purpose: tree-sitter tag
// queries record a reference for a call far more reliably than for a plain
// read of a name, so "nothing references this constant" is mostly a statement
// about the extractor.
var unreferencedKinds = []string{"function", "method", "class", "interface"}

// unreferencedSQL is the unreferenced-definition query for n kinds.
//
// Every probe names its index (INDEXED BY). That is not decoration. With no
// statistics in the database — a fresh index, or any index `ANALYZE` has not
// run on — the planner preferred the edges table's UNIQUE autoindex, whose
// leading column is index_run_id, and so read every edge in the run once per
// candidate symbol. On a 5,000-symbol, 20,000-edge fixture that was 8.5
// seconds; with the indexes named it is milliseconds. The scale suite found
// it (tests/scale), and TestUnreferencedSymbolsUsesItsIndexes pins the plan.
//
// The symbols table itself is read NOT INDEXED, in primary-key order, for the
// same kind of reason. Left to choose, the planner reached the run's symbols
// through idx_symbols_fqn and then sorted them by id, so every PAGE probed
// every remaining symbol in the run before LIMIT applied: the scan was
// quadratic in the number of pages, and nothing showed it until a run held
// more unreferenced definitions than one page. Read in key order, `s.id > ?`
// is a seek, ORDER BY needs no sort and LIMIT stops the statement. CROSS JOIN
// keeps symbols as the outer loop, which is what makes that true.
func unreferencedSQL(n int) string { return unreferencedSQLUnder(n, false) }

// unreferencedSQLUnder is unreferencedSQL, optionally evaluating the three
// per-definition probes only for definitions under a path prefix.
//
// The probes are correlated sub-queries and the most expensive part of a row,
// and a caller that wants one directory of a large index discards every row
// outside it before reading them. With under set, the leading
// parameters are the prefix, twice per probe, and a row outside it
// comes back with the three signals false. The ROWS returned are exactly the
// same, so a caller counting what it examined counts the same thing.
func unreferencedSQLUnder(n int, under bool) string {
	guard := func(probe string) string {
		if !under {
			return probe
		}
		return `CASE WHEN substr(f.path, 1, length(?)) = ? THEN ` + probe + ` ELSE 0 END`
	}
	return `
		SELECT s.id, s.kind, s.name, s.fqn, f.path, f.language,
		       s.start_line, s.end_line, s.exported,
		       f.parse_status <> 'ok',
		       ` + guard(nameUnboundProbe) + `,
		       ` + guard(overridesProbe) + `,
		       ` + guard(consumesEventProbe) + `
		  FROM symbols s NOT INDEXED
		 CROSS JOIN files f ON f.id = s.file_id
		 WHERE s.index_run_id = ?
		   AND s.id > ?
		   AND s.kind IN (` + placeholders(n) + `)
		   AND NOT EXISTS (SELECT 1 FROM edges e INDEXED BY idx_edges_dst
		                    WHERE ` + incomingEdge + `)
		 ORDER BY s.id
		 LIMIT ?`
}

// The fragments every definition query here shares, so two queries cannot
// grade the same definition differently. Each is correlated on `s` and each
// probe names its index; see unreferencedSQL for why.
const (
	// incomingEdge is what "something points at s" means, as the WHERE clause
	// of a probe over `edges e`: CONTAINS records nesting rather than use,
	// DOCUMENTS a backticked mention in prose (a symbol only a README names is
	// still unused code), and a symbol's edges to itself are recursion.
	incomingEdge = `e.index_run_id = s.index_run_id
		                      AND e.dst_id = s.id
		                      AND e.kind NOT IN ('CONTAINS', 'DOCUMENTS')
		                      AND e.src_id <> s.id`

	nameUnboundProbe = `EXISTS (SELECT 1 FROM symbol_references sr INDEXED BY idx_refs_name_binding
		                WHERE sr.index_run_id = s.index_run_id
		                  AND sr.name = s.name
		                  AND sr.resolution IN ('pending','ambiguous','unresolved'))`

	overridesProbe = `EXISTS (SELECT 1 FROM edges o INDEXED BY idx_edges_src
		                WHERE o.index_run_id = s.index_run_id
		                  AND o.src_id = s.id
		                  AND o.kind IN ('IMPLEMENTS','EXTENDS'))`

	// consumesEventProbe checks the destination's kind because CONSUMES is
	// also the edge a client call draws to the endpoint it requests, and a
	// function that calls an HTTP API is not thereby an entry point.
	consumesEventProbe = `EXISTS (SELECT 1 FROM edges c INDEXED BY idx_edges_src
		                WHERE c.index_run_id = s.index_run_id
		                  AND c.src_id = s.id
		                  AND c.kind = 'CONSUMES'
		                  AND EXISTS (SELECT 1 FROM symbols t
		                               WHERE t.id = c.dst_id AND t.kind = 'event'))`
)

// placeholders renders n comma-separated bind markers.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// UnreferencedSymbols returns up to limit definitions with no incoming
// relationship, in ascending id order starting after afterID.
//
// Keyset pagination on the primary key, for the reason references.go gives:
// OFFSET rescans everything it skips, and the caller here pages until it has
// collected enough rows that survive its own filters.
//
// "No incoming relationship" excludes CONTAINS, which records nesting rather
// than use, and excludes a symbol's edges to itself, so plain recursion does
// not keep a function alive.
//
// Every EXISTS probe is an index lookup: idx_edges_dst (index_run_id, dst_id,
// kind) for the incoming check, idx_refs_run_name (index_run_id, name) for
// the unbound-name check and idx_edges_src (index_run_id, src_id, kind) for
// the two outgoing checks. None scans.
func (r *Registry) UnreferencedSymbols(ctx context.Context, runID, afterID int64, limit int) ([]UnreferencedSymbol, error) {
	return r.UnreferencedSymbolsUnder(ctx, runID, afterID, limit, "")
}

// UnreferencedSymbolsUnder is UnreferencedSymbols for a caller that will use
// only the definitions whose file path begins with prefix. It returns the same
// rows in the same order; for a row outside the prefix the three signals
// (NameUnbound, Overrides, ConsumesEvent) are left false instead of being
// worked out, because that caller discards the row. An empty prefix is
// UnreferencedSymbols.
func (r *Registry) UnreferencedSymbolsUnder(ctx context.Context, runID, afterID int64, limit int, prefix string) ([]UnreferencedSymbol, error) {
	if limit <= 0 {
		limit = 500
	}
	under := prefix != ""
	args := make([]any, 0, len(unreferencedKinds)+6)
	if under {
		// Three probes, each guarded by the same prefix, come before the WHERE
		// clause's parameters in statement order.
		for i := 0; i < 3; i++ {
			args = append(args, prefix, prefix)
		}
	}
	args = append(args, runID, afterID)
	for _, k := range unreferencedKinds {
		args = append(args, k)
	}
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, unreferencedSQLUnder(len(unreferencedKinds), under), args...)
	if err != nil {
		return nil, fmt.Errorf("registry: unreferenced symbols for run %d: %w", runID, err)
	}
	defer rows.Close()

	var out []UnreferencedSymbol
	for rows.Next() {
		var u UnreferencedSymbol
		if err := rows.Scan(&u.ID, &u.Kind, &u.Name, &u.FQN, &u.FilePath, &u.Language,
			&u.StartLine, &u.EndLine, &u.Exported,
			&u.PartialParse, &u.NameUnbound, &u.Overrides, &u.ConsumesEvent); err != nil {
			return nil, fmt.Errorf("registry: scan unreferenced symbol: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UnreferencedSymbolsIn is UnreferencedSymbols restricted to the definitions in
// files whose path begins with prefix, reached through those files instead of
// through every symbol of the run.
//
// For a caller that knows how many definitions a full scan would have
// examined (StoredUnreferencedCount) and wants one directory of a large index:
// it reads that directory's definitions and nothing else. The rows are the plain
// form's rows for that prefix, in the same order, with the same signals.
func (r *Registry) UnreferencedSymbolsIn(ctx context.Context, runID, afterID int64, limit int, prefix string) ([]UnreferencedSymbol, error) {
	if limit <= 0 {
		limit = 500
	}
	args := make([]any, 0, len(unreferencedKinds)+6)
	args = append(args, runID, runID, prefix, prefix, afterID)
	for _, k := range unreferencedKinds {
		args = append(args, k)
	}
	args = append(args, limit)

	rows, err := r.db.QueryContext(ctx, `
		SELECT s.id, s.kind, s.name, s.fqn, f.path, f.language,
		       s.start_line, s.end_line, s.exported,
		       f.parse_status <> 'ok',
		       `+nameUnboundProbe+`,
		       `+overridesProbe+`,
		       `+consumesEventProbe+`
		  FROM files f
		 CROSS JOIN symbols s INDEXED BY idx_symbols_file ON s.file_id = f.id
		 WHERE f.index_run_id = ?
		   AND s.index_run_id = ?
		   AND substr(f.path, 1, length(?)) = ?
		   AND s.id > ?
		   AND s.kind IN (`+placeholders(len(unreferencedKinds))+`)
		   AND NOT EXISTS (SELECT 1 FROM edges e INDEXED BY idx_edges_dst
		                    WHERE `+incomingEdge+`)
		 ORDER BY s.id
		 LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("registry: unreferenced symbols under %q for run %d: %w", prefix, runID, err)
	}
	defer rows.Close()

	var out []UnreferencedSymbol
	for rows.Next() {
		var u UnreferencedSymbol
		if err := rows.Scan(&u.ID, &u.Kind, &u.Name, &u.FQN, &u.FilePath, &u.Language,
			&u.StartLine, &u.EndLine, &u.Exported,
			&u.PartialParse, &u.NameUnbound, &u.Overrides, &u.ConsumesEvent); err != nil {
			return nil, fmt.Errorf("registry: scan unreferenced symbol: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ConfinedSymbol is a definition that IS referenced, and only from inside one
// set of files the caller named.
//
// Like UnreferencedSymbol it is a candidate and not a verdict, and it carries
// the same signals for the same reason: "every reference the index bound
// starts in these files" leaves open every reference the index did not bind.
type ConfinedSymbol struct {
	ID        int64
	Kind      string
	Name      string
	FQN       string
	FilePath  string
	Language  string
	StartLine int
	EndLine   int
	Exported  bool

	// PartialParse, NameUnbound, Overrides and ConsumesEvent mean what they
	// mean on UnreferencedSymbol.
	PartialParse  bool
	NameUnbound   bool
	Overrides     bool
	ConsumesEvent bool

	// Referrers counts the incoming relationships, every one of which starts
	// inside the set. It counts edges, not distinct callers.
	Referrers int
}

// ConfinedPage is one window of a confined-symbol scan.
type ConfinedPage struct {
	Symbols []ConfinedSymbol

	// Examined is how many of the run's symbols, of any kind, the window
	// covered. Zero means the scan has reached the end of the run.
	Examined int

	// LastID is the id to pass as afterID for the next window.
	LastID int64
}

// confinedWindowSQL finds where a window of `limit` symbols ends. The scan is
// windowed by id rather than by a LIMIT on its result because its result is
// sparse: with a LIMIT on matches, one statement would read to the end of the
// run looking for a page that is not there, and the caller could neither
// bound the work nor report how much of it was done.
const confinedWindowSQL = `
	SELECT COALESCE(MAX(id), 0), COUNT(*)
	  FROM (SELECT s.id AS id
	          FROM symbols s NOT INDEXED
	         WHERE s.index_run_id = ? AND s.id > ? AND s.kind <> 'file'
	         ORDER BY s.id
	         LIMIT ?)`

// confinedSQL is the confined-symbol query for n kinds over one id window.
//
// The file set arrives as ONE bound parameter holding a JSON array, which the
// engine materialises once per statement into a lookup (each use below is a
// LIST SUBQUERY in the plan). It is not an IN list of bind markers because a
// repository can have more test files than a statement may have variables,
// and not a temporary table because this is a read path that may run on any
// pooled connection.
//
// The last probe is what the query is for, and it is the one that could go
// quadratic. For each definition it walks the incoming edges through
// idx_edges_dst and stops at the FIRST one that starts outside the set, so a
// definition with ten thousand callers in ordinary code costs a handful of
// probes. Only a definition every one of whose callers is inside the set has
// all its edges read, and those rows are the answer. Each edge costs one
// primary-key lookup of the symbol it starts at; CROSS JOIN keeps edges as the
// outer loop so that lookup is never turned into a scan of symbols.
// TestConfinedSymbolsUsesItsIndexes pins the plan.
func confinedSQL(n int) string {
	// The kinds are numbered from ?5 by hand. A bare `?` takes the number
	// after the highest one seen so far in the text, and the kinds appear
	// before the first use of ?4, so bare markers would collide with it.
	kinds := make([]string, n)
	for i := range kinds {
		kinds[i] = "?" + strconv.Itoa(i+5)
	}
	return `
		SELECT s.id, s.kind, s.name, s.fqn, f.path, f.language,
		       s.start_line, s.end_line, s.exported,
		       f.parse_status <> 'ok',
		       ` + nameUnboundProbe + `,
		       ` + overridesProbe + `,
		       ` + consumesEventProbe + `,
		       (SELECT COUNT(*) FROM edges e INDEXED BY idx_edges_dst
		         WHERE ` + incomingEdge + `)
		  FROM symbols s NOT INDEXED
		 CROSS JOIN files f ON f.id = s.file_id
		 WHERE s.index_run_id = ?1
		   AND s.id > ?2
		   AND s.id <= ?3
		   AND s.kind IN (` + strings.Join(kinds, ",") + `)
		   AND s.file_id NOT IN (SELECT value FROM json_each(?4))
		   AND EXISTS (SELECT 1 FROM edges e INDEXED BY idx_edges_dst
		                WHERE ` + incomingEdge + `)
		   AND NOT EXISTS (SELECT 1 FROM edges e INDEXED BY idx_edges_dst
		                    CROSS JOIN symbols c ON c.id = e.src_id
		                    WHERE ` + incomingEdge + `
		                      AND c.file_id NOT IN (SELECT value FROM json_each(?4)))
		 ORDER BY s.id`
}

// SymbolsReferencedOnlyFrom returns the definitions outside fileIDs whose
// every incoming relationship starts in a symbol inside fileIDs, examining at
// most window symbols after afterID.
//
// The registry holds no opinion about what the set means. The caller that
// exists passes the run's test files, which makes the answer "code only its
// tests reach"; that classification lives in internal/capability, where the
// one definition of a test file is.
//
// "Incoming relationship" means what it means in UnreferencedSymbols: not
// CONTAINS, and not the symbol's own edges to itself. So the two queries
// partition the same definitions three ways: nothing points at it, everything
// that points at it is in the set, or something outside the set points at it.
//
// A definition with no incoming relationship at all is NOT returned (it is
// unreferenced, which is the other query's finding), and neither is one that
// sits inside the set itself.
func (r *Registry) SymbolsReferencedOnlyFrom(ctx context.Context, runID, afterID int64, window int, fileIDs []int64) (ConfinedPage, error) {
	if window <= 0 {
		window = 500
	}
	var page ConfinedPage
	if err := r.db.QueryRowContext(ctx, confinedWindowSQL, runID, afterID, window).
		Scan(&page.LastID, &page.Examined); err != nil {
		return ConfinedPage{}, fmt.Errorf("registry: confined-symbol window for run %d: %w", runID, err)
	}
	if page.Examined == 0 || len(fileIDs) == 0 {
		// An empty set confines nothing: any definition with a referrer has
		// one outside it. The window is still reported, so a caller's count of
		// what was examined stays true.
		return page, nil
	}

	// Every parameter is numbered in the statement, because the file set is
	// used twice: run, window start, window end, the set, then the kinds.
	args := make([]any, 0, len(unreferencedKinds)+4)
	args = append(args, runID, afterID, page.LastID, jsonIDs(fileIDs))
	for _, k := range unreferencedKinds {
		args = append(args, k)
	}

	rows, err := r.db.QueryContext(ctx, confinedSQL(len(unreferencedKinds)), args...)
	if err != nil {
		return ConfinedPage{}, fmt.Errorf("registry: confined symbols for run %d: %w", runID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var c ConfinedSymbol
		if err := rows.Scan(&c.ID, &c.Kind, &c.Name, &c.FQN, &c.FilePath, &c.Language,
			&c.StartLine, &c.EndLine, &c.Exported,
			&c.PartialParse, &c.NameUnbound, &c.Overrides, &c.ConsumesEvent,
			&c.Referrers); err != nil {
			return ConfinedPage{}, fmt.Errorf("registry: scan confined symbol: %w", err)
		}
		page.Symbols = append(page.Symbols, c)
	}
	return page, rows.Err()
}

// jsonIDs renders ids as a JSON array for json_each.
func jsonIDs(ids []int64) string {
	var b strings.Builder
	b.Grow(len(ids)*8 + 2)
	b.WriteByte('[')
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatInt(id, 10))
	}
	b.WriteByte(']')
	return b.String()
}

// FilePaths returns the path of every file in the run, keyed by file id.
//
// It is what lets a caller turn a rule about paths ("is this a test file")
// into the set of file ids SymbolsReferencedOnlyFrom takes, without the
// registry learning the rule. Memory follows the number of FILES, which is
// far below the number of symbols.
func (r *Registry) FilePaths(ctx context.Context, runID int64) (map[int64]string, error) {
	return r.filePaths(ctx, runID)
}

// unboundHandlersSQL lists the handler names routes declare and nothing was
// bound to.
//
// One pass over the run's symbols, in key order. There is no index on kind,
// and the planner's own choice was to walk idx_symbols_fqn and fetch every
// row out of order to test it; the table read in order is the same rows for
// less. The edge probe names its index for the reason unreferencedSQL gives.
const unboundHandlersSQL = `
	SELECT DISTINCT e.signature
	  FROM symbols e NOT INDEXED
	 WHERE e.index_run_id = ?
	   AND e.kind = ?
	   AND COALESCE(e.signature, '') <> ''
	   AND NOT EXISTS (SELECT 1 FROM edges x INDEXED BY idx_edges_src
	                    WHERE x.index_run_id = e.index_run_id
	                      AND x.src_id = e.id
	                      AND x.kind = ?)`

// UnboundRouteHandlers returns the handler names that HTTP routes in the run
// declare and that no definition was bound to.
//
// A route whose handler WAS bound has an EXPOSES edge to it, so that handler
// has an incoming relationship and UnreferencedSymbols never returns it. What
// is left is the routes whose handler could not be bound: the name is defined
// in more than one file, or in none the binder would accept. Every definition
// spelled like one of these names may be the handler, and a caller grading
// "nothing calls this" needs to know that.
//
// The list is as long as the number of unbound routes, and it is read in one
// statement.
func (r *Registry) UnboundRouteHandlers(ctx context.Context, runID int64) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, unboundHandlersSQL,
		runID, string(model.KindEndpoint), string(model.EdgeExposes))
	if err != nil {
		return nil, fmt.Errorf("registry: unbound route handlers for run %d: %w", runID, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("registry: scan unbound route handler: %w", err)
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// FileStructure is what the index knows about one file's size and how much of
// the rest of the repository depends on it.
type FileStructure struct {
	Path      string
	Language  string
	SizeBytes int64
	Symbols   int

	// Dependents counts relationships that start in ANOTHER file and end at a
	// symbol defined here. It is a count of edges, not of distinct files, so a
	// file called from one place a hundred times scores as high as one called
	// from a hundred places; the report says "inbound relationships", never
	// "dependent files".
	Dependents int
}

// structureChunk bounds one IN list. SQLite's default variable limit is 32766;
// this stays far below it and keeps each statement's plan a handful of index
// probes.
const structureChunk = 400

// fileStructureSQL is the per-file structure query for n paths.
//
// The join order in the dependents subquery is FORCED (CROSS JOIN and INDEXED
// BY), not left to the planner. Left alone it started from edges, reading every
// edge in the run once per file: correct, and on this repository's own
// 19,000-edge index slow enough that `graphlume hotspots` did not return in two
// minutes. The intended plan starts from the file's own symbols
// (idx_symbols_file) and probes idx_edges_dst for each, so the cost follows the
// file, not the repository. TestFileStructureForUsesTheDestinationIndex pins
// it, because a plan is exactly the kind of thing that regresses silently.
func fileStructureSQL(n int) string {
	return `
		SELECT f.path, f.language, f.size_bytes,
		       (SELECT COUNT(*) FROM symbols s WHERE s.file_id = f.id AND s.kind <> 'file'),
		       (SELECT COUNT(*)
		          FROM symbols d
		          CROSS JOIN edges e INDEXED BY idx_edges_dst
		                  ON e.index_run_id = f.index_run_id AND e.dst_id = d.id
		          CROSS JOIN symbols sr ON sr.id = e.src_id
		         WHERE d.file_id = f.id
		           AND sr.file_id <> f.id
		           AND e.kind NOT IN ('CONTAINS', 'DOCUMENTS'))
		  FROM files f
		 WHERE f.index_run_id = ?
		   AND f.path IN (` + placeholders(n) + `)`
}

// FileStructureFor returns structure for the named files that exist in runID.
//
// The caller names the paths, so memory follows the size of the question (the
// files a commit window touched) rather than the size of the repository. Paths
// the run does not contain are simply absent from the result — a file deleted
// since the commit that touched it is the ordinary case.
func (r *Registry) FileStructureFor(ctx context.Context, runID int64, paths []string) (map[string]FileStructure, error) {
	out := make(map[string]FileStructure, len(paths))
	for start := 0; start < len(paths); start += structureChunk {
		end := min(start+structureChunk, len(paths))
		chunk := paths[start:end]

		args := make([]any, 0, len(chunk)+1)
		args = append(args, runID)
		for _, p := range chunk {
			args = append(args, p)
		}
		rows, err := r.db.QueryContext(ctx, fileStructureSQL(len(chunk)), args...)
		if err != nil {
			return nil, fmt.Errorf("registry: file structure for run %d: %w", runID, err)
		}
		for rows.Next() {
			var fs FileStructure
			if err := rows.Scan(&fs.Path, &fs.Language, &fs.SizeBytes, &fs.Symbols, &fs.Dependents); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("registry: scan file structure: %w", err)
			}
			out[fs.Path] = fs
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}

// EdgeBetween returns the strongest relationship from src to dst, or false when
// none exists. It is how a path of symbol ids is turned back into evidence: the
// traversal returns ids, and a path an agent cannot check hop by hop is an
// assertion rather than an answer.
func (r *Registry) EdgeBetween(ctx context.Context, runID, srcID, dstID int64) (EdgeHit, bool, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT e.src_id, src.name, srcf.path, e.dst_id, dst.name,
		       e.kind, e.confidence, e.source, COALESCE(e.detail,''),
		       e.evidence_file, e.evidence_line, e.evidence_col
		  FROM edges e
		  JOIN symbols src  ON src.id = e.src_id
		  JOIN files   srcf ON srcf.id = src.file_id
		  JOIN symbols dst  ON dst.id = e.dst_id
		 WHERE e.index_run_id = ? AND e.src_id = ? AND e.dst_id = ?
		 ORDER BY `+fmt.Sprintf(confidenceRankCase, "e.confidence")+` DESC, e.kind, e.evidence_line
		 LIMIT 1`, runID, srcID, dstID)
	if err != nil {
		return EdgeHit{}, false, fmt.Errorf("registry: edge %d -> %d: %w", srcID, dstID, err)
	}
	defer rows.Close()

	if !rows.Next() {
		return EdgeHit{}, false, rows.Err()
	}
	var e EdgeHit
	if err := rows.Scan(&e.SrcID, &e.SrcName, &e.SrcPath, &e.DstID, &e.DstName,
		&e.Kind, &e.Confidence, &e.Source, &e.Detail,
		&e.EvidenceFile, &e.EvidenceLine, &e.EvidenceCol); err != nil {
		return EdgeHit{}, false, fmt.Errorf("registry: scan edge: %w", err)
	}
	return e, true, rows.Err()
}

// SymbolByID returns one symbol with its file path, or false when the run does
// not hold it.
func (r *Registry) SymbolByID(ctx context.Context, runID, id int64) (SymbolHit, bool, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.id, s.name, s.fqn, s.kind, f.path, s.start_line, s.end_line,
		       s.confidence, s.source, s.exported
		  FROM symbols s
		  JOIN files f ON f.id = s.file_id
		 WHERE s.index_run_id = ? AND s.id = ?`, runID, id)
	if err != nil {
		return SymbolHit{}, false, fmt.Errorf("registry: symbol %d: %w", id, err)
	}
	defer rows.Close()

	if !rows.Next() {
		return SymbolHit{}, false, rows.Err()
	}
	var h SymbolHit
	if err := rows.Scan(&h.ID, &h.Name, &h.FQN, &h.Kind, &h.FilePath, &h.StartLine, &h.EndLine,
		&h.Confidence, &h.Source, &h.Exported); err != nil {
		return SymbolHit{}, false, fmt.Errorf("registry: scan symbol: %w", err)
	}
	return h, true, rows.Err()
}

// Mention is one file whose indexed text uses an identifier in code.
type Mention struct {
	Path string
	// Lines are the 1-based lines on which the identifier occurs as a whole
	// word outside a comment-only line, in ascending order, at most
	// mentionLineCap of them.
	Lines []int
}

const (
	// mentionFileCap bounds how many candidate files one identifier's text is
	// read from. The question a caller asks is "does this name appear anywhere
	// besides its definition", which is answered by the first few files; a
	// name common enough to exceed the cap is certainly mentioned.
	mentionFileCap = 12
	// mentionLineCap bounds the lines recorded per file.
	mentionLineCap = 64
)

// IdentifierMentions reports where name occurs as a whole identifier in the
// indexed text, on lines that are not comments.
//
// It exists because the symbol graph records CALLS far more reliably than it
// records a function used as a value — passed as a callback, stored in a
// table, registered with a framework. Those uses leave no edge, and they do
// leave the identifier in the text. A caller deciding whether a definition is
// unused needs both.
//
// The full-text index finds candidate files; the lines are then read from the
// stored text with identifier boundaries, because the index's tokenizer stems
// and splits on underscores and would otherwise report `get_user` for a file
// that only contains `get` and `user`.
//
// COMMENT-ONLY LINES ARE SKIPPED, and the reason is a measured one: most
// languages' documentation conventions open a definition's comment with the
// definition's own name ("// orphan is never called"), so counting comments
// made every documented function look used. Detection is by the line's leading
// marker (see commentOnly), which is deliberately crude and deliberately
// one-sided: a commented-out call is read as no use, and a line of code is
// never mistaken for a comment unless it starts with one of those markers.
//
// The line a definition itself sits on is returned like any other; the caller
// knows where its definitions are and discounts them.
//
// ok is false when the answer is incomplete: the name has nothing the tokenizer
// can index (no letter or digit), or so many files matched that not all were
// read. Either way the caller must treat the name as unknown rather than as
// unmentioned.
func (r *Registry) IdentifierMentions(ctx context.Context, runID int64, name string) (mentions []Mention, ok bool, err error) {
	phrase := ftsPhrase(name)
	if phrase == "" {
		return nil, false, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT f.path, files_fts.content
		  FROM files_fts
		  JOIN files_fts_map m ON m.fts_rowid = files_fts.rowid
		  JOIN files f         ON f.id = m.file_id
		 WHERE files_fts MATCH ?
		   AND f.index_run_id = ?
		 LIMIT ?`, "content : "+phrase, runID, mentionFileCap)
	if err != nil {
		return nil, false, fmt.Errorf("registry: mentions of %q: %w", name, err)
	}
	defer rows.Close()

	files := 0
	for rows.Next() {
		var path, content string
		if err := rows.Scan(&path, &content); err != nil {
			return nil, false, fmt.Errorf("registry: scan mention: %w", err)
		}
		files++
		if lines := identifierLines(content, name); len(lines) > 0 {
			mentions = append(mentions, Mention{Path: path, Lines: lines})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	// The cap was reached, so files that were not read may hold the name. The
	// tokenizer folds case and stems, so a name like `builder` matches every
	// file that says strings.Builder, and the twelve returned need not include
	// the one that matters. That is "could not look everywhere", which the
	// contract reports as unknown.
	if files >= mentionFileCap {
		return mentions, false, nil
	}
	return mentions, true, nil
}

// identifierLines returns the lines of text on which name occurs as a whole
// identifier, skipping comment-only lines.
func identifierLines(text, name string) []int {
	var out []int
	for _, l := range identifierLinesText(text, name) {
		out = append(out, l.n)
	}
	return out
}

// numberedLine is one line of text with its 1-based number.
type numberedLine struct {
	n    int
	text string
}

// identifierLinesText is identifierLines with the text of each line, for a
// caller that needs to look at the line (UnrecordedMentions reads past imports).
func identifierLinesText(text, name string) []numberedLine {
	var out []numberedLine
	line := 0
	for len(text) > 0 && len(out) < mentionLineCap {
		line++
		var cur string
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			cur, text = text[:i], text[i+1:]
		} else {
			cur, text = text, ""
		}
		if !strings.Contains(cur, name) || commentOnly(cur) {
			continue
		}
		if countIdentifier(cur, name) > 0 {
			out = append(out, numberedLine{n: line, text: cur})
		}
	}
	return out
}

// commentMarkers open a line that holds nothing but a comment, across the
// languages the index binds references for. `*` covers the continuation lines
// of a block comment, which is where Java and JavaScript documentation lives.
// Python docstrings are absent on purpose: they sit inside the definition they
// document, and the caller already discounts a definition's own lines.
var commentMarkers = []string{"//", "#", "*", "/*", "--", ";", "<!--", "%"}

func commentOnly(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	for _, m := range commentMarkers {
		if !strings.HasPrefix(trimmed, m) {
			continue
		}
		// `#include`, a Rust attribute and a C# directive are code: a `#`
		// followed by a letter, `[` or `!` is not treated as a comment.
		if m == "#" && len(trimmed) > 1 && (identByte(trimmed[1]) || trimmed[1] == '[' || trimmed[1] == '!') {
			return false
		}
		// A leading `*` is a comment continuation only when it is not a
		// pointer dereference: require a space, a slash or another `*`
		// after it.
		if m == "*" && len(trimmed) > 1 && trimmed[1] != ' ' && trimmed[1] != '/' && trimmed[1] != '*' {
			return false
		}
		return true
	}
	return false
}

// ftsPhrase renders name as an FTS5 phrase: its alphanumeric runs, quoted.
// Everything else is a separator to the unicode61 tokenizer, so `get_user`
// becomes the two-token phrase "get user". Returns "" when no run exists.
func ftsPhrase(name string) string {
	var b strings.Builder
	inRun := false
	wrote := false
	for _, r := range name {
		alnum := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 127
		if alnum {
			if !inRun && wrote {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			inRun, wrote = true, true
			continue
		}
		inRun = false
	}
	if !wrote {
		return ""
	}
	return `"` + b.String() + `"`
}

// countIdentifier counts occurrences of name in text that are not part of a
// longer identifier on either side.
func countIdentifier(text, name string) int {
	if name == "" {
		return 0
	}
	n := 0
	for from := 0; from < len(text); {
		i := strings.Index(text[from:], name)
		if i < 0 {
			break
		}
		start := from + i
		end := start + len(name)
		if (start == 0 || !identByte(text[start-1])) && (end == len(text) || !identByte(text[end])) {
			n++
		}
		from = end
	}
	return n
}

func identByte(c byte) bool {
	return c == '_' || c == '$' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 128
}

// FanInSymbol is an exported function or method with its inbound edge count.
type FanInSymbol struct {
	ID       int64
	Name     string
	FQN      string
	Kind     string
	FilePath string
	// InDegree counts every relationship pointing at the symbol, of any kind
	// and confidence: the same figure IncomingEdgeCount returns for it.
	InDegree int
}

// HighFanInSymbols returns the exported functions and methods with at least
// minInDegree inbound relationships, the widest first, at most limit of them.
//
// ONE aggregate query, in place of a count query per symbol. The blindspot
// detector must not load every symbol in the run and ask for each exported one's
// in-degree separately, which is one round trip per exported function: 2.4
// seconds on a 5,000-symbol fixture and unbounded memory on a large one. This
// reads idx_edges_dst once and keeps the top `limit`.
//
// Ties break on id so the order — and therefore which symbols fall inside the
// limit — is the same on every run.
func (r *Registry) HighFanInSymbols(ctx context.Context, runID int64, minInDegree, limit int) ([]FanInSymbol, error) {
	if limit <= 0 {
		limit = 500
	}
	// The in-degrees are counted from the edge index alone, then joined to
	// the symbols they belong to. Starting from the symbols instead read every
	// row of the symbol table to test its kind, which no index covers: 0.6 s
	// of a blindspots audit on a 366,000-symbol index, against 0.15 s.
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.id, s.name, s.fqn, s.kind, f.path, d.in_degree
		  FROM (SELECT dst_id, COUNT(*) AS in_degree
		          FROM edges INDEXED BY idx_edges_dst
		         WHERE index_run_id = ?
		         GROUP BY dst_id
		        HAVING COUNT(*) >= ?) d
		 CROSS JOIN symbols s ON s.id = d.dst_id
		  JOIN files f ON f.id = s.file_id
		 WHERE s.index_run_id = ?
		   AND s.exported = 1
		   AND s.kind IN ('function','method')
		 ORDER BY d.in_degree DESC, s.id
		 LIMIT ?`, runID, minInDegree, runID, limit)
	if err != nil {
		return nil, fmt.Errorf("registry: high fan-in symbols for run %d: %w", runID, err)
	}
	defer rows.Close()

	var out []FanInSymbol
	for rows.Next() {
		var f FanInSymbol
		if err := rows.Scan(&f.ID, &f.Name, &f.FQN, &f.Kind, &f.FilePath, &f.InDegree); err != nil {
			return nil, fmt.Errorf("registry: scan fan-in symbol: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// UnboundReferencesNamed counts the references in a run spelled name that
// resolution did not bind: pending, ambiguous or unresolved. Any one of them
// may be a use of a definition by that name that the graph cannot show, which
// is why a deletion that sees no incoming edge must still look here.
func (r *Registry) UnboundReferencesNamed(ctx context.Context, runID int64, name string) (int64, error) {
	var n int64
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM symbol_references sr INDEXED BY idx_refs_name_binding
		 WHERE sr.index_run_id = ? AND sr.name = ?
		   AND sr.resolution IN ('pending','ambiguous','unresolved')`, runID, name).Scan(&n); err != nil {
		return 0, fmt.Errorf("registry: unbound references named %q: %w", name, err)
	}
	return n, nil
}
