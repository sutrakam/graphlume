package infer

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// # Guided candidates: the cap bounds the unguided search only
//
// MaxCandidates protects against a name like `get` or `dispose` (2,722
// definitions in vscode) being weighed on the name alone: with nothing to go on
// there is no honest answer, and the cap says so. It was never meant to apply
// when the SOURCE says where the definition is — and it did. The candidate list
// for a name is the first rows the index holds, so a name with eleven
// definitions was abandoned before the rule that reads the file's own
// `import { IDisposable } from '../../base/common/lifecycle.js'` ever saw it:
// all 651 references to `IDisposable` in vscode were ambiguous, and the
// supertype reference `class X implements IDisposable` was among them.
//
// The answer is not to raise the cap. A reference that the ordinary rules refused
// BECAUSE its name is over the cap is given a second look at a different
// candidate set: the definitions of that name in the places the source points
// to. Those places are
//
//   - the file itself, which holds the enclosing type chain;
//   - the files in the same directory (the same package, where the language
//     counts that as evidence);
//   - the files each import resolves to;
//   - the files that define the type a receiver names, for the members of
//     that type.
//
// and the set is fetched FOR those files (name + file ids, idx_symbols_file_name)
// rather than cut from the global list. The existing rules then run unchanged
// over that set, so a guided binding is the binding the rules would have made
// had the name been rare — with one exception: a rule whose claim is that the
// name is unique in the REPOSITORY (the `unique name` and `only member` tiers)
// is refused here, because a guided set is small by construction and says
// nothing about the repository.
//
// Only references the first pass left ambiguous are retried. A reference that
// bound on the first pass, and every reference whose name is at or under the
// cap, takes exactly the path it always did.

// GuidedCandidates is the optional second port a Candidates source may offer.
// A source without it resolves exactly as before.
type GuidedCandidates interface {
	// Files lists the run's files.
	Files(ctx context.Context) ([]registry.FileRec, error)

	// InFiles returns the definitions of each name that live in the given files.
	InFiles(ctx context.Context, names []string, fileIDs []int64) (map[string][]registry.SymbolIndexEntry, error)

	// PerNameLimit is the most definitions Candidates returns for one name; a
	// list that long may have been cut. Zero means it never is.
	PerNameLimit() int
}

// Bounds on how much one source file may ask a guided lookup to read. They keep
// the work per file proportional to the file, not to the repository: a
// directory of 3,000 generated files, or an import that matches half the
// tree, is not evidence of where one name lives.
const (
	maxGuidedPerImport = 64
	maxGuidedSameDir   = 1024
	maxGuidedFiles     = 4096
	maxGuidedTypeFiles = 8
)

// guidedFiles is the run's file list indexed the ways an import or a directory
// is turned into files.
type guidedFiles struct {
	recs       []registry.FileRec
	byPath     map[string]int
	byStem     map[string][]int // lower-cased file stem
	byDir      map[string][]int // directory path
	dirsByLast map[string][]string
}

func newGuidedFiles(recs []registry.FileRec) *guidedFiles {
	g := &guidedFiles{
		recs:       recs,
		byPath:     make(map[string]int, len(recs)),
		byStem:     make(map[string][]int, len(recs)),
		byDir:      make(map[string][]int, len(recs)/4+1),
		dirsByLast: make(map[string][]string),
	}
	for i, f := range recs {
		g.byPath[f.Path] = i
		stem := strings.ToLower(fileBaseName(f.Path))
		g.byStem[stem] = append(g.byStem[stem], i)
		dir := path.Dir(f.Path)
		if _, seen := g.byDir[dir]; !seen {
			last := strings.ToLower(path.Base(dir))
			g.dirsByLast[last] = append(g.dirsByLast[last], dir)
		}
		g.byDir[dir] = append(g.byDir[dir], i)
	}
	return g
}

// guidedIndex returns the run's file index, built on first use.
func (r *Resolver) guidedIndex(ctx context.Context) (*guidedFiles, error) {
	if r.gfiles != nil {
		return r.gfiles, nil
	}
	recs, err := r.guide.Files(ctx)
	if err != nil {
		return nil, err
	}
	r.gfiles = newGuidedFiles(recs)
	return r.gfiles, nil
}

// fileGuide is what one source file's guided lookups know. It lives for one
// ResolveFile call.
type fileGuide struct {
	r       *Resolver
	spec    LanguageSpec
	fr      registry.FileRefs
	imports importSet

	// ids are the files the source points at, ascending and unique.
	ids  []int64
	have map[int64]struct{}

	// symbols is the file's candidate lists as the resolver holds them, and
	// cutDefs the definitions in ids of each name whose list was cut short.
	symbols map[string][]registry.SymbolIndexEntry
	cutDefs map[string][]registry.SymbolIndexEntry
}

// newFileGuide works out which files the source points at.
func (r *Resolver) newFileGuide(
	ctx context.Context,
	spec LanguageSpec,
	fr registry.FileRefs,
	imports importSet,
	symbols map[string][]registry.SymbolIndexEntry,
) (*fileGuide, error) {
	gf, err := r.guidedIndex(ctx)
	if err != nil {
		return nil, err
	}
	g := &fileGuide{r: r, spec: spec, fr: fr, imports: imports, have: map[int64]struct{}{}}
	add := func(i int) {
		id := gf.recs[i].ID
		if _, dup := g.have[id]; !dup {
			g.have[id] = struct{}{}
			g.ids = append(g.ids, id)
		}
	}
	g.have[fr.FileID] = struct{}{}
	g.ids = append(g.ids, fr.FileID)

	if dir := gf.byDir[path.Dir(fr.Path)]; len(dir) <= maxGuidedSameDir {
		for _, i := range dir {
			add(i)
		}
	}
	for _, e := range imports.imports {
		if len(g.ids) >= maxGuidedFiles {
			break
		}
		files := r.importFiles(gf, spec, fr, imports, e)
		if len(files) > maxGuidedPerImport {
			// An import that matches this many files names no one of them.
			continue
		}
		for _, i := range files {
			add(i)
		}
	}
	sort.Slice(g.ids, func(i, j int) bool { return g.ids[i] < g.ids[j] })

	g.symbols = symbols
	var cut []string
	for name, cs := range symbols {
		if g.truncated(cs) {
			cut = append(cut, name)
		}
	}
	sort.Strings(cut)
	// Names whose every definition is already in hand are filtered in memory
	// (inIDs). Only a name the candidate list cut off is asked of the index, for
	// the files the source points at.
	g.cutDefs, err = r.guide.InFiles(ctx, cut, g.ids)
	if err != nil {
		return nil, fmt.Errorf("guided candidates for %s: %w", fr.Path, err)
	}
	return g, nil
}

// truncated reports whether a candidate list may be missing definitions of its
// name because the source cut it at its per-name limit.
func (g *fileGuide) truncated(cs []registry.SymbolIndexEntry) bool {
	lim := g.r.guide.PerNameLimit()
	return lim > 0 && len(cs) >= lim
}

// definitionsIn is the definitions of name in the given files: filtered from the
// list in hand when that list is whole, asked of the index when it was cut.
func (g *fileGuide) definitionsIn(ctx context.Context, name string, ids []int64, base bool) ([]registry.SymbolIndexEntry, error) {
	cs := g.symbols[name]
	if !g.truncated(cs) {
		return filterToFiles(cs, ids, g.have, base), nil
	}
	if base {
		return g.cutDefs[name], nil
	}
	more, err := g.r.guide.InFiles(ctx, []string{name}, ids)
	if err != nil {
		return nil, err
	}
	return more[name], nil
}

// filterToFiles keeps the definitions that live in one of ids; with base set,
// the file set is the guide's own (have).
func filterToFiles(cs []registry.SymbolIndexEntry, ids []int64, have map[int64]struct{}, base bool) []registry.SymbolIndexEntry {
	var want map[int64]struct{}
	if base {
		want = have
	} else {
		want = make(map[int64]struct{}, len(ids))
		for _, id := range ids {
			want[id] = struct{}{}
		}
	}
	var out []registry.SymbolIndexEntry
	for _, c := range cs {
		if _, ok := want[c.FileID]; ok {
			out = append(out, c)
		}
	}
	return out
}

// importFiles are the indexed files one import can name.
func (r *Resolver) importFiles(gf *guidedFiles, spec LanguageSpec, fr registry.FileRefs, imports importSet, e importEntry) []int {
	if len(e.segments) == 0 {
		return nil
	}
	var out []int
	if spec.Calls.Imports == ImportsNameModules {
		m := r.scriptModule(fr.Path, e.raw)
		if m.contains == nil {
			return nil
		}
		if strings.HasPrefix(e.raw, ".") {
			// A relative specifier names one file exactly: look the spellings of
			// it up rather than searching.
			target := path.Clean(path.Join(path.Dir(fr.Path), trimScriptExtension(e.raw)))
			for _, ext := range scriptExtensions {
				for _, p := range []string{target + ext, target + "/index" + ext} {
					if i, ok := gf.byPath[p]; ok && m.contains(registry.SymbolIndexEntry{FilePath: p}) {
						out = append(out, i)
					}
				}
			}
			return out
		}
		stem := strings.ToLower(trimScriptExtension(path.Base(e.raw)))
		if stem == "index" {
			stem = strings.ToLower(path.Base(path.Dir(e.raw)))
			for _, d := range gf.dirsByLast[stem] {
				for _, i := range gf.byDir[d] {
					if m.contains(registry.SymbolIndexEntry{FilePath: gf.recs[i].Path}) {
						out = append(out, i)
					}
				}
			}
			return out
		}
		for _, i := range gf.byStem[stem] {
			if m.contains(registry.SymbolIndexEntry{FilePath: gf.recs[i].Path}) {
				out = append(out, i)
			}
		}
		return out
	}

	last := e.segments[len(e.segments)-1]
	seen := map[int]struct{}{}
	take := func(i int) {
		if _, dup := seen[i]; !dup {
			seen[i] = struct{}{}
			out = append(out, i)
		}
	}
	// The import names a directory (Go, Java packages, a Python package's init
	// module) or a file (a Python module, a Java class): the same test
	// pickImported applies, run over the files that could pass it.
	for _, d := range gf.dirsByLast[last] {
		for _, i := range gf.byDir[d] {
			p := gf.recs[i].Path
			if imports.coversFrom(e, p) && !imports.insidePackage(e, p) {
				take(i)
			}
		}
	}
	for _, i := range gf.byStem[last] {
		p := gf.recs[i].Path
		if (imports.coversFrom(e, p) && !imports.insidePackage(e, p)) || classFileNamed(e, p) {
			take(i)
		}
	}
	sort.Ints(out)
	return out
}

// classFileNamed reports whether a file is the one a class-style import names:
// the import's segments are the trailing directories and the file's stem.
func classFileNamed(e importEntry, filePath string) bool {
	segs := append(pathSegments(path.Dir(filePath)), strings.ToLower(fileBaseName(filePath)))
	return commonSuffixLen(e.segments, segs) == len(e.segments)
}

// receiverTypeName is the type name a reference's receiver may denote, or "".
func receiverTypeName(ref registry.Reference) string {
	r := strings.TrimPrefix(ref.Receiver, "*")
	if r == "" || r == opaqueReceiver {
		return ""
	}
	return lastSegment(r)
}

// overlay returns the candidate lists to substitute for one reference: the
// definitions of its name in the files the source points at, and of the type its
// receiver names. A name absent from the result has nothing to substitute.
func (g *fileGuide) overlay(
	ctx context.Context,
	ref registry.Reference,
	symbols map[string][]registry.SymbolIndexEntry,
) (map[string][]registry.SymbolIndexEntry, error) {
	out := map[string][]registry.SymbolIndexEntry{}
	name := ref.Name
	defs, err := g.definitionsIn(ctx, name, nil, true)
	if err != nil {
		return nil, err
	}

	// The receiver's type, if it is itself too common to have been weighed.
	if t := receiverTypeName(ref); t != "" && t != name {
		cs := symbols[t]
		if len(cs) > g.spec.MaxCandidates {
			tdefs, err := g.definitionsIn(ctx, t, nil, true)
			if err != nil {
				return nil, err
			}
			if len(tdefs) > 0 {
				out[t] = tdefs
			}
			cs = tdefs
		}
		// The members of that type are in the files that define it.
		var tfiles []int64
		for _, c := range cs {
			if isTypeKind(c.Kind) {
				if _, have := g.have[c.FileID]; !have {
					tfiles = append(tfiles, c.FileID)
				}
			}
		}
		if len(tfiles) > 0 && len(tfiles) <= maxGuidedTypeFiles {
			more, err := g.definitionsIn(ctx, name, tfiles, false)
			if err != nil {
				return nil, err
			}
			defs = mergeByID(defs, more)
		}
	}
	if len(defs) > 0 {
		out[name] = defs
	}
	return out, nil
}

// mergeByID is the union of two candidate lists, by id, in id order.
func mergeByID(a, b []registry.SymbolIndexEntry) []registry.SymbolIndexEntry {
	if len(b) == 0 {
		return a
	}
	seen := make(map[int64]struct{}, len(a)+len(b))
	out := make([]registry.SymbolIndexEntry, 0, len(a)+len(b))
	for _, list := range [][]registry.SymbolIndexEntry{a, b} {
		for _, c := range list {
			if _, dup := seen[c.ID]; !dup {
				seen[c.ID] = struct{}{}
				out = append(out, c)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// claimsRepositoryUniqueness reports whether a binding's reason asserts that
// the definition is the only one of its name in the repository. A guided
// candidate set cannot support that claim, so such a binding is refused.
func claimsRepositoryUniqueness(reason string) bool {
	for _, p := range []string{
		"unique name in repository",
		ruleOnlyMember, ruleOnlyType, ruleOnlyFree, ruleExtension,
	} {
		if strings.HasPrefix(reason, p) {
			return true
		}
	}
	return false
}

// pickNamedImport binds a reference to the definition an import NAMES, among
// candidates already restricted to files the source points at.
//
// pickImported matches an import path to a file by its trailing segments, which
// a specifier written with its extension (`../common/lifecycle.js`, every import
// in vscode) never does, and in a language that names classes in its imports it
// cannot see the class at all. The call rules have had import-by-name rules
// of their own (resolveMember, scriptModule, classImport); a type reference, an
// `extends` or an `implements` goes through pick and had none.
//
// Run only on the second look. On the first pass the same reference has the whole
// name's candidate list to weigh and the ordinary tiers decide, as they always did.
func (r *Resolver) pickNamedImport(
	fr registry.FileRefs,
	imports importSet,
	ref registry.Reference,
	cands []registry.SymbolIndexEntry,
) (candidate, bool) {
	var group []registry.SymbolIndexEntry
	switch imports.spec.Calls.Imports {
	case ImportsNameModules:
		for _, i := range imports.members[ref.Name] {
			m := r.scriptModule(fr.Path, imports.imports[i].raw)
			if m.contains == nil {
				continue
			}
			for _, c := range cands {
				if m.contains(c) && isModuleLevel(c) && !containsID(group, c.ID) {
					group = append(group, c)
				}
			}
		}
	case ImportsNameClasses:
		m, ok := r.classImport(imports, ref.Name)
		if !ok || m.contains == nil {
			return candidate{}, false
		}
		for _, c := range cands {
			if m.contains(c) {
				group = append(group, c)
			}
		}
	default:
		return candidate{}, false
	}
	first, ok := firstDeclaration(group)
	if !ok {
		return candidate{}, false
	}
	return candidate{first, model.ConfidenceHigh, ruleImportedByName}, true
}
