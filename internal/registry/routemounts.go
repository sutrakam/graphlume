package registry

import (
	"context"
	"fmt"
	"strings"

	"github.com/sutrakam/graphlume/pkg/model"
)

// FileText is one indexed file's path and text.
type FileText struct {
	Path    string
	Content string
}

// PythonMountFiles returns the indexed Python files that mount a router or
// blueprint: the ones that can give a route in another file its prefix.
//
// Found through the full-text index, so the cost follows the number of such
// files (a handful) rather than the size of the repository. The tokenizer
// splits on '_', so the phrase "include router" is include_router; a file that
// merely says the words is filtered out by the caller's own parse.
func (r *Registry) PythonMountFiles(ctx context.Context, runID int64) ([]FileText, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT f.path, files_fts.content
		  FROM files_fts
		  JOIN files_fts_map m ON m.fts_rowid = files_fts.rowid
		  JOIN files f         ON f.id = m.file_id
		 WHERE files_fts MATCH ?
		   AND f.index_run_id = ?
		 ORDER BY f.path`,
		`content : "include router" OR content : "register blueprint"`, runID)
	if err != nil {
		return nil, fmt.Errorf("registry: python mount files: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []FileText
	for rows.Next() {
		var ft FileText
		if err := rows.Scan(&ft.Path, &ft.Content); err != nil {
			return nil, err
		}
		if strings.HasSuffix(ft.Path, ".py") {
			out = append(out, ft)
		}
	}
	return out, rows.Err()
}

// EndpointRow is one endpoint symbol with where it was declared.
type EndpointRow struct {
	ID   int64
	Name string
	FQN  string
	Path string
	Line int
	// Handler and Doc are read by PythonEndpoints only.
	Handler    string
	Doc        string
	Confidence model.Confidence
}

// PythonEndpoints returns every endpoint the run's Python files declare.
func (r *Registry) PythonEndpoints(ctx context.Context, runID int64) ([]EndpointRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.id, s.name, s.fqn, f.path, s.start_line,
		       COALESCE(s.signature, ''), COALESCE(s.doc, ''), s.confidence
		  FROM symbols s
		  JOIN files f ON f.id = s.file_id
		 WHERE s.index_run_id = ? AND s.kind = ? AND f.path LIKE '%.py'
		 ORDER BY f.path, s.start_line, s.id`,
		runID, string(model.KindEndpoint))
	if err != nil {
		return nil, fmt.Errorf("registry: python endpoints: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []EndpointRow
	for rows.Next() {
		var e EndpointRow
		if err := rows.Scan(&e.ID, &e.Name, &e.FQN, &e.Path, &e.Line, &e.Handler, &e.Doc, &e.Confidence); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ExpressMountFiles returns the indexed JS/TS files that may mount an Express
// router: those that call use() and import something. "use" is a common word,
// so the extension is filtered in SQL rather than after every matching file's
// text has been read.
func (r *Registry) ExpressMountFiles(ctx context.Context, runID int64) ([]FileText, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT f.path, files_fts.content
		  FROM files_fts
		  JOIN files_fts_map m ON m.fts_rowid = files_fts.rowid
		  JOIN files f         ON f.id = m.file_id
		 WHERE files_fts MATCH ?
		   AND f.index_run_id = ?
		   AND `+jsPathSQL+`
		 ORDER BY f.path`,
		`content : "use" AND (content : "require" OR content : "import")`, runID)
	if err != nil {
		return nil, fmt.Errorf("registry: express mount files: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []FileText
	for rows.Next() {
		var ft FileText
		if err := rows.Scan(&ft.Path, &ft.Content); err != nil {
			return nil, err
		}
		if IsJSPath(ft.Path) {
			out = append(out, ft)
		}
	}
	return out, rows.Err()
}

// ExpressEndpoints returns every endpoint the run's JS/TS files declare.
func (r *Registry) ExpressEndpoints(ctx context.Context, runID int64) ([]EndpointRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.id, s.name, s.fqn, f.path, s.start_line
		  FROM symbols s
		  JOIN files f ON f.id = s.file_id
		 WHERE s.index_run_id = ? AND s.kind = ?
		   AND `+jsPathSQL+`
		 ORDER BY f.path, s.start_line, s.id`,
		runID, string(model.KindEndpoint))
	if err != nil {
		return nil, fmt.Errorf("registry: express endpoints: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []EndpointRow
	for rows.Next() {
		var e EndpointRow
		if err := rows.Scan(&e.ID, &e.Name, &e.FQN, &e.Path, &e.Line); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// jsExts are the JavaScript and TypeScript source extensions.
var jsExts = []string{".js", ".ts", ".mjs", ".cjs", ".jsx", ".tsx", ".mts", ".cts"}

// jsPathSQL restricts f.path to jsExts.
var jsPathSQL = func() string {
	parts := make([]string, len(jsExts))
	for i, e := range jsExts {
		parts[i] = "f.path LIKE '%" + e + "'"
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}()

// IsJSPath reports whether a path is a JavaScript or TypeScript source.
func IsJSPath(p string) bool {
	for _, e := range jsExts {
		if strings.HasSuffix(p, e) {
			return true
		}
	}
	return false
}

// RenameEndpoints sets the display name of endpoint symbols, by id. The FQN,
// which is the symbol's identity, is left alone.
func (r *Registry) RenameEndpoints(ctx context.Context, names map[int64]string) error {
	if len(names) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("registry: rename endpoints: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `UPDATE symbols SET name = ? WHERE id = ? AND kind = ?`)
	if err != nil {
		return fmt.Errorf("registry: rename endpoints: prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()
	for id, name := range names {
		if _, err := stmt.ExecContext(ctx, name, id, string(model.KindEndpoint)); err != nil {
			return fmt.Errorf("registry: rename endpoint %d: %w", id, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE index_runs 
		   SET mutation_seq = mutation_seq + 1 
		 WHERE id IN (SELECT DISTINCT index_run_id FROM symbols WHERE kind = ?)`,
		string(model.KindEndpoint)); err != nil {
		return fmt.Errorf("registry: rename endpoints: bump mutation_seq: %w", err)
	}
	return tx.Commit()
}

// GoRouterFiles returns the indexed Go files that import a router package
// (gin, chi, echo, fiber, gorilla/mux) or are a module's go.mod. Found
// through the full-text index: an import path's parts are tokens, so a file
// importing github.com/gin-gonic/gin holds the token "gin".
func (r *Registry) GoRouterFiles(ctx context.Context, runID int64) ([]FileText, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT f.path, files_fts.content
		  FROM files_fts
		  JOIN files_fts_map m ON m.fts_rowid = files_fts.rowid
		  JOIN files f         ON f.id = m.file_id
		 WHERE files_fts MATCH ?
		   AND f.index_run_id = ?
		   AND (f.path LIKE '%.go' OR f.path = 'go.mod' OR f.path LIKE '%/go.mod')
		 ORDER BY f.path`,
		`content : "gin" OR content : "chi" OR content : "echo" OR content : "fiber" OR content : "mux" OR content : "module"`, runID)
	if err != nil {
		return nil, fmt.Errorf("registry: go router files: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []FileText
	for rows.Next() {
		var ft FileText
		if err := rows.Scan(&ft.Path, &ft.Content); err != nil {
			return nil, err
		}
		out = append(out, ft)
	}
	return out, rows.Err()
}

// GoEndpoints returns every endpoint the run's Go files declare.
func (r *Registry) GoEndpoints(ctx context.Context, runID int64) ([]EndpointRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.id, s.name, s.fqn, f.path, s.start_line
		  FROM symbols s
		  JOIN files f ON f.id = s.file_id
		 WHERE s.index_run_id = ? AND s.kind = ? AND f.path LIKE '%.go'
		 ORDER BY f.path, s.start_line, s.id`,
		runID, string(model.KindEndpoint))
	if err != nil {
		return nil, fmt.Errorf("registry: go endpoints: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []EndpointRow
	for rows.Next() {
		var e EndpointRow
		if err := rows.Scan(&e.ID, &e.Name, &e.FQN, &e.Path, &e.Line); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// JSFilesMatching returns the run's JS/TS files a full-text query matches,
// with their text.
func (r *Registry) JSFilesMatching(ctx context.Context, runID int64, query string) ([]FileText, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT f.path, files_fts.content
		  FROM files_fts
		  JOIN files_fts_map m ON m.fts_rowid = files_fts.rowid
		  JOIN files f         ON f.id = m.file_id
		 WHERE files_fts MATCH ?
		   AND f.index_run_id = ?
		   AND `+jsPathSQL+`
		 ORDER BY f.path`, query, runID)
	if err != nil {
		return nil, fmt.Errorf("registry: js files matching: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []FileText
	for rows.Next() {
		var ft FileText
		if err := rows.Scan(&ft.Path, &ft.Content); err != nil {
			return nil, err
		}
		out = append(out, ft)
	}
	return out, rows.Err()
}

// FilesImporting returns the run's files that import any of the given import
// paths, sorted.
func (r *Registry) FilesImporting(ctx context.Context, runID int64, importPaths []string) ([]string, error) {
	if len(importPaths) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(importPaths)+1)
	args = append(args, runID)
	marks := make([]string, len(importPaths))
	for i, p := range importPaths {
		marks[i] = "?"
		args = append(args, p)
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT f.path
		  FROM file_imports i
		  JOIN files f ON f.id = i.file_id
		 WHERE i.index_run_id = ? AND i.path IN (`+strings.Join(marks, ",")+`)
		 ORDER BY f.path`, args...)
	if err != nil {
		return nil, fmt.Errorf("registry: files importing: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PythonFilesMatching returns the run's Python files whose text matches a
// full-text query, with their text.
func (r *Registry) PythonFilesMatching(ctx context.Context, runID int64, query string) ([]FileText, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT f.path, files_fts.content
		  FROM files_fts
		  JOIN files_fts_map m ON m.fts_rowid = files_fts.rowid
		  JOIN files f         ON f.id = m.file_id
		 WHERE files_fts MATCH ?
		   AND f.index_run_id = ?
		   AND f.path LIKE '%.py'
		 ORDER BY f.path`, query, runID)
	if err != nil {
		return nil, fmt.Errorf("registry: python files matching: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []FileText
	for rows.Next() {
		var ft FileText
		if err := rows.Scan(&ft.Path, &ft.Content); err != nil {
			return nil, err
		}
		out = append(out, ft)
	}
	return out, rows.Err()
}

// DerivedEndpoint is an endpoint a whole-run pass derives from a route
// declared elsewhere (Template): a router factory's route as one call site
// serves it, or a table loop's route for one row of an imported table.
type DerivedEndpoint struct {
	// Path is the file the endpoint is placed in: the call site's.
	Path       string
	Line       int
	Name       string
	FQN        string
	Handler    string
	Doc        string
	Confidence model.Confidence
	// Template is the declared endpoint it is derived from. Its outgoing
	// edges (handler, middlewares) are the derived endpoint's too.
	Template int64
}

// ReplaceDerivedEndpoints replaces every derived endpoint of the run (an
// endpoint whose FQN carries isDerived's marker) with eps, copying each one's
// outgoing edges from its template. Deleting first makes the pass that
// derives them idempotent: a call site removed, or a factory changed, takes
// its endpoints with it.
func (r *Registry) ReplaceDerivedEndpoints(ctx context.Context, runID int64, marker string, eps []DerivedEndpoint) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("registry: derived endpoints: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		DELETE FROM symbols
		 WHERE index_run_id = ? AND kind = ? AND instr(fqn, ?) > 0`,
		runID, string(model.KindEndpoint), marker)
	if err != nil {
		return 0, fmt.Errorf("registry: derived endpoints: delete: %w", err)
	}
	removed, _ := res.RowsAffected()

	fileIDs := map[string]int64{}
	written := 0
	for _, e := range eps {
		if e.Confidence == model.ConfidenceExact {
			return 0, fmt.Errorf("registry: derived endpoint %s: exact needs a compiler", e.FQN)
		}
		id, ok := fileIDs[e.Path]
		if !ok {
			err := tx.QueryRowContext(ctx, `SELECT id FROM files WHERE index_run_id = ? AND path = ?`, runID, e.Path).Scan(&id)
			if err != nil {
				return 0, fmt.Errorf("registry: derived endpoint file %s: %w", e.Path, err)
			}
			fileIDs[e.Path] = id
		}
		res, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO symbols
				(index_run_id, file_id, kind, name, fqn, start_line, end_line,
				 signature, doc, exported, confidence, source)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
			runID, id, string(model.KindEndpoint), e.Name, e.FQN, e.Line, e.Line,
			e.Handler, e.Doc, string(e.Confidence), string(model.SourceSourceCode))
		if err != nil {
			return 0, fmt.Errorf("registry: derived endpoint %s: %w", e.FQN, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		newID, err := res.LastInsertId()
		if err != nil {
			return 0, err
		}
		written++
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO edges
				(index_run_id, src_id, dst_id, kind, confidence,
				 evidence_file, evidence_line, source, detail)
			SELECT index_run_id, ?, dst_id, kind,
			       CASE WHEN confidence IN ('exact', 'high') AND ? = 'medium' THEN 'medium' ELSE confidence END,
			       ?, ?, source, detail
			  FROM edges
			 WHERE index_run_id = ? AND src_id = ? AND confidence <> 'exact'`,
			newID, string(e.Confidence), e.Path, e.Line, runID, e.Template); err != nil {
			return 0, fmt.Errorf("registry: derived endpoint %s: edges: %w", e.FQN, err)
		}
	}
	if removed > 0 || written > 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE index_runs SET mutation_seq = mutation_seq + 1 WHERE id = ?`, runID); err != nil {
			return 0, fmt.Errorf("registry: derived endpoints: bump mutation_seq: %w", err)
		}
	}
	return written, tx.Commit()
}

// SetEndpointDocs sets the doc of endpoint symbols, by id.
func (r *Registry) SetEndpointDocs(ctx context.Context, docs map[int64]string) error {
	if len(docs) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("registry: endpoint docs: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `UPDATE symbols SET doc = ? WHERE id = ? AND kind = ?`)
	if err != nil {
		return fmt.Errorf("registry: endpoint docs: prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()
	for id, doc := range docs {
		if _, err := stmt.ExecContext(ctx, doc, id, string(model.KindEndpoint)); err != nil {
			return fmt.Errorf("registry: endpoint doc %d: %w", id, err)
		}
	}
	return tx.Commit()
}

// SetEndpointConfidence sets the confidence of endpoint symbols, by id: a
// declared route renamed after the path a factory call serves it at is graded
// as that call's evidence is. Never exact: that is a compiler's claim.
func (r *Registry) SetEndpointConfidence(ctx context.Context, conf map[int64]model.Confidence) error {
	if len(conf) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("registry: endpoint confidence: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for id, c := range conf {
		if c == model.ConfidenceExact {
			return fmt.Errorf("registry: endpoint %d: exact needs a compiler", id)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE symbols SET confidence = ? WHERE id = ? AND kind = ? AND source <> ?`,
			string(c), id, string(model.KindEndpoint), string(model.SourceCompiler)); err != nil {
			return fmt.Errorf("registry: endpoint confidence %d: %w", id, err)
		}
	}
	return tx.Commit()
}

// RuntimeRoutes returns the endpoints whose doc is doc: the routes registered
// at run time with a path the index could not read.
func (r *Registry) RuntimeRoutes(ctx context.Context, runID int64, doc string) ([]EndpointRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.id, s.name, s.fqn, f.path, s.start_line
		  FROM symbols s
		  JOIN files f ON f.id = s.file_id
		 WHERE s.index_run_id = ? AND s.kind = ? AND s.doc = ?
		 ORDER BY f.path, s.start_line, s.id`,
		runID, string(model.KindEndpoint), doc)
	if err != nil {
		return nil, fmt.Errorf("registry: runtime routes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []EndpointRow
	for rows.Next() {
		var e EndpointRow
		if err := rows.Scan(&e.ID, &e.Name, &e.FQN, &e.Path, &e.Line); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
