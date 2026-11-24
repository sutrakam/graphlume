package registry

import (
	"context"
	"fmt"
	"strings"
)

// Reads for the dependency reachability report.
//
// DependencyReport answers "how many files import this dependency". A
// reachability answer has to show its evidence — which files, at which line,
// under which alias, and which references sit beside the import — so these two
// reads return the rows themselves rather than a count.

// ImportSite is one recorded import with the file it is in.
type ImportSite struct {
	File     string
	Language string
	Path     string
	Alias    string
	Line     int
}

// ImportSites returns every import of one run, ordered by file and line.
//
// The whole table, deliberately: which imports belong to a dependency is
// ecosystem knowledge this layer does not have (see ImportMatcher), so the
// caller filters. A repository's imports number in the tens of thousands of
// short rows.
func (r *Registry) ImportSites(ctx context.Context, runID int64) ([]ImportSite, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT f.path, f.language, i.path, i.alias, i.start_line
		  FROM file_imports i JOIN files f ON f.id = i.file_id
		 WHERE i.index_run_id = ?
		 ORDER BY f.path, i.start_line, i.path, i.alias`, runID)
	if err != nil {
		return nil, fmt.Errorf("registry: import sites: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []ImportSite
	for rows.Next() {
		var s ImportSite
		if err := rows.Scan(&s.File, &s.Language, &s.Path, &s.Alias, &s.Line); err != nil {
			return nil, fmt.Errorf("registry: import sites: scan: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ReferenceSite is one recorded reference with the file it is in.
//
// Resolution is not part of it. A reference to a symbol defined in a
// DEPENDENCY never resolves — the index holds no definition to bind it to —
// so the question a reachability report asks is the lexical one: does this
// name, with this qualifier, appear in a file that imports the package.
type ReferenceSite struct {
	File     string
	Name     string
	Receiver string
	Kind     string
	Line     int
}

// referenceNameChunk keeps an IN list well under SQLite's bound-parameter limit.
const referenceNameChunk = 400

// ReferencesNamed returns the references of one run whose name is one of
// names, ordered by file and line. Served by idx_refs_run_name.
func (r *Registry) ReferencesNamed(ctx context.Context, runID int64, names []string) ([]ReferenceSite, error) {
	var out []ReferenceSite
	for start := 0; start < len(names); start += referenceNameChunk {
		end := min(start+referenceNameChunk, len(names))
		chunk := names[start:end]

		args := make([]any, 0, len(chunk)+1)
		args = append(args, runID)
		for _, n := range chunk {
			args = append(args, n)
		}
		rows, err := r.db.QueryContext(ctx, `
			SELECT f.path, r.name, r.receiver, r.kind, r.start_line
			  FROM symbol_references r JOIN files f ON f.id = r.file_id
			 WHERE r.index_run_id = ? AND r.name IN (?`+strings.Repeat(",?", len(chunk)-1)+`)
			 ORDER BY f.path, r.start_line, r.start_col`, args...)
		if err != nil {
			return nil, fmt.Errorf("registry: references named: %w", err)
		}
		for rows.Next() {
			var s ReferenceSite
			if err := rows.Scan(&s.File, &s.Name, &s.Receiver, &s.Kind, &s.Line); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("registry: references named: scan: %w", err)
			}
			out = append(out, s)
		}
		_ = rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}
