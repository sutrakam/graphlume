package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/sutrakam/graphlume/pkg/model"
)

// The reads in this file serve Go-module stitching across repositories
// (internal/multirepo): which modules a repository defines, which of its calls
// are qualified by an import of somebody else's module, and which exported
// declarations a package directory holds. Each is a projection of tables the
// index already fills — files, files_fts, file_imports, symbol_references and
// symbols — so stitching needs no schema of its own and works on any index
// built since those tables existed.

// GoModFiles returns the text of every go.mod the run indexed, ordered by path.
//
// Read from the index rather than from the checkout on disk, for the reason
// FileExcerpt gives: the index is what the question is about, and a checkout
// that moved on since indexing would describe module paths the indexed code
// does not have. go.mod is indexed as text (language "gomod"), so its content
// is in files_fts even though it yields no definitions.
func (r *Registry) GoModFiles(ctx context.Context, runID int64) ([]FileText, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT f.path, files_fts.content
		  FROM files f
		  JOIN files_fts_map m ON m.file_id = f.id
		  JOIN files_fts       ON files_fts.rowid = m.fts_rowid
		 WHERE f.index_run_id = ?
		   AND (f.path = 'go.mod' OR f.path LIKE '%/go.mod')
		 ORDER BY f.path`, runID)
	if err != nil {
		return nil, fmt.Errorf("registry: go.mod files: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []FileText
	for rows.Next() {
		var ft FileText
		if err := rows.Scan(&ft.Path, &ft.Content); err != nil {
			return nil, fmt.Errorf("registry: go.mod files: scan: %w", err)
		}
		out = append(out, ft)
	}
	return out, rows.Err()
}

// GoImportedCall is one call reference in a Go file that imports a given
// module, paired with that import.
//
// A file importing two packages of the module yields one row per pair, so a
// consumer deciding which import a receiver names sees every candidate import
// beside the call; RefID identifies the call across those rows.
type GoImportedCall struct {
	RefID        int64
	FileID       int64
	FilePath     string
	Line         int
	Col          int
	Name         string
	Receiver     string
	ReceiverKind string
	Resolution   string
	ImportPath   string
	ImportAlias  string
}

// GoImportedCalls returns the calls in this run's Go files that did not bind
// to a definition in this repository, from files that import a package of one
// of the given modules, at most limit rows.
//
// Only unbound calls are read: a call the repository's own resolution bound is
// answered inside the repository, and reading it again here could only produce
// a second, contradictory answer. truncated is true when more rows existed than
// limit, so the caller can say its counts are floors rather than totals.
//
// The order is file path, line, column, reference id, import path and alias:
// stable across runs and across rowid assignment, which the deterministic
// report that consumes it depends on.
func (r *Registry) GoImportedCalls(ctx context.Context, runID int64, modules []string, limit int) (
	calls []GoImportedCall, truncated bool, err error,
) {
	if len(modules) == 0 {
		return nil, false, nil
	}
	if limit <= 0 {
		limit = 10_000
	}
	var match []string
	args := []any{runID}
	for _, m := range modules {
		match = append(match, `i.path = ? OR i.path LIKE ? ESCAPE '\'`)
		args = append(args, m, likeEscape(m)+"/%")
	}
	args = append(args, limit+1)

	rows, err := r.db.QueryContext(ctx, `
		SELECT r.id, r.file_id, f.path, r.start_line, r.start_col, r.name, r.receiver,
		       r.receiver_kind, r.resolution, i.path, i.alias
		  FROM symbol_references r
		  JOIN files f        ON f.id = r.file_id
		  JOIN file_imports i ON i.file_id = r.file_id
		 WHERE r.index_run_id = ?
		   AND f.path LIKE '%.go'
		   AND r.kind LIKE 'reference.call%'
		   AND r.resolution <> 'resolved'
		   AND (`+strings.Join(match, " OR ")+`)
		 ORDER BY f.path, r.start_line, r.start_col, r.id, i.path, i.alias
		 LIMIT ?`, args...)
	if err != nil {
		return nil, false, fmt.Errorf("registry: go imported calls: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var c GoImportedCall
		if err := rows.Scan(&c.RefID, &c.FileID, &c.FilePath, &c.Line, &c.Col, &c.Name, &c.Receiver,
			&c.ReceiverKind, &c.Resolution, &c.ImportPath, &c.ImportAlias); err != nil {
			return nil, false, fmt.Errorf("registry: go imported calls: scan: %w", err)
		}
		calls = append(calls, c)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("registry: go imported calls: %w", err)
	}
	if len(calls) > limit {
		// The extra row only proved there was more. It is dropped rather than
		// kept so that a cut never lands in the middle of the rows of one
		// call without the caller knowing: truncated says so.
		calls, truncated = calls[:limit], true
	}
	return calls, truncated, nil
}

// goPackageKinds are the declarations a qualified call `pkg.Name(...)` can
// name: a function, a type (a conversion or a generic instantiation is written
// as a call), a constant or a variable of function type. A method is not one
// of them, because `pkg.Name` never names a method.
var goPackageKinds = []string{
	string(model.KindFunction), string(model.KindClass), string(model.KindInterface),
	string(model.KindTypeAlias), string(model.KindConstant), string(model.KindVariable),
}

// GoPackageSymbols returns the exported, package-level declarations named one
// of names in the Go files directly inside dir (not in its subdirectories),
// test files excluded, ordered by name, path and line.
//
// dir is relative to the repository root, "" for the root itself. "Directly
// inside" is the Go package rule: a directory is one package, and its
// subdirectories are other packages. Test files are excluded because no
// importer can reach a declaration in a _test.go file. Package-level means
// the symbol's qualified name is its file and its bare name — a method or a
// field carries its owner in between and is never a package member.
func (r *Registry) GoPackageSymbols(ctx context.Context, runID int64, dir string, names []string) ([]SymbolHit, error) {
	if len(names) == 0 {
		return nil, nil
	}
	var out []SymbolHit
	// Chunked so a package referenced by thousands of distinct names stays
	// under SQLite's bound-parameter limit.
	const chunk = 400
	for start := 0; start < len(names); start += chunk {
		part := names[start:min(start+chunk, len(names))]
		hits, err := r.goPackageSymbols(ctx, runID, dir, part)
		if err != nil {
			return nil, err
		}
		out = append(out, hits...)
	}
	return out, nil
}

func (r *Registry) goPackageSymbols(ctx context.Context, runID int64, dir string, names []string) ([]SymbolHit, error) {
	args := []any{runID}
	inDir := `f.path NOT LIKE '%/%'`
	if dir != "" {
		esc := likeEscape(dir)
		inDir = `f.path LIKE ? ESCAPE '\' AND f.path NOT LIKE ? ESCAPE '\'`
		args = append(args, esc+"/%", esc+"/%/%")
	}
	kinds := make([]string, len(goPackageKinds))
	for i, k := range goPackageKinds {
		kinds[i] = "?"
		args = append(args, k)
	}
	ph := make([]string, len(names))
	for i, n := range names {
		ph[i] = "?"
		args = append(args, n)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.id, s.name, s.fqn, s.kind, f.path, s.start_line, s.end_line,
		       s.confidence, s.source, s.exported
		  FROM symbols s
		  JOIN files f ON f.id = s.file_id
		 WHERE s.index_run_id = ?
		   AND f.path LIKE '%.go' AND f.path NOT LIKE '%\_test.go' ESCAPE '\'
		   AND `+inDir+`
		   AND s.kind IN (`+strings.Join(kinds, ",")+`)
		   AND s.exported = 1
		   AND s.fqn = f.path || ':' || s.name
		   AND s.name IN (`+strings.Join(ph, ",")+`)
		 ORDER BY s.name, f.path, s.start_line, s.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("registry: go package symbols in %q: %w", dir, err)
	}
	defer func() { _ = rows.Close() }()
	var out []SymbolHit
	for rows.Next() {
		var h SymbolHit
		var exported int
		if err := rows.Scan(&h.ID, &h.Name, &h.FQN, &h.Kind, &h.FilePath,
			&h.StartLine, &h.EndLine, &h.Confidence, &h.Source, &exported); err != nil {
			return nil, fmt.Errorf("registry: go package symbols: scan: %w", err)
		}
		h.Exported = exported == 1
		out = append(out, h)
	}
	return out, rows.Err()
}

// GoPackageName returns the name in the package clause of the Go package in
// dir, read from the first non-test Go file directly inside it, or "" when the
// index holds no such file or its clause cannot be read.
//
// The clause, not the directory, is what an unaliased import binds: the
// package at gopkg.in/yaml.v3 is called yaml, and one at .../go-redis is
// usually called redis. Reading it is what lets a call written against that
// name be recognised.
func (r *Registry) GoPackageName(ctx context.Context, runID int64, dir string) (string, error) {
	args := []any{runID}
	inDir := `f.path NOT LIKE '%/%'`
	if dir != "" {
		esc := likeEscape(dir)
		inDir = `f.path LIKE ? ESCAPE '\' AND f.path NOT LIKE ? ESCAPE '\'`
		args = append(args, esc+"/%", esc+"/%/%")
	}
	var content string
	err := r.db.QueryRowContext(ctx, `
		SELECT files_fts.content
		  FROM files f
		  JOIN files_fts_map m ON m.file_id = f.id
		  JOIN files_fts       ON files_fts.rowid = m.fts_rowid
		 WHERE f.index_run_id = ?
		   AND f.path LIKE '%.go' AND f.path NOT LIKE '%\_test.go' ESCAPE '\'
		   AND `+inDir+`
		 ORDER BY f.path
		 LIMIT 1`, args...).Scan(&content)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("registry: go package name in %q: %w", dir, err)
	}
	return goPackageClause(content), nil
}

// goPackageClause reads the name from a Go file's package clause, skipping the
// comments and build constraints that may precede it.
func goPackageClause(src string) string {
	inBlock := false
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(raw)
		if inBlock {
			end := strings.Index(line, "*/")
			if end < 0 {
				continue
			}
			inBlock = false
			line = strings.TrimSpace(line[end+2:])
		}
		if strings.HasPrefix(line, "/*") {
			if end := strings.Index(line[2:], "*/"); end >= 0 {
				line = strings.TrimSpace(line[2+end+2:])
			} else {
				inBlock = true
				continue
			}
		}
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		rest, ok := strings.CutPrefix(line, "package")
		if !ok || rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
			return ""
		}
		name := strings.Fields(rest)
		if len(name) == 0 {
			return ""
		}
		return strings.TrimSuffix(name[0], ";")
	}
	return ""
}

// goDirClause is the SQL condition, and its arguments, for "a file directly
// inside dir": the Go package rule, in which each subdirectory is another
// package. "" is the repository root.
func goDirClause(dir string) (string, []any) {
	if dir == "" {
		return `f.path NOT LIKE '%/%'`, nil
	}
	esc := likeEscape(dir)
	return `f.path LIKE ? ESCAPE '\' AND f.path NOT LIKE ? ESCAPE '\'`, []any{esc + "/%", esc + "/%/%"}
}

// GoPackageMethods returns the exported methods named one of names declared in
// the Go files directly inside dir, test files excluded, ordered by name, path
// and line. A concrete type's methods and the methods an interface type
// declares are both methods here: the parser records each with its owner
// between the file and the name (lib.go:Client.Send, lib.go:Sender.Send).
//
// It is the method half of GoPackageSymbols, kept apart because a qualified
// call `pkg.Name()` never names a method, and folding methods into that read
// would turn every package-qualified lookup into a list of methods as well.
// Stitching asks for methods only once it knows the receiver's type, and picks
// the owner out of the qualified name itself.
func (r *Registry) GoPackageMethods(ctx context.Context, runID int64, dir string, names []string) ([]SymbolHit, error) {
	var out []SymbolHit
	// Chunked for the same bound-parameter reason as GoPackageSymbols.
	const chunk = 400
	for start := 0; start < len(names); start += chunk {
		hits, err := r.goPackageMethods(ctx, runID, dir, names[start:min(start+chunk, len(names))])
		if err != nil {
			return nil, err
		}
		out = append(out, hits...)
	}
	return out, nil
}

func (r *Registry) goPackageMethods(ctx context.Context, runID int64, dir string, names []string) ([]SymbolHit, error) {
	inDir, dirArgs := goDirClause(dir)
	args := append([]any{runID}, dirArgs...)
	args = append(args, string(model.KindMethod))
	ph := make([]string, len(names))
	for i, n := range names {
		ph[i] = "?"
		args = append(args, n)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.id, s.name, s.fqn, s.kind, f.path, s.start_line, s.end_line,
		       s.confidence, s.source, s.exported
		  FROM symbols s
		  JOIN files f ON f.id = s.file_id
		 WHERE s.index_run_id = ?
		   AND f.path LIKE '%.go' AND f.path NOT LIKE '%\_test.go' ESCAPE '\'
		   AND `+inDir+`
		   AND s.kind = ?
		   AND s.exported = 1
		   AND s.name IN (`+strings.Join(ph, ",")+`)
		 ORDER BY s.name, f.path, s.start_line, s.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("registry: go package methods in %q: %w", dir, err)
	}
	defer func() { _ = rows.Close() }()
	var out []SymbolHit
	for rows.Next() {
		var h SymbolHit
		var exported int
		if err := rows.Scan(&h.ID, &h.Name, &h.FQN, &h.Kind, &h.FilePath,
			&h.StartLine, &h.EndLine, &h.Confidence, &h.Source, &exported); err != nil {
			return nil, fmt.Errorf("registry: go package methods: scan: %w", err)
		}
		h.Exported = exported == 1
		out = append(out, h)
	}
	return out, rows.Err()
}

// GoPackageFiles returns the indexed text of every non-test Go file directly
// inside dir ("" for the repository root), ordered by path.
//
// Stitching reads a provider package's declarations from this text — the type
// a variable declares, the result a function declares, the fields a struct
// embeds — because the index records none of them for Go: the parser leaves Go
// out of return types (internal/parse/returns.go), since the type checker
// answers Go inside one repository, and no checker spans two.
func (r *Registry) GoPackageFiles(ctx context.Context, runID int64, dir string) ([]FileText, error) {
	inDir, dirArgs := goDirClause(dir)
	args := append([]any{runID}, dirArgs...)
	rows, err := r.db.QueryContext(ctx, `
		SELECT f.path, files_fts.content
		  FROM files f
		  JOIN files_fts_map m ON m.file_id = f.id
		  JOIN files_fts       ON files_fts.rowid = m.fts_rowid
		 WHERE f.index_run_id = ?
		   AND f.path LIKE '%.go' AND f.path NOT LIKE '%\_test.go' ESCAPE '\'
		   AND `+inDir+`
		 ORDER BY f.path`, args...)
	if err != nil {
		return nil, fmt.Errorf("registry: go package files in %q: %w", dir, err)
	}
	defer func() { _ = rows.Close() }()
	var out []FileText
	for rows.Next() {
		var ft FileText
		if err := rows.Scan(&ft.Path, &ft.Content); err != nil {
			return nil, fmt.Errorf("registry: go package files: scan: %w", err)
		}
		out = append(out, ft)
	}
	return out, rows.Err()
}
