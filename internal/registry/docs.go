package registry

import (
	"context"
	"fmt"
)

// DocFile is a prose document the index holds: a Markdown file.
type DocFile struct {
	ID   int64
	Path string
}

// DocSection is a heading of a document and the lines it runs over. The index
// holds each Markdown heading as a definition, which is what lets a mention be
// attributed to the section it sits in.
type DocSection struct {
	Name      string
	StartLine int
	EndLine   int
}

// DocFiles lists the run's Markdown documents, by path.
func (r *Registry) DocFiles(ctx context.Context, runID int64) ([]DocFile, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, path FROM files WHERE index_run_id = ? AND language = 'markdown' ORDER BY path`, runID)
	if err != nil {
		return nil, fmt.Errorf("registry: list documents: %w", err)
	}
	defer rows.Close()
	var out []DocFile
	for rows.Next() {
		var d DocFile
		if err := rows.Scan(&d.ID, &d.Path); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DocSections lists a document's headings in order.
func (r *Registry) DocSections(ctx context.Context, runID, fileID int64) ([]DocSection, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT name, start_line, end_line FROM symbols
		  WHERE index_run_id = ? AND file_id = ? AND kind = 'package'
		  ORDER BY start_line, id`, runID, fileID)
	if err != nil {
		return nil, fmt.Errorf("registry: list sections: %w", err)
	}
	defer rows.Close()
	var out []DocSection
	for rows.Next() {
		var s DocSection
		if err := rows.Scan(&s.Name, &s.StartLine, &s.EndLine); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
