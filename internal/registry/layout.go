package registry

import (
	"context"
	"fmt"
)

// RunLayout returns what resolution needs to know about a run as a whole:
// every indexed file's path, and the name of every dependency its manifests
// declare.
//
// Two bounded reads, made once per resolve. They exist so that a call
// qualified by a package can be told apart by WHERE the package is — in the
// repository, or outside it — without a query per reference: the resolver
// builds a directory index from the paths and asks that.
//
// The paths are every row of `files`, parsed or not. A file that was skipped
// for its size or failed to parse still makes its directory part of the
// repository, and a call into it is an unresolved reference, not an external
// one.
func (r *Registry) RunLayout(ctx context.Context, runID int64) (paths, dependencies []string, err error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT path FROM files WHERE index_run_id = ? ORDER BY path`, runID)
	if err != nil {
		return nil, nil, fmt.Errorf("registry: run layout: files: %w", err)
	}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		paths = append(paths, p)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, nil, err
	}
	_ = rows.Close()

	// No index covers kind. The name index was chosen anyway and fetched every
	// symbol row at random to read its kind: 13 s on 770,000 symbols, against
	// 2 s for the scan.
	rows, err = r.db.QueryContext(ctx,
		`SELECT DISTINCT name FROM symbols NOT INDEXED WHERE index_run_id = ? AND kind = 'dependency' ORDER BY name`, runID)
	if err != nil {
		return nil, nil, fmt.Errorf("registry: run layout: dependencies: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, nil, err
		}
		dependencies = append(dependencies, name)
	}
	return paths, dependencies, rows.Err()
}
