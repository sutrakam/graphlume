package registry

import (
	"context"
	"fmt"
)

// SupertypeRefs is what resolution did with the references that name a type as
// a supertype: `extends T`, `implements T`, `impl T for X`, `class C(T)`.
//
// Each such reference that binds to T and has a type to hang on becomes an
// extends or implements edge, and implementations lists it. The others are
// subtypes of T that are named in the source and absent from the list.
type SupertypeRefs struct {
	// References is every supertype reference spelled like the name.
	References int64

	// Linked were bound to the type and became an edge.
	Linked int64

	// NoSource were bound to the type and became no edge, because the type that
	// does the extending has no definition in this index: `impl Matcher for
	// ExternalType`. They are subtypes the index saw and cannot list.
	NoSource int64

	// Ambiguous, Unresolved and Pending were bound to nothing. Any of them may
	// name this type; the spelling is all that is known.
	Ambiguous  int64
	Unresolved int64
	Pending    int64
}

// Outstanding is the supertype references that name this spelling and are not an
// edge to this type.
func (s SupertypeRefs) Outstanding() int64 {
	return s.NoSource + s.Ambiguous + s.Unresolved + s.Pending
}

// hierarchyRefKinds selects the reference rows that state a supertype. The
// parse queries name them reference.extends, reference.inherit and
// reference.implementation.
const hierarchyRefKinds = `(sr.kind LIKE '%extend%' OR sr.kind LIKE '%inherit%' OR sr.kind LIKE '%implement%')`

// SupertypeReferences counts the supertype references spelled name, by what
// became of each, as seen from the definition symbolID.
//
// Both statements are index reads (idx_refs_run_name, then the edges into the
// definition); the second is a set difference for the same reason NameBinding's
// is.
func (r *Registry) SupertypeReferences(ctx context.Context, runID, symbolID int64, name string) (SupertypeRefs, error) {
	var out SupertypeRefs
	rows, err := r.db.QueryContext(ctx, `
		SELECT sr.resolution, COALESCE(sr.resolved_symbol_id, 0) = ?, COUNT(*)
		  FROM symbol_references sr INDEXED BY idx_refs_run_name
		 WHERE sr.index_run_id = ? AND sr.name = ? AND `+hierarchyRefKinds+`
		 GROUP BY 1, 2`, symbolID, runID, name)
	if err != nil {
		return out, fmt.Errorf("registry: supertype references named %q: %w", name, err)
	}
	defer rows.Close()
	var boundHere int64
	for rows.Next() {
		var resolution string
		var here bool
		var n int64
		if err := rows.Scan(&resolution, &here, &n); err != nil {
			return out, fmt.Errorf("registry: supertype references named %q: scan: %w", name, err)
		}
		out.References += n
		switch Resolution(resolution) {
		case ResolutionResolved:
			if here {
				boundHere += n
			}
		case ResolutionAmbiguous:
			out.Ambiguous += n
		case ResolutionUnresolved:
			out.Unresolved += n
		case ResolutionPending:
			out.Pending += n
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if boundHere == 0 {
		return out, nil
	}
	var withoutEdge int64
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM (
			SELECT f.path, sr.start_line
			  FROM symbol_references sr
			  JOIN files f ON f.id = sr.file_id
			 WHERE sr.resolved_symbol_id = ? AND sr.index_run_id = ?
			   AND sr.resolution = 'resolved' AND sr.name = ? AND `+hierarchyRefKinds+`
			EXCEPT
			SELECT e.evidence_file, e.evidence_line
			  FROM edges e
			 WHERE e.index_run_id = ? AND e.dst_id = ? AND e.kind IN ('IMPLEMENTS','EXTENDS'))`,
		symbolID, runID, name, runID, symbolID).Scan(&withoutEdge); err != nil {
		return out, fmt.Errorf("registry: supertype references to %q without an edge: %w", name, err)
	}
	out.NoSource = withoutEdge
	out.Linked = boundHere - withoutEdge
	return out, nil
}
