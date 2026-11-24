package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/sutrakam/graphlume/pkg/model"
)

// This file holds the reads an ANSWER needs beyond the rows it lists: which
// definition a name means, and how much of the evidence about that definition
// the index actually holds.
//
// They exist because the answer layer was publishing verdicts it had no input
// for. "0 callers, risk low" was printed for a method with 792 references
// spelled like it, every one of them recorded as ambiguous, because nothing
// read those 792 rows back. Each query here is one indexed read of rows the
// index already stores.

// SymbolCandidate is one definition a name could mean, with everything the
// index holds that tells it apart from the others.
//
// It is wider than SymbolHit on purpose. Overloads share one qualified name
// (`<file>:<name>`), so the qualified name cannot select between them; the
// line, the parameter count and the enclosing type can.
type SymbolCandidate struct {
	SymbolHit

	// Language is the defining file's language as the index recorded it.
	Language string

	// ParamCount is the number of declared parameters, or model.ArityUnknown.
	ParamCount model.Arity

	// Owner is the innermost type whose body encloses the definition, when the
	// index recorded one. Empty for a top-level definition and for a language
	// that declares methods outside the type (a Go receiver is not lexically
	// enclosing), which is why it is a hint and never the only discriminator.
	Owner     string
	OwnerKind string
}

// codeKinds are the definition kinds that are code: something that can call
// or be called, extend or be extended.
const codeKinds = `'function','method','class','interface','type_alias'`

// ownerKinds are the definition kinds that can own a member.
const ownerKinds = `'class','interface','type_alias'`

// symbolCandidateSelect is the shared projection, so the by-name and by-fqn
// reads cannot drift into returning differently shaped rows.
const symbolCandidateSelect = `
	SELECT s.id, s.name, s.fqn, s.kind, f.path, s.start_line, s.end_line,
	       s.confidence, s.source, s.exported, f.language, s.param_count,
	       COALESCE((SELECT o.name FROM symbols o
	                  WHERE o.file_id = s.file_id AND o.id <> s.id
	                    AND o.kind IN (` + ownerKinds + `)
	                    AND o.start_line <= s.start_line AND o.end_line >= s.end_line
	                    AND (o.start_line < s.start_line OR o.end_line > s.end_line)
	                  ORDER BY (o.end_line - o.start_line), o.id LIMIT 1), ''),
	       COALESCE((SELECT o.kind FROM symbols o
	                  WHERE o.file_id = s.file_id AND o.id <> s.id
	                    AND o.kind IN (` + ownerKinds + `)
	                    AND o.start_line <= s.start_line AND o.end_line >= s.end_line
	                    AND (o.start_line < s.start_line OR o.end_line > s.end_line)
	                  ORDER BY (o.end_line - o.start_line), o.id LIMIT 1), '')
	  FROM symbols s
	  JOIN files f ON f.id = s.file_id`

// SymbolCandidates returns the definitions named exactly name, and how many
// exist in total.
//
// The total is separate from the rows because the rows are capped: a caller
// that reports "matches 14 definitions" from len(rows) reports the cap, not
// the count, the moment a name is common.
//
// Ordered by path, line and id. Deliberately NOT exported-first, which is how
// FindSymbolsByName orders: the exported flag follows a capitalised first
// letter in every language (see capability.ExportedKnown), so ordering by it
// ranks a Java class's public methods below its constants for a reason that
// has nothing to do with either.
func (r *Registry) SymbolCandidates(ctx context.Context, runID int64, name string, limit int) ([]SymbolCandidate, int, error) {
	return r.symbolCandidates(ctx, runID, "s.name", name, "", limit)
}

// SymbolCandidatesIn is SymbolCandidates narrowed to files whose path contains
// pathPart, and its total counts only those.
//
// The narrowing happens in SQL, before the LIMIT. A caller that fetched the
// first rows by name and then looked among them for a path was choosing among
// whichever definitions happened to sort first: with five rows fetched and
// fifteen definitions named New, a path naming the eleventh matched nothing
// and the first was answered for instead.
func (r *Registry) SymbolCandidatesIn(ctx context.Context, runID int64, name, pathPart string, limit int) ([]SymbolCandidate, int, error) {
	return r.symbolCandidates(ctx, runID, "s.name", name, pathPart, limit)
}

// SymbolCandidatesByFQN is SymbolCandidates keyed on the qualified name.
//
// It returns SEVERAL rows for one qualified name whenever overloads exist,
// which is the case this exists for: a caller that was told to "re-ask with a
// fully-qualified name" and did so must not be handed the first overload as if
// the name had been unique.
func (r *Registry) SymbolCandidatesByFQN(ctx context.Context, runID int64, fqn string, limit int) ([]SymbolCandidate, int, error) {
	return r.symbolCandidates(ctx, runID, "s.fqn", fqn, "", limit)
}

func (r *Registry) symbolCandidates(ctx context.Context, runID int64, column, value, pathPart string, limit int) ([]SymbolCandidate, int, error) {
	where := `s.index_run_id = ? AND s.kind <> 'file' AND ` + column + ` = ?`
	args := []any{runID, value}
	if pathPart != "" {
		// instr, not LIKE: a path holds '_' and '%' as ordinary characters.
		where += ` AND instr(f.path, ?) > 0`
		args = append(args, pathPart)
	}
	return r.symbolCandidatesWhere(ctx, value, where, args, limit)
}

// symbolCandidatesWhere is the shared read: the rows matching where, their
// count, and the same projection whichever way they were selected. value is
// what the caller asked for, for the error messages.
func (r *Registry) symbolCandidatesWhere(ctx context.Context, value, where string, args []any, limit int) ([]SymbolCandidate, int, error) {
	if limit <= 0 {
		limit = 50
	}

	var total int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM symbols s JOIN files f ON f.id = s.file_id WHERE `+where,
		args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("registry: count definitions of %q: %w", value, err)
	}
	if total == 0 {
		return nil, 0, nil
	}

	rows, err := r.db.QueryContext(ctx, symbolCandidateSelect+`
		 WHERE `+where+`
		 ORDER BY f.path, s.start_line, s.id
		 LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, 0, fmt.Errorf("registry: definitions of %q: %w", value, err)
	}
	defer rows.Close()

	out := make([]SymbolCandidate, 0, min(total, limit))
	for rows.Next() {
		var c SymbolCandidate
		var exported, params int
		if err := rows.Scan(&c.ID, &c.Name, &c.FQN, &c.Kind, &c.FilePath, &c.StartLine, &c.EndLine,
			&c.Confidence, &c.Source, &exported, &c.Language, &params, &c.Owner, &c.OwnerKind); err != nil {
			return nil, 0, fmt.Errorf("registry: definitions of %q: scan: %w", value, err)
		}
		c.Exported = exported == 1
		c.ParamCount = model.Arity(params)
		out = append(out, c)
	}
	return out, total, rows.Err()
}

// NameBinding is what resolution did with every reference spelled like one
// definition's name.
//
// The run-wide rate says how well the index resolved the repository. This says
// how well it resolved THE SYMBOL BEING ASKED ABOUT, which is the number an
// answer about that symbol rests on: a repository can bind 90% of its
// references and none of the ones naming this method.
type NameBinding struct {
	// Language is the language of the file that defines the symbol asked
	// about, as the index recorded it. It is read here because every caller
	// that asks how a name was bound next asks how that language was.
	Language string

	// References is every reference row spelled exactly like the name.
	References int64

	// BoundHere were bound to the definition asked about. BoundElsewhere were
	// bound to another definition of the same name.
	BoundHere      int64
	BoundElsewhere int64

	// Ambiguous matched several definitions and none was chosen. Unresolved
	// matched none. Pending have not been through resolution. Any of the three
	// may be a use of the definition asked about.
	Ambiguous  int64
	Unresolved int64
	Pending    int64

	// ProvenOther were left unbound by the resolver but a compiler has shown
	// each to be a use of something else, while the definition asked about is
	// in a file that compiler read. For Go, the type check read the reference's
	// file: had it been a use of this definition the checker would have proven
	// it and written an exact edge (measured: every call pair of three
	// repositories, none missed in a checked file). For a language with a
	// compiler indexer, the indexer resolved that very position to a symbol
	// outside this repository (see scipimport.Proof): json.Valid beside a Valid
	// method. They are taken out of Ambiguous, Unresolved and Pending, so an
	// answer does not tell an agent to search for callers the compiler has ruled
	// out.
	ProvenOther int64

	// Definitions is how many definitions are spelled like the name in files a
	// call of the symbol's language can reach (its family: see
	// ReferencingLanguages). Zero when it was not counted. It is what "several
	// definitions share the name" must be checked against: overloads and
	// `@overload` stubs the extractor folds into one are one definition.
	Definitions int64

	// InstalledCopy is how many of the unbound references (Ambiguous,
	// Unresolved, Pending: they are still counted there) sit where a compiler
	// resolved this repository's own package to an installed copy of it instead
	// of this checkout. See ResolutionSourceInstalled.
	InstalledCopy int64

	// WithoutEdge counts distinct sites bound to the definition asked about
	// that produced no relationship row, because the reference sits outside
	// every extracted definition: at module top level, or inside an anonymous
	// function. They are uses the index recognised and no relationship tool
	// lists.
	WithoutEdge int64
}

// Unbound is the references that name this symbol's spelling and are bound to
// nothing.
func (b NameBinding) Unbound() int64 { return b.Ambiguous + b.Unresolved + b.Pending }

// NameBinding reads the binding outcomes for the references spelled name, as
// seen from the definition symbolID.
//
// Both statements are index reads: idx_refs_run_name covers (index_run_id,
// name), idx_refs_resolved_fk the bound rows and idx_edges_dst the edges they
// are compared with.
func (r *Registry) NameBinding(ctx context.Context, runID, symbolID int64, name string) (NameBinding, error) {
	var b NameBinding
	if err := r.db.QueryRowContext(ctx, `
		SELECT f.language FROM symbols s JOIN files f ON f.id = s.file_id
		 WHERE s.id = ? AND s.index_run_id = ?`, symbolID, runID).Scan(&b.Language); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return b, fmt.Errorf("registry: language of symbol %d: %w", symbolID, err)
	}
	b.Language = strings.ToLower(b.Language)

	langs := ReferencingLanguages([]string{b.Language})
	ph := strings.TrimSuffix(strings.Repeat("?,", len(langs)), ",")
	defArgs := []any{runID, name}
	for _, l := range langs {
		defArgs = append(defArgs, l)
	}
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		  FROM symbols s INDEXED BY idx_symbols_name
		  JOIN files f ON f.id = s.file_id
		 WHERE s.index_run_id = ? AND s.name = ?
		   AND s.kind IN ('function','method','class','interface','type_alias','variable','constant','field')
		   AND f.language IN (`+ph+`)`, defArgs...).Scan(&b.Definitions); err != nil {
		return b, fmt.Errorf("registry: definitions named %q: %w", name, err)
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT sr.resolution, COALESCE(sr.resolved_symbol_id, 0) = ?, COUNT(*)
		  FROM symbol_references sr INDEXED BY idx_refs_name_binding
		 WHERE sr.index_run_id = ? AND sr.name = ?
		 GROUP BY 1, 2
		 ORDER BY 1, 2`, symbolID, runID, name)
	if err != nil {
		return b, fmt.Errorf("registry: binding of references named %q: %w", name, err)
	}
	defer rows.Close()

	for rows.Next() {
		var resolution string
		var here bool
		var n int64
		if err := rows.Scan(&resolution, &here, &n); err != nil {
			return b, fmt.Errorf("registry: binding of references named %q: scan: %w", name, err)
		}
		b.References += n
		switch Resolution(resolution) {
		case ResolutionResolved:
			if here {
				b.BoundHere += n
			} else {
				b.BoundElsewhere += n
			}
		case ResolutionAmbiguous:
			b.Ambiguous += n
		case ResolutionUnresolved:
			b.Unresolved += n
		case ResolutionPending:
			b.Pending += n
		}
	}
	if err := rows.Err(); err != nil {
		return b, fmt.Errorf("registry: binding of references named %q: %w", name, err)
	}
	if b.Ambiguous+b.Unresolved+b.Pending > 0 {
		var err error
		if b.Language == "go" {
			err = r.settleByGoTypes(ctx, runID, symbolID, name, &b)
		} else {
			err = r.settleByCompilerIndex(ctx, runID, symbolID, name, &b)
		}
		if err != nil {
			return b, err
		}
	}
	if b.BoundHere == 0 {
		return b, nil
	}

	// A set difference rather than NOT EXISTS: the anti-join would probe
	// idx_edges_dst once per bound reference and read every edge of the
	// definition each time, which is quadratic on exactly the definitions with
	// the most callers.
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT f.path, sr.start_line
			  FROM symbol_references sr
			  JOIN files f ON f.id = sr.file_id
			 WHERE sr.resolved_symbol_id = ? AND sr.index_run_id = ?
			   AND sr.resolution = 'resolved' AND sr.name = ?
			EXCEPT
			SELECT e.evidence_file, e.evidence_line
			  FROM edges e
			 WHERE e.index_run_id = ? AND e.dst_id = ?)`,
		symbolID, runID, name, runID, symbolID).Scan(&b.WithoutEdge); err != nil {
		return b, fmt.Errorf("registry: references to %q without a relationship: %w", name, err)
	}
	return b, nil
}

// LanguageBinding is one language's share of a run: what was defined in it and
// what resolution did with the references written in it.
type LanguageBinding struct {
	Language string
	Files    int64

	// Definitions counts the CODE definitions in the language: functions,
	// methods and types. A configuration key or a declared dependency is a
	// symbol too, and counting those would make YAML a language "whose calls
	// are not extracted".
	Definitions int64

	// Relationships counts the edges that START at a definition in this
	// language, containment excluded. A compiler index can record them for a
	// language whose references the parser does not extract, so they are
	// counted beside the references rather than inferred from them.
	Relationships int64

	// Hierarchy is the subset of Relationships that record a type relationship
	// (IMPLEMENTS or EXTENDS). Zero in a language with classes means the index
	// records no inheritance for it, which is a different gap from unbound
	// calls and is invisible in the reference counts.
	Hierarchy int64

	ResolutionStats
}

// ExtractsReferences reports whether anything at all records what this
// language's code uses: a reference row from one of its files, or a
// relationship starting at one of its definitions.
//
// False is the finding that matters. A language whose files hold thousands of
// definitions and not one reference is a language whose calls are not
// extracted, and every "no callers" about it is a statement about the parser.
func (l LanguageBinding) ExtractsReferences() bool { return l.Total > 0 || l.Relationships > 0 }

// LanguageBindings reads LanguageBinding for every language in the run, keyed
// by the language name the index recorded (lower case; "" for files with none).
//
// Four GROUP BYs over the run. Two of them read every reference row and every
// edge, so a caller that needs this per request should memoise it by run id,
// as it does ResolutionStats: a committed run never changes.
func (r *Registry) LanguageBindings(ctx context.Context, runID int64) (map[string]LanguageBinding, error) {
	// The figures depend on the index revision and on nothing the caller asked,
	// so `resolve` stores them when it finishes (runstats.go). An index written
	// by an older build, or changed since it was last summarised, has no valid
	// stored copy and is computed here as it always was.
	if out, ok, err := r.storedLanguageBindings(ctx, runID); err != nil {
		return nil, err
	} else if ok {
		return out, nil
	}
	return r.computeLanguageBindings(ctx, runID)
}

// languageRefsSQL counts a run's references by language and outcome.
//
// Counted per FILE first and joined to the language second, over a plain scan
// of the table. Joined per reference, the planner walked the reference table in
// name order (idx_refs_run_name) and fetched each row from the table at random:
// 3.8 million random page reads on the Elasticsearch index, 65 s against 8 s
// for the scan, with the same counts.
const languageRefsSQL = `SELECT f.language, x.resolution, SUM(x.n)
	                  FROM (SELECT file_id, resolution, COUNT(*) AS n
	                          FROM symbol_references NOT INDEXED
	                         WHERE index_run_id = ? GROUP BY file_id, resolution) x
	                  JOIN files f ON f.id = x.file_id
	                 GROUP BY f.language, x.resolution
	                 ORDER BY f.language, x.resolution`

// computeLanguageBindings is LanguageBindings read from the tables themselves.
func (r *Registry) computeLanguageBindings(ctx context.Context, runID int64) (map[string]LanguageBinding, error) {
	out := map[string]LanguageBinding{}
	get := func(lang string) LanguageBinding {
		l := out[lang]
		l.Language = lang
		return l
	}

	scan := func(query string, apply func(lang, key string, n int64)) error {
		rows, err := r.db.QueryContext(ctx, query, runID)
		if err != nil {
			return fmt.Errorf("registry: language bindings: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var lang, key string
			var n int64
			if err := rows.Scan(&lang, &key, &n); err != nil {
				return fmt.Errorf("registry: language bindings: scan: %w", err)
			}
			apply(strings.ToLower(lang), key, n)
		}
		return rows.Err()
	}

	if err := scan(`SELECT language, '', COUNT(*) FROM files
	                 WHERE index_run_id = ? GROUP BY language ORDER BY language`,
		func(lang, _ string, n int64) {
			l := get(lang)
			l.Files = n
			out[lang] = l
		}); err != nil {
		return nil, err
	}
	if err := scan(`SELECT f.language, '', COUNT(*) FROM symbols s
	                  JOIN files f ON f.id = s.file_id
	                 WHERE s.index_run_id = ? AND s.kind IN (`+codeKinds+`)
	                 GROUP BY f.language ORDER BY f.language`,
		func(lang, _ string, n int64) {
			l := get(lang)
			l.Definitions = n
			out[lang] = l
		}); err != nil {
		return nil, err
	}
	if err := scan(`SELECT f.language,
	                       CASE WHEN e.kind IN ('IMPLEMENTS','EXTENDS') THEN 'hierarchy' ELSE 'other' END,
	                       COUNT(*)
	                  FROM edges e
	                  JOIN symbols s ON s.id = e.src_id
	                  JOIN files f ON f.id = s.file_id
	                 WHERE e.index_run_id = ? AND e.kind NOT IN ('CONTAINS', 'DOCUMENTS')
	                 GROUP BY 1, 2 ORDER BY 1, 2`,
		func(lang, group string, n int64) {
			l := get(lang)
			l.Relationships += n
			if group == "hierarchy" {
				l.Hierarchy += n
			}
			out[lang] = l
		}); err != nil {
		return nil, err
	}
	if err := scan(languageRefsSQL,
		func(lang, resolution string, n int64) {
			l := get(lang)
			l.Total += n
			switch Resolution(resolution) {
			case ResolutionResolved:
				l.Resolved = n
			case ResolutionAmbiguous:
				l.Ambiguous = n
			case ResolutionUnresolved:
				l.Unresolved = n
			case ResolutionIgnored:
				l.Ignored = n
			case ResolutionPending:
				l.Pending = n
			}
			out[lang] = l
		}); err != nil {
		return nil, err
	}
	return out, nil
}

// IncomingSourceCount is how many distinct symbols have an edge to symbolID
// under the filter: the number of callers, where EdgePage's total is the
// number of call sites. One function that calls three times is one caller.
func (r *Registry) IncomingSourceCount(ctx context.Context, runID, symbolID int64, filter EdgeFilter) (int, error) {
	where := "e.index_run_id = ? AND e.dst_id = ?"
	args := []any{runID, symbolID}
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
	var n int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT e.src_id) FROM edges e WHERE `+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("registry: count sources for symbol %d: %w", symbolID, err)
	}
	return n, nil
}

// EdgePage returns one page of the relationships at a symbol, and how many
// pass the filter in total.
//
// It differs from IncomingEdges and OutgoingEdges in two ways a paged answer
// needs. The order is TOTAL: confidence, then file, line and edge id, so the
// row at offset 50 is the same row on every call and a second page neither
// repeats nor skips one. And the total is counted, not inferred from a full
// page, so the answer can say "50 of 180" instead of "50, perhaps more".
func (r *Registry) EdgePage(ctx context.Context, runID, symbolID int64, incoming bool, limit, offset int, filter EdgeFilter) ([]EdgeHit, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	where := "e.index_run_id = ? AND e.dst_id = ?"
	if !incoming {
		where = "e.index_run_id = ? AND e.src_id = ?"
	}
	args := []any{runID, symbolID}
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

	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM edges e WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("registry: count edges for symbol %d: %w", symbolID, err)
	}
	if total == 0 || offset >= total {
		return nil, total, nil
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT e.src_id, src.name, COALESCE(src.fqn,''), srcf.path, e.dst_id, dst.name,
		       e.kind, e.confidence, e.source, COALESCE(e.detail,''),
		       e.evidence_file, e.evidence_line, e.evidence_col
		  FROM edges e
		  JOIN symbols src  ON src.id = e.src_id
		  JOIN files   srcf ON srcf.id = src.file_id
		  JOIN symbols dst  ON dst.id = e.dst_id
		 WHERE `+where+`
		 ORDER BY `+fmt.Sprintf(confidenceRankCase, "e.confidence")+` DESC,
		          srcf.path, e.evidence_line, e.id
		 LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("registry: edges for symbol %d: %w", symbolID, err)
	}
	defer rows.Close()

	out := make([]EdgeHit, 0, min(limit, total-offset))
	for rows.Next() {
		var e EdgeHit
		var fqn string
		if err := rows.Scan(&e.SrcID, &e.SrcName, &fqn, &e.SrcPath, &e.DstID, &e.DstName,
			&e.Kind, &e.Confidence, &e.Source, &e.Detail,
			&e.EvidenceFile, &e.EvidenceLine, &e.EvidenceCol); err != nil {
			return nil, 0, fmt.Errorf("registry: edges for symbol %d: scan: %w", symbolID, err)
		}
		e.SrcQualified = qualifiedName(e.SrcName, fqn, e.SrcPath)
		out = append(out, e)
	}
	return out, total, rows.Err()
}

// BoundSite is one reference bound to a definition, with the qualifier it was
// written with: `errors` in `errors.New(...)`, empty for a bare `New(...)`.
type BoundSite struct {
	Path      string
	Line      int
	Qualifier string

	// ReceiverKind is what the call was made on, one of the model.Receiver*
	// constants: a bare call, a name, a path, the enclosing instance, a
	// stated type, a value whose type nothing states, or an expression. Empty
	// for a reference that did not record it.
	ReceiverKind string
}

// BoundSites returns the references bound to symbolID, up to limit, ordered by
// file and line.
//
// The qualifier and the receiver kind are the reason this exists. The
// resolver binds a qualified call through what the qualifier names (an
// import, a module, a type), and a call on a value whose type nothing states
// through the member name alone. Reading both back lets an answer show which
// of the two a row is, which the tier alone does not.
func (r *Registry) BoundSites(ctx context.Context, runID, symbolID int64, limit int) ([]BoundSite, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT f.path, sr.start_line, sr.receiver, sr.receiver_kind
		  FROM symbol_references sr INDEXED BY idx_refs_bound_sites
		  JOIN files f ON f.id = sr.file_id
		 WHERE sr.resolved_symbol_id = ? AND sr.index_run_id = ? AND sr.resolution = 'resolved'
		 ORDER BY f.path, sr.start_line, sr.id
		 LIMIT ?`, symbolID, runID, limit)
	if err != nil {
		return nil, fmt.Errorf("registry: references bound to symbol %d: %w", symbolID, err)
	}
	defer rows.Close()

	var out []BoundSite
	for rows.Next() {
		var b BoundSite
		if err := rows.Scan(&b.Path, &b.Line, &b.Qualifier, &b.ReceiverKind); err != nil {
			return nil, fmt.Errorf("registry: references bound to symbol %d: scan: %w", symbolID, err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GoTypesDetail is the detail the built-in Go type check writes on every edge
// it proves (gotypes.Detail; a test there keeps the two equal, since this
// package cannot import that one).
const GoTypesDetail = "go/types (built in)"

// settleByGoTypes moves the unbound references of a Go name that the type
// checker has ruled out into ProvenOther: see NameBinding.ProvenOther. It does
// nothing unless the definition's own file was read by the checker, which is
// known from an exact edge written there or pointing into it.
func (r *Registry) settleByGoTypes(ctx context.Context, runID, symbolID int64, name string, b *NameBinding) error {
	var checked bool
	if err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM symbols t JOIN files tf ON tf.id = t.file_id
			 WHERE t.id = ? AND (
			   EXISTS (SELECT 1 FROM edges e INDEXED BY idx_edges_evidence
			            WHERE e.index_run_id = ? AND e.evidence_file = tf.path
			              AND e.source = 'compiler' AND e.detail = ?)
			   OR EXISTS (SELECT 1 FROM edges e INDEXED BY idx_edges_dst
			               CROSS JOIN symbols d ON d.id = e.dst_id
			              WHERE e.index_run_id = ? AND e.dst_id = t.id
			                AND e.source = 'compiler' AND e.detail = ?)
			   OR EXISTS (SELECT 1 FROM symbols d CROSS JOIN edges e INDEXED BY idx_edges_dst
			                ON e.index_run_id = ? AND e.dst_id = d.id
			              WHERE d.file_id = tf.id AND e.source = 'compiler' AND e.detail = ?)))`,
		symbolID, runID, GoTypesDetail, runID, GoTypesDetail, runID, GoTypesDetail).Scan(&checked); err != nil {
		return fmt.Errorf("registry: was the file of symbol %d type-checked: %w", symbolID, err)
	}
	if !checked {
		return nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT sr.resolution, COUNT(*)
		  FROM symbol_references sr INDEXED BY idx_refs_name_binding
		  JOIN files f ON f.id = sr.file_id
		 WHERE sr.index_run_id = ? AND sr.name = ?
		   AND sr.resolution IN ('ambiguous','unresolved','pending')
		   AND f.language = 'go'
		   AND EXISTS (SELECT 1 FROM edges e INDEXED BY idx_edges_evidence
		                WHERE e.index_run_id = ? AND e.evidence_file = f.path
		                  AND e.source = 'compiler' AND e.detail = ?)
		 GROUP BY 1`, runID, name, runID, GoTypesDetail)
	if err != nil {
		return fmt.Errorf("registry: references to %q the type check ruled out: %w", name, err)
	}
	defer rows.Close()
	for rows.Next() {
		var resolution string
		var n int64
		if err := rows.Scan(&resolution, &n); err != nil {
			return fmt.Errorf("registry: references to %q the type check ruled out: scan: %w", name, err)
		}
		switch Resolution(resolution) {
		case ResolutionAmbiguous:
			n = min(n, b.Ambiguous)
			b.Ambiguous -= n
		case ResolutionUnresolved:
			n = min(n, b.Unresolved)
			b.Unresolved -= n
		case ResolutionPending:
			n = min(n, b.Pending)
			b.Pending -= n
		default:
			continue
		}
		b.ProvenOther += n
	}
	return rows.Err()
}

// settleByCompilerIndex moves into ProvenOther the unbound references of a name
// that a compiler indexer resolved, at their own position, to something outside
// this repository (they carry resolution_source = ResolutionSourceRuledOut).
//
// It does nothing unless the file defining the symbol is one the indexer read:
// a definition outside its program (excluded by the project's configuration,
// or one the indexer could not load) could be what a reference means while the
// indexer, never having seen it, resolved the position to something else. A
// file counts as read only if it was a document of an index imported from an
// indexer this tool ran (compiler_coverage), never because it was merely
// indexed, and never when the indexer skipped it.
func (r *Registry) settleByCompilerIndex(ctx context.Context, runID, symbolID int64, name string, b *NameBinding) error {
	var covered bool
	if err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM symbols t
		                JOIN compiler_coverage c ON c.file_id = t.file_id AND c.index_run_id = ?
		               WHERE t.id = ?)`, runID, symbolID).Scan(&covered); err != nil {
		return fmt.Errorf("registry: was the file of symbol %d read by a compiler indexer: %w", symbolID, err)
	}
	if !covered {
		return nil
	}
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM symbol_references sr INDEXED BY idx_refs_name_binding
		 WHERE sr.index_run_id = ? AND sr.name = ?
		   AND sr.resolution IN ('ambiguous','unresolved','pending')
		   AND sr.resolution_source = ?`, runID, name, ResolutionSourceInstalled).Scan(&b.InstalledCopy); err != nil {
		return fmt.Errorf("registry: references to %q seen against an installed copy: %w", name, err)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT sr.resolution, COUNT(*)
		  FROM symbol_references sr INDEXED BY idx_refs_name_binding
		 WHERE sr.index_run_id = ? AND sr.name = ?
		   AND sr.resolution IN ('ambiguous','unresolved','pending')
		   AND sr.resolution_source = ?
		 GROUP BY 1`, runID, name, ResolutionSourceRuledOut)
	if err != nil {
		return fmt.Errorf("registry: references to %q a compiler indexer ruled out: %w", name, err)
	}
	defer rows.Close()
	for rows.Next() {
		var resolution string
		var n int64
		if err := rows.Scan(&resolution, &n); err != nil {
			return fmt.Errorf("registry: references to %q a compiler indexer ruled out: scan: %w", name, err)
		}
		switch Resolution(resolution) {
		case ResolutionAmbiguous:
			n = min(n, b.Ambiguous)
			b.Ambiguous -= n
		case ResolutionUnresolved:
			n = min(n, b.Unresolved)
			b.Unresolved -= n
		case ResolutionPending:
			n = min(n, b.Pending)
			b.Pending -= n
		default:
			continue
		}
		b.ProvenOther += n
	}
	return rows.Err()
}
