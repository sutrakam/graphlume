package gotypes

import (
	"github.com/sutrakam/graphlume/internal/resolve/scip"
	"github.com/sutrakam/graphlume/pkg/model"
)

// FileFacts converts the result into the form the SCIP importer lands in the
// graph, so that exact edges have one writer.
//
// Every relation is addressed by FQN and given the kind its fact supports.
// Source and Confidence are NOT set here: scip.Relation.Edge stamps
// confidence=exact and source=compiler together and validates them, which is
// the one place in the product that does.
func (r *Result) FileFacts() []scip.FileFacts {
	byPath := make(map[string]int, len(r.Files))
	out := make([]scip.FileFacts, 0, len(r.Files))
	for i := range r.Files {
		f := &r.Files[i]
		ff := scip.FileFacts{Path: f.Path, Language: "go", Tool: Tool}
		ff.Symbols = make([]model.Symbol, 0, len(f.Defs))
		for j, d := range f.Defs {
			ff.Symbols = append(ff.Symbols, model.Symbol{
				ID:   int64(-(j + 1)),
				Kind: d.Kind,
				Name: d.Name,
				FQN:  d.FQN(),
				Range: model.Range{
					Start: model.Position{Line: d.Line, Col: d.Col},
					End:   model.Position{Line: d.Line, Col: d.Col},
				},
				Confidence: model.ConfidenceExact,
				Source:     model.SourceCompiler,
			})
		}
		ff.Relations = make([]scip.Relation, 0, len(f.Uses))
		for _, u := range f.Uses {
			ff.Relations = append(ff.Relations, scip.Relation{
				SrcFQN: u.Encl.FQN(),
				DstFQN: u.Obj.FQN(),
				Kind:   u.Rel(),
				Evidence: model.Evidence{
					FilePath: f.Path,
					Range:    point(u.Line, u.Col),
					Detail:   Detail,
				},
			})
		}
		byPath[f.Path] = len(out)
		out = append(out, ff)
	}
	for _, im := range r.Impls {
		i, ok := byPath[im.Type.File]
		if !ok {
			continue
		}
		out[i].Relations = append(out[i].Relations, scip.Relation{
			SrcFQN: im.Type.FQN(),
			DstFQN: im.Iface.FQN(),
			Kind:   model.EdgeImplements,
			Evidence: model.Evidence{
				FilePath: im.Type.File,
				Range:    point(im.Type.Line, im.Type.Col),
				Detail:   Detail,
			},
		})
		for _, m := range im.Methods {
			j, ok := byPath[m.Concrete.File]
			if !ok {
				continue
			}
			out[j].Relations = append(out[j].Relations, scip.Relation{
				SrcFQN: m.Concrete.FQN(),
				DstFQN: m.Iface.FQN(),
				Kind:   model.EdgeImplements,
				Evidence: model.Evidence{
					FilePath: m.Concrete.File,
					Range:    point(m.Concrete.Line, m.Concrete.Col),
					Detail:   Detail,
				},
			})
		}
	}
	return out
}

func point(line, col int) model.Range {
	p := model.Position{Line: line, Col: col}
	return model.Range{Start: p, End: p}
}

// CallSites lists every call whose callee identifier the checker saw, in the
// form the importer uses to withdraw contradicted heuristic edges.
func (r *Result) CallSites() []CallSiteFact {
	var out []CallSiteFact
	for i := range r.Files {
		f := &r.Files[i]
		for _, c := range f.Callees {
			out = append(out, CallSiteFact{Path: f.Path, Line: c.Line, Name: c.Name, Known: c.Known, DstFQN: c.Dst})
		}
	}
	return out
}

// CallSiteFact is one Callee with its file.
type CallSiteFact struct {
	Path   string
	Line   int
	Name   string
	Known  bool
	DstFQN string
}
