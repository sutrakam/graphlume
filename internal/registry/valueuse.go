package registry

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Reads behind the question "does this name occur in code the index does not
// record as a reference to it?".
//
// A function handed to something by name (`.then(handleResponse)`,
// `register("x", handler)`, a table of handlers) is used, and the use is not a
// call, so no edge records it. The graph cannot see the relationship; the text
// can, because the identifier is still there. These reads let an answer say so
// instead of reporting the graph's silence as the code's.

// DefSpan is the lines one definition occupies in one file. A mention inside it
// is the definition itself, or the definition referring to itself, and neither
// is a use by anything else.
type DefSpan struct {
	Path     string
	From, To int
}

// Place is one line of one file.
type Place struct {
	Path string
	Line int
}

// UnrecordedMentions is where a name occurs as an identifier in code that is
// neither a definition of that name nor a reference the index recorded.
type UnrecordedMentions struct {
	// Known is false when the text index could not answer for this name, which
	// must be read as "not measured" and never as "no such place".
	Known bool

	// Total is how many places were found. A lower bound when !Complete.
	Total int

	// Complete is false when so many files hold the name that not all were
	// read, so Total may be short.
	Complete bool

	// Sample is the first few places, ordered by path and line.
	Sample []Place
}

const (
	// refLineCap bounds how many recorded reference lines of one name are read
	// to be subtracted. A name with more references than this is a hub: its
	// mentions cannot be told from its references by line, so the answer is
	// "not measured" rather than a count with the references left in.
	refLineCap = 5000

	// unrecordedSample is how many places are returned as examples.
	unrecordedSample = 5
)

// UnrecordedMentions reports the places name occurs as a whole identifier,
// outside comment-only lines and import statements, that are not inside any of
// defs and not on the line of any reference the index recorded with that name.
//
// Only SOURCE files of a language that can reference the name are read: the
// languages of the files that define it and their families (ReferencingLanguages).
// A Markdown page, a workflow file or a manifest that says the name is not a
// use of it. Import, use, require and include statements (several lines long
// included) and the function's own declarations and prototypes are not uses
// either; see importLineSet and declaresFunction.
//
// Reference lines are subtracted whatever became of the reference (bound here,
// bound elsewhere, ambiguous, unresolved), because each of those is a use the
// index DID see: the unbound ones are counted by NameBinding, and the ones bound
// to another definition are not uses of this one. What is left is text the
// extractor produced no reference for at all.
func (r *Registry) UnrecordedMentions(ctx context.Context, runID int64, name string, defs []DefSpan) (UnrecordedMentions, error) {
	return r.unrecordedMentions(ctx, runID, name, defs)
}

func (r *Registry) unrecordedMentions(ctx context.Context, runID int64, name string, defs []DefSpan) (UnrecordedMentions, error) {
	langs, err := r.definitionLanguages(ctx, runID, name)
	if err != nil {
		return UnrecordedMentions{}, err
	}
	files, capped, err := r.mentionFiles(ctx, runID, name, ReferencingLanguages(langs), mentionFileCap)
	if err != nil {
		return UnrecordedMentions{}, err
	}
	if files == nil {
		return UnrecordedMentions{}, nil
	}
	defStems := map[string]bool{}
	for _, d := range defs {
		defStems[pathStem(d.Path)] = true
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT f.path, sr.start_line
		  FROM symbol_references sr INDEXED BY idx_refs_run_name
		  JOIN files f ON f.id = sr.file_id
		 WHERE sr.index_run_id = ? AND sr.name = ?
		 LIMIT ?`, runID, name, refLineCap+1)
	if err != nil {
		return UnrecordedMentions{}, fmt.Errorf("registry: reference lines of %q: %w", name, err)
	}
	defer rows.Close()
	recorded := map[Place]bool{}
	n := 0
	for rows.Next() {
		var p Place
		if err := rows.Scan(&p.Path, &p.Line); err != nil {
			return UnrecordedMentions{}, fmt.Errorf("registry: reference lines of %q: scan: %w", name, err)
		}
		recorded[p] = true
		n++
	}
	if err := rows.Err(); err != nil {
		return UnrecordedMentions{}, err
	}
	if n > refLineCap {
		return UnrecordedMentions{}, nil
	}

	// A site the graph holds an edge for is recorded too, whatever wrote the
	// edge. The compiler pass records a function passed as a value as a
	// REFERENCES edge with no parser reference row behind it; counting only
	// reference rows reported those sites as "not recorded" while
	// references listed them, and lowered impact's certainty for it.
	erows, err := r.db.QueryContext(ctx, `
		SELECT e.evidence_file, e.evidence_line
		  FROM edges e
		  JOIN symbols d ON d.id = e.dst_id
		 WHERE e.index_run_id = ? AND d.index_run_id = ? AND d.name = ?
		 LIMIT ?`, runID, runID, name, refLineCap+1)
	if err != nil {
		return UnrecordedMentions{}, fmt.Errorf("registry: edge lines of %q: %w", name, err)
	}
	defer erows.Close()
	for erows.Next() {
		var p Place
		if err := erows.Scan(&p.Path, &p.Line); err != nil {
			return UnrecordedMentions{}, fmt.Errorf("registry: edge lines of %q: scan: %w", name, err)
		}
		recorded[p] = true
	}
	if err := erows.Err(); err != nil {
		return UnrecordedMentions{}, err
	}

	var places []Place
	for _, f := range files {
		imports := importLineSet(f.content)
		paired := isHeader(f.path) && defStems[pathStem(f.path)]
		for _, line := range identifierLinesText(f.content, name) {
			p := Place{Path: f.path, Line: line.n}
			if recorded[p] || insideAny(p, defs) || imports[line.n] || !identifierInCode(line.text, name) ||
				declaresFunction(line.text, name, f.lang, paired) || isJsxClosing(line.text, name) {
				continue
			}
			places = append(places, p)
		}
	}
	sort.Slice(places, func(i, j int) bool {
		if places[i].Path != places[j].Path {
			return places[i].Path < places[j].Path
		}
		return places[i].Line < places[j].Line
	})
	out := UnrecordedMentions{Known: true, Total: len(places), Complete: !capped}
	// A name found in so many files that not all were read, with none of what
	// was read outside a definition or a recorded reference, says nothing.
	if capped && len(places) == 0 {
		return UnrecordedMentions{}, nil
	}
	out.Sample = places[:min(len(places), unrecordedSample)]
	return out, nil
}

func insideAny(p Place, defs []DefSpan) bool {
	for _, d := range defs {
		if d.Path == p.Path && p.Line >= d.From && p.Line <= d.To {
			return true
		}
	}
	return false
}

// identifierInCode reports whether name occurs in line as a whole identifier
// outside a string literal. A test description or a message that says the name
// ("must have context when called with ExecuteContext") is prose and not a use.
//
// Crude and one-sided like commentOnly: a quote opens a string until the same
// quote closes it, so an apostrophe that is not a quote (a Rust lifetime) can
// hide the rest of a line. That loses a mention and never invents one.
func identifierInCode(line, name string) bool {
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case strings.HasPrefix(line[i:], name):
			end := i + len(name)
			if (i == 0 || !identByte(line[i-1])) && (end == len(line) || !identByte(line[end])) {
				return true
			}
		}
	}
	return false
}

type mentionFile struct {
	path    string
	lang    string
	content string
}

// mentionFiles reads the indexed text of the files that may hold name, at most
// mentionFileCap of them. capped says the cap was reached, so files that were
// not read may hold the name too. nil files means the name cannot be searched.
//
// Only files whose language is one of langs are read, so the cap is spent on
// code and a page of prose cannot use up a slot.
func (r *Registry) mentionFiles(ctx context.Context, runID int64, name string, langs []string, limit int) (files []mentionFile, capped bool, err error) {
	phrase := ftsPhrase(name)
	if phrase == "" {
		return nil, false, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(langs)), ",")
	args := []any{"content : " + phrase, runID}
	for _, l := range langs {
		args = append(args, l)
	}
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, `
		SELECT f.path, f.language, files_fts.content
		  FROM files_fts
		  JOIN files_fts_map m ON m.fts_rowid = files_fts.rowid
		  JOIN files f         ON f.id = m.file_id
		 WHERE files_fts MATCH ?
		   AND f.index_run_id = ?
		   AND f.language IN (`+ph+`)
		 LIMIT ?`, args...)
	if err != nil {
		return nil, false, fmt.Errorf("registry: mentions of %q: %w", name, err)
	}
	defer rows.Close()
	files = []mentionFile{}
	for rows.Next() {
		var f mentionFile
		if err := rows.Scan(&f.path, &f.lang, &f.content); err != nil {
			return nil, false, fmt.Errorf("registry: scan mention: %w", err)
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return files, len(files) >= limit, nil
}

// IncomingEdgeCounts returns, for each id, how many recorded relationships of
// any kind and confidence point at it. An id with none is absent from the map.
//
// One grouped read over idx_edges_dst for the whole set, so choosing which
// symbols on a path have few callers costs one query and not one per symbol.
func (r *Registry) IncomingEdgeCounts(ctx context.Context, runID int64, ids []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(ids))
	const chunk = 400
	for start := 0; start < len(ids); start += chunk {
		part := ids[start:min(start+chunk, len(ids))]
		ph := strings.TrimSuffix(strings.Repeat("?,", len(part)), ",")
		args := make([]any, 0, len(part)+1)
		args = append(args, runID)
		for _, id := range part {
			args = append(args, id)
		}
		rows, err := r.db.QueryContext(ctx, `
			SELECT e.dst_id, COUNT(*)
			  FROM edges e INDEXED BY idx_edges_dst
			 WHERE e.index_run_id = ? AND e.dst_id IN (`+ph+`)
			 GROUP BY e.dst_id`, args...)
		if err != nil {
			return nil, fmt.Errorf("registry: incoming edge counts: %w", err)
		}
		for rows.Next() {
			var id int64
			var n int
			if err := rows.Scan(&id, &n); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("registry: incoming edge counts: scan: %w", err)
			}
			out[id] = n
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
