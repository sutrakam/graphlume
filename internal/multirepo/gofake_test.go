package multirepo

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// fakeGo is a hand-built member that satisfies both MemberSource (through the
// embedded fakeSource) and GoModuleSource, so stitching is exercised without
// a database. Its reads apply the same filters the registry's SQL does —
// unbound calls only, import paths matched on the module or module + "/",
// one package directory, exported package members, no test files — and the
// registry's own tests pin that SQL against the writer.
type fakeGo struct {
	*fakeSource
	gomods   map[string]string
	imports  map[string][]registry.Import // by file path
	calls    []fakeCall
	symbols  []registry.SymbolHit
	pkgNames map[string]string
	excerpts map[string]string // "path:line" -> text
	// sources are whole Go files by path: the provider's package text and
	// the importer's files that value calls are traced through.
	sources map[string]string
	// enclosing is by file path; ids are assigned from the file's position
	// in fileIDs.
	enclosing map[string][]registry.EnclosingSymbol
	incoming  map[int64][]registry.EdgeHit
}

type fakeCall struct {
	file, name, recv, recvKind, resolution string
	line, col                              int
}

func newFakeGo(rev string) *fakeGo {
	return &fakeGo{
		fakeSource: &fakeSource{run: readyRun(1, rev, at(0))},
		gomods:     map[string]string{}, imports: map[string][]registry.Import{},
		pkgNames: map[string]string{}, excerpts: map[string]string{}, sources: map[string]string{},
		enclosing: map[string][]registry.EnclosingSymbol{}, incoming: map[int64][]registry.EdgeHit{},
	}
}

func (f *fakeGo) fileID(p string) int64 {
	var paths []string
	for _, c := range f.calls {
		paths = append(paths, c.file)
	}
	sort.Strings(paths)
	for i, q := range paths {
		if q == p {
			return int64(i + 1)
		}
	}
	return 0
}

func (f *fakeGo) fileOf(id int64) string {
	for _, c := range f.calls {
		if f.fileID(c.file) == id {
			return c.file
		}
	}
	return ""
}

func (f *fakeGo) GoModFiles(context.Context, int64) ([]registry.FileText, error) {
	var out []registry.FileText
	for p, c := range f.gomods {
		out = append(out, registry.FileText{Path: p, Content: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (f *fakeGo) GoImportedCalls(_ context.Context, _ int64, modules []string, limit int) ([]registry.GoImportedCall, bool, error) {
	var out []registry.GoImportedCall
	for i, c := range f.calls {
		if c.resolution == "resolved" {
			continue
		}
		for _, imp := range f.imports[c.file] {
			in := false
			for _, m := range modules {
				in = in || importInModule(imp.Path, m)
			}
			if !in {
				continue
			}
			out = append(out, registry.GoImportedCall{
				RefID: int64(i + 1), FileID: f.fileID(c.file), FilePath: c.file, Line: c.line, Col: c.col,
				Name: c.name, Receiver: c.recv, ReceiverKind: c.recvKind, Resolution: c.resolution,
				ImportPath: imp.Path, ImportAlias: imp.Alias,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.FilePath != b.FilePath:
			return a.FilePath < b.FilePath
		case a.Line != b.Line:
			return a.Line < b.Line
		case a.RefID != b.RefID:
			return a.RefID < b.RefID
		case a.ImportPath != b.ImportPath:
			return a.ImportPath < b.ImportPath
		default:
			return a.ImportAlias < b.ImportAlias
		}
	})
	if limit > 0 && len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

func (f *fakeGo) GoPackageSymbols(_ context.Context, _ int64, dir string, names []string) ([]registry.SymbolHit, error) {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var out []registry.SymbolHit
	for _, s := range f.symbols {
		d := path.Dir(s.FilePath)
		if d == "." {
			d = ""
		}
		if d != dir || !want[s.Name] || !s.Exported || strings.HasSuffix(s.FilePath, "_test.go") ||
			s.Kind == "method" || s.FQN != s.FilePath+":"+s.Name {
			continue
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].FilePath < out[j].FilePath
	})
	return out, nil
}

func (f *fakeGo) GoPackageName(_ context.Context, _ int64, dir string) (string, error) {
	return f.pkgNames[dir], nil
}

func inGoDir(p, dir string) bool {
	d := path.Dir(p)
	if d == "." {
		d = ""
	}
	return d == dir && strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go")
}

func (f *fakeGo) GoPackageMethods(_ context.Context, _ int64, dir string, names []string) ([]registry.SymbolHit, error) {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	var out []registry.SymbolHit
	for _, s := range f.symbols {
		if s.Kind == "method" && want[s.Name] && s.Exported && inGoDir(s.FilePath, dir) {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		if out[i].FilePath != out[j].FilePath {
			return out[i].FilePath < out[j].FilePath
		}
		return out[i].StartLine < out[j].StartLine
	})
	return out, nil
}

func (f *fakeGo) GoPackageFiles(_ context.Context, _ int64, dir string) ([]registry.FileText, error) {
	var out []registry.FileText
	for p, c := range f.sources {
		if inGoDir(p, dir) {
			out = append(out, registry.FileText{Path: p, Content: c})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (f *fakeGo) FileContent(_ context.Context, _ int64, p string) (string, error) {
	c, ok := f.sources[p]
	if !ok {
		return "", fmt.Errorf("no file %s", p)
	}
	return c, nil
}

func (f *fakeGo) FileSymbols(_ context.Context, fileID int64) ([]registry.EnclosingSymbol, error) {
	return f.enclosing[f.fileOf(fileID)], nil
}

func (f *fakeGo) FileExcerpt(_ context.Context, _ int64, p string, line, _ int) (string, error) {
	return f.excerpts[p+":"+strconv.Itoa(line)], nil
}

func (f *fakeGo) FindSymbolsByName(_ context.Context, _ int64, name, _ string, _ int) ([]registry.SymbolHit, error) {
	var out []registry.SymbolHit
	for _, s := range f.symbols {
		if s.Name == name {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeGo) IncomingEdges(_ context.Context, _, id int64, limit int, filter registry.EdgeFilter) ([]registry.EdgeHit, error) {
	var out []registry.EdgeHit
	for _, e := range f.incoming[id] {
		if rank := filter.MinConfidence.Rank(); rank > 0 && model.Confidence(e.Confidence).Rank() < rank {
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}
