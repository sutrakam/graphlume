package registry

import (
	"context"
	"fmt"

	"github.com/sutrakam/graphlume/pkg/model"
)

// ReferenceEdgeTiers counts, by confidence, the edges of the kinds reference
// resolution produces: CALLS, INSTANTIATES, REFERENCES, EXTENDS and IMPLEMENTS.
//
// It counts edge ROWS, which is not the number of resolved references. Several
// references from one definition to another share a single edge, so a run
// with 2,932 resolved references can hold 2,342 edges, and a tier that has
// references in it can have no edge of its own when every one of them
// duplicates a stronger edge. `graphlume resolve` printed the per-tier
// reference counts under the word "Edges", where they neither summed to the
// edge count nor matched the table.
//
// A constructor call is an INSTANTIATES edge, not a CALLS edge, and the
// resolver's own count includes it, so leaving it out here printed more edges
// written than exist.
//
// Edges a compiler index supplied are included, at `exact`. Edges of other
// kinds (an endpoint exposing its handler, a statement reading a table) are
// not: they are written by other passes and reported on their own lines.
func (r *Registry) ReferenceEdgeTiers(ctx context.Context, runID int64) (map[model.Confidence]int64, int64, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT confidence, count(*) FROM edges
		 WHERE index_run_id = ? AND kind IN (?, ?, ?, ?, ?)
		 GROUP BY confidence ORDER BY confidence`,
		runID, string(model.EdgeCalls), string(model.EdgeInstantiates), string(model.EdgeReferences),
		string(model.EdgeExtends), string(model.EdgeImplements))
	if err != nil {
		return nil, 0, fmt.Errorf("registry: reference edge tiers: %w", err)
	}
	defer rows.Close()

	tiers := map[model.Confidence]int64{}
	var total int64
	for rows.Next() {
		var conf string
		var n int64
		if err := rows.Scan(&conf, &n); err != nil {
			return nil, 0, fmt.Errorf("registry: reference edge tiers: %w", err)
		}
		tiers[model.Confidence(conf)] = n
		total += n
	}
	return tiers, total, rows.Err()
}
