package registry

import (
	"context"
	"fmt"
	"strings"
)

// fileSizeChunk bounds one IN list, for the same reason structureChunk does.
const fileSizeChunk = 400

// FileSizes returns the size in bytes, as recorded when the file was indexed,
// of each named file that runID contains.
//
// It exists for the session usage report (internal/mcp), which states how large
// the files an answer named are. The caller names the paths, so the cost
// follows the size of one answer and never the size of the repository. A path
// the run does not hold is absent from the result rather than present as zero:
// "not a file in this index" and "an empty file" are different facts.
func (r *Registry) FileSizes(ctx context.Context, runID int64, paths []string) (map[string]int64, error) {
	out := make(map[string]int64, len(paths))
	for start := 0; start < len(paths); start += fileSizeChunk {
		chunk := paths[start:min(start+fileSizeChunk, len(paths))]

		args := make([]any, 0, len(chunk)+1)
		args = append(args, runID)
		for _, p := range chunk {
			args = append(args, p)
		}
		// The (index_run_id, path) uniqueness constraint is the index this
		// reads through, so each path is one lookup.
		rows, err := r.db.QueryContext(ctx, `
			SELECT path, size_bytes
			  FROM files
			 WHERE index_run_id = ?
			   AND path IN (`+strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("registry: file sizes for run %d: %w", runID, err)
		}
		for rows.Next() {
			var (
				path string
				size int64
			)
			if err := rows.Scan(&path, &size); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("registry: scan file size: %w", err)
			}
			out[path] = size
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("registry: file sizes for run %d: %w", runID, err)
		}
		_ = rows.Close()
	}
	return out, nil
}
