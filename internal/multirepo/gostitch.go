package multirepo

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// Go-module stitching: the second kind of edge this package draws between
// repositories, after the HTTP route match in graph.go.
//
// When one member imports a Go module another member defines, a call such as
// `lib.Do()` in the importer is a reference its own index could not bind — the
// definition is in another repository — and the provider's index holds that
// definition. Stitching joins the two: the importer's unbound qualified call,
// the import that names the provider's package, and the provider's exported
// package-level declaration of that name. That is the hop that lets callers
// and impact cross a repository boundary. A method call (`c.Send()`) needs one
// more step, the receiver's type, and gomethods.go documents how that is read.
//
// What it reads, all from the members' indexes and nothing from disk except
// where a replace directive's local path has to be compared with a checkout:
//
//   - go.mod text (files_fts), for the module paths each member defines, and
//     for each importer's require version and replace directives;
//   - file_imports, for which import path a receiver names;
//   - symbol_references, for the unbound calls and their receivers;
//   - symbols, for the provider's candidates and the importer's enclosing
//     caller;
//   - the Go source text (files_fts) of a provider package and of an
//     importing file, for the declared types method calls are followed by.
//
// THE CEILING. No compiler checked the importer against the provider's
// indexed source, so a stitched link is never `exact`: a single candidate is
// `high`, the strongest claim available from source text, and every leg is
// reported so the grade can be audited. The members may also have been
// indexed at versions that do not match what the importer requires; that is
// reported as a warning beside the links rather than as a refusal, because a
// link to the provider's current code is still where a reader starts.

// GoModuleSource is the port Go-module stitching reads one member through.
//
// Separate from [MemberSource] rather than folded into it, so the HTTP
// federation's fakes and the workspace pool need not grow eleven methods they
// never call. A member whose source does not satisfy it simply contributes no
// Go module facts. *registry.Registry satisfies both.
type GoModuleSource interface {
	GoModFiles(ctx context.Context, runID int64) ([]registry.FileText, error)
	GoImportedCalls(ctx context.Context, runID int64, modules []string, limit int) ([]registry.GoImportedCall, bool, error)
	GoPackageSymbols(ctx context.Context, runID int64, dir string, names []string) ([]registry.SymbolHit, error)
	GoPackageName(ctx context.Context, runID int64, dir string) (string, error)
	GoPackageMethods(ctx context.Context, runID int64, dir string, names []string) ([]registry.SymbolHit, error)
	GoPackageFiles(ctx context.Context, runID int64, dir string) ([]registry.FileText, error)
	FileContent(ctx context.Context, runID int64, path string) (string, error)
	FileSymbols(ctx context.Context, fileID int64) ([]registry.EnclosingSymbol, error)
	FileExcerpt(ctx context.Context, runID int64, path string, startLine, lines int) (string, error)
	FindSymbolsByName(ctx context.Context, runID int64, name, kind string, limit int) ([]registry.SymbolHit, error)
	IncomingEdges(ctx context.Context, runID, symbolID int64, limit int, filter registry.EdgeFilter) ([]registry.EdgeHit, error)
}

var _ GoModuleSource = (*registry.Registry)(nil)

// LegGoModule is the leg name every stitched row carries, in callers and
// impact output alike, so a reader can tell a hop the member's own graph
// recorded from one this package inferred across a repository boundary.
const LegGoModule = "go-module"

// Bounds for stitching. Each cut is counted in GoModuleReport.Truncated.
const (
	// DefaultMaxGoCallsRead bounds the unbound-call rows read from one
	// importer. A row is one call paired with one import of a provider module,
	// so this is a memory bound, not a semantic one.
	DefaultMaxGoCallsRead = 50_000
	// DefaultMaxGoLinks bounds the links LISTED in the system report. Counts
	// per pair are taken before this cut, and callers and impact read every
	// link regardless.
	DefaultMaxGoLinks = 500
	// maxGoSamples bounds the ambiguous and unmatched calls listed.
	maxGoSamples = 50
	// variantHeaderLines is how much of a candidate's definition is read to
	// compare build-variant signatures: enough for a header wrapped over a few
	// lines, never the body.
	variantHeaderLines = 8
)

// GoModuleDecl is one module a member defines.
type GoModuleDecl struct {
	Member string `json:"member"`
	Module string `json:"module"`
	// Dir is the module root inside that member's repository, "." for the
	// repository root.
	Dir string `json:"dir"`
}

// GoLink is one call in an importer stitched to one declaration in a provider.
type GoLink struct {
	FromMember string `json:"from_member"`
	// FromSymbol is the importer-side caller: the declaration whose span
	// encloses the call, as a person names it. "(top level of FILE)" when the
	// call sits outside every declaration.
	FromSymbol   string `json:"from_symbol"`
	FromSymbolID int64  `json:"-"`
	FromFile     string `json:"from_file"`
	FromLine     int    `json:"from_line"`
	// Call is the call as written: receiver and name. A call on a value whose
	// type the importer states is written with that type, `(lib.Client).Send`,
	// because the variable's own name says nothing about where it leads.
	Call string `json:"call"`
	// CallKind is "function" for a package-qualified call (`lib.Do()`, which
	// may also name a type, a variable or a constant) and "method" for a call
	// on a value of a provider type.
	CallKind   string `json:"call_kind"`
	ImportPath string `json:"import_path"`

	ToMember   string `json:"to_member"`
	Module     string `json:"module"`
	ToSymbol   string `json:"to_symbol"`
	ToSymbolID int64  `json:"-"`
	ToKind     string `json:"to_kind"`
	ToFile     string `json:"to_file"`
	ToLine     int    `json:"to_line"`

	// Confidence is the minimum over Legs, and never `exact`.
	Confidence model.Confidence `json:"confidence"`
	Legs       []Leg            `json:"legs"`
	Note       string           `json:"note,omitempty"`
}

// GoCallSample is a qualified call into a provider module that was not linked,
// with the reason.
type GoCallSample struct {
	Member   string `json:"member"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Call     string `json:"call"`
	Provider string `json:"provider"`
	Reason   string `json:"reason"`
	// Candidates are the provider declarations an ambiguous call could name,
	// as "file:line". Empty for an unmatched call.
	Candidates []string `json:"candidates,omitempty"`
}

// GoModulePair is what stitching found between one importer and one module
// another member defines.
type GoModulePair struct {
	Importer string `json:"importer"`
	Provider string `json:"provider"`
	Module   string `json:"module"`

	// Links counts package-qualified calls linked (a call into build variants
	// counts once).
	Links int `json:"links"`
	// Ambiguous counts package-qualified calls that matched several
	// declarations whose signatures differ, and were not linked.
	Ambiguous int `json:"ambiguous"`
	// Unmatched counts qualified calls naming no exported package-level
	// declaration in the provider's package.
	Unmatched int `json:"unmatched"`

	// MethodLinks counts method calls linked (a call into build variants
	// counts once): calls on a value whose type the importer states, a
	// provider variable whose declaration states one, or a local initialised
	// from a provider function whose declaration states its result.
	MethodLinks int `json:"method_links"`
	// MethodAmbiguous counts method calls whose type or method has several
	// declarations that differ, or that is promoted from two embedded types
	// at one depth; not linked.
	MethodAmbiguous int `json:"method_ambiguous"`
	// MethodNotResolvable counts method calls not linked, by reason (see
	// reasonLabels). The `untraced_value` reason is an upper bound counted
	// against every provider module the file imports: it holds every call on
	// a local or an expression the source does not trace to a provider call,
	// most of which are on values of other packages' types.
	MethodNotResolvable map[string]int `json:"method_not_resolvable"`

	// RequiredVersions are the versions the importer's go.mod files require
	// the module at, sorted; empty when none requires it.
	RequiredVersions []string `json:"required_versions"`
	// ProviderRevision is the commit the provider's index was built at.
	ProviderRevision string `json:"provider_revision,omitempty"`
	// Warnings say why the linked code may not be the code the importer
	// builds against. They never prevent a link.
	Warnings []string `json:"warnings"`
}

// GoModuleReport is the Go-module section of the system report.
type GoModuleReport struct {
	Modules   []GoModuleDecl `json:"modules"`
	Pairs     []GoModulePair `json:"pairs"`
	Links     []GoLink       `json:"links"`
	Ambiguous []GoCallSample `json:"ambiguous"`
	Unmatched []GoCallSample `json:"unmatched"`
	// MethodsNotResolved samples the method calls that reached a provider
	// and were not linked, with the reason. Calls on untraced values are
	// counted only: they are mostly calls on other packages' values, and
	// listing them would bury the rest.
	MethodsNotResolved []GoCallSample `json:"methods_not_resolved"`
	// Notes are estate-level conditions, such as a module path two members
	// both define.
	Notes     []string       `json:"notes"`
	Truncated map[string]int `json:"truncated,omitempty"`
	// Caveats are the standing limits of the method, printed every time.
	Caveats []string `json:"caveats"`
}

// goStitch is the full result, links uncapped, that callers and impact read
// and the report section is cut from.
type goStitch struct {
	modules   []GoModuleDecl
	pairs     map[pairKey]*GoModulePair
	links     []GoLink
	ambiguous []GoCallSample
	unmatched []GoCallSample
	// methodsUnresolved samples method calls not linked for a reason other
	// than an untraced value.
	methodsUnresolved []GoCallSample
	notes             []string
	truncated         map[string]int

	// byTarget indexes links by provider member and declaration id.
	byTarget map[symKey][]int
}

type pairKey struct{ importer, provider, module string }

type symKey struct {
	member string
	id     int64
}

// goMember is one available member as stitching sees it.
type goMember struct {
	name    string
	src     GoModuleSource
	runID   int64
	run     *model.IndexRun
	root    string // the checkout holding the index, "" when unknown
	modules []goModule

	// caches, per member
	pkgNames map[string]string
	files    map[int64][]registry.EnclosingSymbol
	// pkgSyntax is the parsed declarations of a package directory, when the
	// member is a provider; fileSyntax a parsed file, when it is an importer;
	// methodHits the method symbols by directory and name. Filled lazily.
	pkgSyntax  map[string]*goPkgSyntax
	fileSyntax map[string]*goFileSyntax
	methodHits map[string][]registry.SymbolHit

	// governing is, per provider module path, the importer's own modules
	// whose go.mod governs a file that imports it: the go.mod files whose
	// require and replace directives decide what that import builds against.
	governing map[string]map[string]goModule
}

// providerModule is one module path and the single member defining it.
type providerModule struct {
	member *goMember
	module goModule
}

// stitchGo runs Go-module stitching across the available members.
func (f *Federator) stitchGo(ctx context.Context, facts []*memberFacts) (*goStitch, error) {
	st := &goStitch{
		pairs:     map[pairKey]*GoModulePair{},
		truncated: map[string]int{},
		byTarget:  map[symKey][]int{},
	}

	var members []*goMember
	for _, mf := range facts {
		if !mf.member.Available() {
			continue
		}
		src, ok := mf.member.Source.(GoModuleSource)
		if !ok {
			continue
		}
		gm := &goMember{
			name: mf.member.Name, src: src, runID: mf.member.RunID, run: mf.run,
			root:     checkoutRoot(mf.member.IndexPath),
			pkgNames: map[string]string{},
			files:    map[int64][]registry.EnclosingSymbol{},
		}
		files, err := src.GoModFiles(ctx, gm.runID)
		if err != nil {
			return nil, fmt.Errorf("multirepo: member %q: read go.mod files: %w", gm.name, err)
		}
		texts := make(map[string]string, len(files))
		for _, ft := range files {
			texts[ft.Path] = ft.Content
		}
		gm.modules = modulesFromFiles(texts)
		for _, m := range gm.modules {
			st.modules = append(st.modules, GoModuleDecl{Member: gm.name, Module: m.Path, Dir: orDot(m.Dir)})
		}
		members = append(members, gm)
	}

	// Which member defines each module path. A path two members both define
	// (a fork, or two checkouts of one repository) has no single answer, and
	// stitching to either would be the invented dependency the route match
	// refuses for the same reason; it is named and skipped.
	definers := map[string][]providerModule{}
	for _, gm := range members {
		for _, m := range gm.modules {
			definers[m.Path] = append(definers[m.Path], providerModule{member: gm, module: m})
		}
	}
	providers := map[string]providerModule{}
	modPaths := make([]string, 0, len(definers))
	for p := range definers {
		modPaths = append(modPaths, p)
	}
	sort.Strings(modPaths)
	for _, p := range modPaths {
		ds := definers[p]
		owners := map[string]bool{}
		for _, d := range ds {
			owners[d.member.name] = true
		}
		if len(owners) > 1 {
			names := make([]string, 0, len(owners))
			for n := range owners {
				names = append(names, n)
			}
			sort.Strings(names)
			st.notes = append(st.notes, fmt.Sprintf(
				"module %s is defined by %s; calls into it are not linked, because there is no "+
					"single provider to link them to", p, joinAnd(names)))
			continue
		}
		providers[p] = ds[0]
	}

	for _, imp := range members {
		if err := f.stitchImporter(ctx, st, imp, providers); err != nil {
			return nil, err
		}
	}

	sort.Slice(st.links, func(i, j int) bool { return goLinkLess(st.links[i], st.links[j]) })
	for i, l := range st.links {
		k := symKey{l.ToMember, l.ToSymbolID}
		st.byTarget[k] = append(st.byTarget[k], i)
	}
	sortSamples(st.ambiguous)
	sortSamples(st.unmatched)
	sortSamples(st.methodsUnresolved)
	return st, nil
}

// pendingCall is one qualified call waiting for its provider lookup.
type pendingCall struct {
	call     registry.GoImportedCall
	provider providerModule
	dir      string
	binding  string // how the receiver was bound to the import, for the leg
}

// stitchImporter classifies every unbound call in one importer.
func (f *Federator) stitchImporter(ctx context.Context, st *goStitch, imp *goMember, providers map[string]providerModule) error {
	// The importer's own modules are answered by its own resolution, so only
	// modules some OTHER member defines are asked about.
	own := map[string]bool{}
	for _, m := range imp.modules {
		own[m.Path] = true
	}
	var foreign []string
	for p, pm := range providers {
		if !own[p] && pm.member != imp {
			foreign = append(foreign, p)
		}
	}
	if len(foreign) == 0 {
		return nil
	}
	sort.Strings(foreign)

	rows, truncated, err := imp.src.GoImportedCalls(ctx, imp.runID, foreign, f.maxGoCallsRead)
	if err != nil {
		return fmt.Errorf("multirepo: member %q: read calls into other members' Go modules: %w", imp.name, err)
	}
	if truncated {
		st.truncated["go_calls_read:"+imp.name] = f.maxGoCallsRead
		st.notes = append(st.notes, fmt.Sprintf(
			"%s holds more than %d unbound calls in files importing other members' modules; only the "+
				"first %d were read, so its counts and links are floors", imp.name, f.maxGoCallsRead, f.maxGoCallsRead))
	}

	imp.governing = map[string]map[string]goModule{}
	for _, r := range rows {
		pm, ok := providerFor(r.ImportPath, providers)
		if !ok {
			continue
		}
		gov, ok := governingModule(imp.modules, r.FilePath)
		if !ok {
			continue
		}
		if imp.governing[pm.module.Path] == nil {
			imp.governing[pm.module.Path] = map[string]goModule{}
		}
		imp.governing[pm.module.Path][gov.File] = gov
	}

	// Group the rows of one call: a call in a file importing two packages of
	// provider modules arrives once per import, and the receiver decides
	// which import (if any) it names.
	var groups [][]registry.GoImportedCall
	for _, r := range rows {
		if n := len(groups); n > 0 && groups[n-1][0].RefID == r.RefID {
			groups[n-1] = append(groups[n-1], r)
			continue
		}
		groups = append(groups, []registry.GoImportedCall{r})
	}

	var pending []pendingCall
	var methods []methodCall
	for _, g := range groups {
		if err := ctx.Err(); err != nil {
			return err
		}
		pc, kind, mc, err := classifyGoCall(ctx, g, providers)
		if err != nil {
			return err
		}
		switch kind {
		case callQualified:
			pending = append(pending, pc)
		case callMethod:
			methods = append(methods, mc)
		case callValue:
			mc, traced, err := traceValueCall(ctx, imp, g, providers)
			if err != nil {
				return err
			}
			if traced {
				methods = append(methods, mc)
				continue
			}
			// Nothing ties the value to one provider, so the call is a
			// possible method call into each the file imports: the count is
			// an upper bound, and says so.
			for _, pm := range providersImported(g, providers) {
				st.methodNotLinked(imp, pm, reasonUntraced, GoCallSample{})
			}
		}
	}

	if err := f.linkPending(ctx, st, imp, pending); err != nil {
		return err
	}
	return f.linkMethods(ctx, st, imp, methods)
}

type callKind int

const (
	callIgnored callKind = iota
	callQualified
	callMethod
	callValue
)

// classifyGoCall decides what one unbound call is, given the provider imports of its
// file.
func classifyGoCall(
	ctx context.Context,
	rows []registry.GoImportedCall,
	providers map[string]providerModule,
) (pendingCall, callKind, methodCall, error) {
	c := rows[0]
	switch c.ReceiverKind {
	case "", model.ReceiverName:
		for _, r := range rows {
			pm, ok := providerFor(r.ImportPath, providers)
			if !ok {
				continue
			}
			dir := providerDir(pm.module, r.ImportPath)
			bound, how, err := receiverNames(ctx, pm.member, r, dir)
			if err != nil {
				return pendingCall{}, callIgnored, methodCall{}, err
			}
			if bound {
				return pendingCall{call: r, provider: pm, dir: dir, binding: how}, callQualified, methodCall{}, nil
			}
		}
		// A one-word receiver naming no provider import is a package
		// variable, another package, or a value the parser could not type.
		// Not counted against any provider: nothing ties it to one.
		return pendingCall{}, callIgnored, methodCall{}, nil
	case model.ReceiverType, model.ReceiverPath:
		root, rest, dotted := strings.Cut(strings.TrimPrefix(c.Receiver, "*"), ".")
		if !dotted {
			return pendingCall{}, callIgnored, methodCall{}, nil
		}
		for _, r := range rows {
			pm, ok := providerFor(r.ImportPath, providers)
			if !ok {
				continue
			}
			dir := providerDir(pm.module, r.ImportPath)
			bound, how, err := receiverNames(ctx, pm.member, registry.GoImportedCall{
				Receiver: root, ImportPath: r.ImportPath, ImportAlias: r.ImportAlias,
			}, dir)
			if err != nil {
				return pendingCall{}, callIgnored, methodCall{}, err
			}
			if !bound {
				continue
			}
			mc := methodCall{call: r, provider: pm, dir: dir, binding: fmt.Sprintf(
				"%s in the receiver %s names the import of %s %s", root, c.Receiver, r.ImportPath, how)}
			if c.ReceiverKind == model.ReceiverType {
				// A generic instantiation's methods are its type's: Box[int]
				// is Box.
				mc.typeName, _, _ = strings.Cut(rest, "[")
				mc.typeLeg = Leg{
					Name: "type resolution", Confidence: model.ConfidenceHigh, Heuristic: true,
					Detail: fmt.Sprintf("the importer's source states the receiver's type, %s", c.Receiver),
				}
			} else {
				mc.varName = rest
			}
			return pendingCall{}, callMethod, mc, nil
		}
		return pendingCall{}, callIgnored, methodCall{}, nil
	case model.ReceiverValue, model.ReceiverExpr:
		return pendingCall{}, callValue, methodCall{}, nil
	default:
		return pendingCall{}, callIgnored, methodCall{}, nil
	}
}

// traceValueCall follows a call on a local value to the provider call the
// local was initialised from (traceValue), and from there to the type that
// call's function declares it returns. traced is false when the value does
// not lead to any provider; when it leads to one and then fails, the call is
// returned with its reason, to be counted against that provider.
func traceValueCall(
	ctx context.Context,
	imp *goMember,
	rows []registry.GoImportedCall,
	providers map[string]providerModule,
) (methodCall, bool, error) {
	c := rows[0]
	if c.ReceiverKind != model.ReceiverValue {
		// An expression receiver (`lib.NewClient().Send()`) is a chain,
		// which the in-repository resolver does not follow either.
		return methodCall{}, false, nil
	}
	fs, err := imp.importerFile(ctx, c.FilePath)
	if err != nil {
		return methodCall{}, false, err
	}
	t := traceValue(fs, c.Line, c.Col, c.Receiver)
	if t.qual == "" {
		return methodCall{}, false, nil
	}
	for _, r := range rows {
		pm, ok := providerFor(r.ImportPath, providers)
		if !ok {
			continue
		}
		dir := providerDir(pm.module, r.ImportPath)
		bound, how, err := receiverNames(ctx, pm.member, registry.GoImportedCall{
			Receiver: t.qual, ImportPath: r.ImportPath, ImportAlias: r.ImportAlias,
		}, dir)
		if err != nil {
			return methodCall{}, false, err
		}
		if !bound {
			continue
		}
		mc := methodCall{call: r, provider: pm, dir: dir, selfCalls: t.selfCalls, binding: fmt.Sprintf(
			"%s in %s's initialiser %s.%s() names the import of %s %s",
			t.qual, c.Receiver, t.qual, t.fn, r.ImportPath, how)}
		if t.reason != "" {
			mc.failReason, mc.failWhy = t.reason, t.why
			return mc, true, nil
		}
		pkg, err := pm.member.providerPackage(ctx, dir)
		if err != nil {
			return methodCall{}, false, err
		}
		typ, reason, why := returnedType(pkg, dir, t.fn)
		if reason != "" {
			mc.failReason, mc.failWhy = reason, why
			return mc, true, nil
		}
		mc.typeName = typ
		mc.typeLeg = Leg{
			Name: "type resolution", Confidence: model.ConfidenceMedium, Heuristic: true,
			Detail: fmt.Sprintf("%s is assigned once, at line %d, from %s.%s(), which declares that it returns %s",
				c.Receiver, t.at, t.qual, t.fn, printNode(pkg.fset, pkg.funcs[t.fn][0].typ.Results.List[0].Type)),
		}
		return mc, true, nil
	}
	return methodCall{}, false, nil
}

// providersImported is every distinct provider module the rows of one call import,
// in module order: a value call in a file importing two providers' modules is
// a possible method call into either.
func providersImported(rows []registry.GoImportedCall, providers map[string]providerModule) []providerModule {
	seen := map[string]bool{}
	var out []providerModule
	for _, r := range rows {
		pm, ok := providerFor(r.ImportPath, providers)
		if !ok || seen[pm.module.Path] {
			continue
		}
		seen[pm.module.Path] = true
		out = append(out, pm)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].module.Path < out[j].module.Path })
	return out
}

// providerFor returns the module, among those exactly one member defines,
// that import path p belongs to: the LONGEST module path p is in, which is the
// go command's rule and the one that sends example.com/lib/sub to a nested
// module of that name rather than to a directory of example.com/lib.
func providerFor(p string, providers map[string]providerModule) (providerModule, bool) {
	best, found := providerModule{}, false
	for m, pm := range providers {
		if importInModule(p, m) && (!found || len(m) > len(best.module.Path)) {
			best, found = pm, true
		}
	}
	return best, found
}

// providerDir is the directory, in the provider's repository, of the package
// at import path p inside module m.
func providerDir(m goModule, p string) string {
	rest := strings.TrimPrefix(strings.TrimPrefix(p, m.Path), "/")
	d := path.Clean(path.Join(m.Dir, rest))
	if d == "." {
		return ""
	}
	return d
}

// receiverNames reports whether call c's receiver is the name its import of
// c.ImportPath binds in c's file, and how.
//
// An alias binds exactly itself; `_` and `.` bind no qualifier. With no
// alias the import binds the package's own name, which is read from the
// provider's package clause, and is assumed from the path (last element, a
// /vN element skipped, a .vN suffix dropped) when the clause cannot be read.
// Both are accepted: the clause is the truth when the index has it, and the
// path is what the go tool assumes when it does not.
func receiverNames(ctx context.Context, provider *goMember, c registry.GoImportedCall, dir string) (bool, string, error) {
	if c.Receiver == "" {
		return false, "", nil
	}
	if c.ImportAlias != "" {
		if c.ImportAlias == "_" || c.ImportAlias == "." {
			return false, "", nil
		}
		return c.ImportAlias == c.Receiver, "by its alias " + c.ImportAlias, nil
	}
	if c.Receiver == defaultPackageName(c.ImportPath) {
		return true, "by the package name the import path implies", nil
	}
	name, err := provider.packageName(ctx, dir)
	if err != nil {
		return false, "", err
	}
	if name != "" && name == c.Receiver {
		return true, "by the package clause in " + provider.name, nil
	}
	return false, "", nil
}

func (gm *goMember) packageName(ctx context.Context, dir string) (string, error) {
	if n, ok := gm.pkgNames[dir]; ok {
		return n, nil
	}
	n, err := gm.src.GoPackageName(ctx, gm.runID, dir)
	if err != nil {
		return "", fmt.Errorf("multirepo: member %q: read package clause in %q: %w", gm.name, orDot(dir), err)
	}
	gm.pkgNames[dir] = n
	return n, nil
}

// linkPending looks every qualified call up in its provider and links it.
func (f *Federator) linkPending(ctx context.Context, st *goStitch, imp *goMember, pending []pendingCall) error {
	// One lookup per provider package: the names asked of it, together.
	type pkgKey struct {
		member string
		dir    string
	}
	names := map[pkgKey]map[string]bool{}
	members := map[pkgKey]*goMember{}
	for _, pc := range pending {
		k := pkgKey{pc.provider.member.name, pc.dir}
		if names[k] == nil {
			names[k] = map[string]bool{}
			members[k] = pc.provider.member
		}
		names[k][pc.call.Name] = true
	}
	cands := map[pkgKey]map[string][]registry.SymbolHit{}
	keys := make([]pkgKey, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].member != keys[j].member {
			return keys[i].member < keys[j].member
		}
		return keys[i].dir < keys[j].dir
	})
	for _, k := range keys {
		list := make([]string, 0, len(names[k]))
		for n := range names[k] {
			if isExportedGo(n) {
				list = append(list, n)
			}
		}
		sort.Strings(list)
		pm := members[k]
		hits, err := pm.src.GoPackageSymbols(ctx, pm.runID, k.dir, list)
		if err != nil {
			return fmt.Errorf("multirepo: member %q: read declarations in %q: %w", pm.name, orDot(k.dir), err)
		}
		byName := map[string][]registry.SymbolHit{}
		for _, h := range hits {
			byName[h.Name] = append(byName[h.Name], h)
		}
		cands[k] = byName
	}

	// Variant decisions are per provider declaration name, not per call: two
	// calls to one GOOS-split function get one answer.
	type variantKey struct {
		pkgKey
		name string
	}
	sameSig := map[variantKey]bool{}

	for _, pc := range pending {
		k := pkgKey{pc.provider.member.name, pc.dir}
		found := cands[k][pc.call.Name]
		pair := st.pair(imp, pc.provider)
		call := pc.call.Receiver + "." + pc.call.Name
		sample := GoCallSample{
			Member: imp.name, File: pc.call.FilePath, Line: pc.call.Line, Call: call,
			Provider: pc.provider.member.name,
		}

		switch {
		case len(found) == 0:
			pair.Unmatched++
			sample.Reason = fmt.Sprintf("no exported package-level declaration named %s in %s of %s",
				pc.call.Name, orDot(pc.dir), pc.provider.member.name)
			st.unmatched = appendSample(st.unmatched, sample, st.truncated, "go_unmatched")
			continue
		case len(found) > 1:
			vk := variantKey{k, pc.call.Name}
			same, seen := sameSig[vk]
			if !seen {
				var err error
				same, err = sameSignatures(ctx, pc.provider.member, found)
				if err != nil {
					return err
				}
				sameSig[vk] = same
			}
			if !same {
				pair.Ambiguous++
				sample.Reason = fmt.Sprintf("%d declarations named %s in %s of %s differ in signature, "+
					"so which one this build calls cannot be told from source text",
					len(found), pc.call.Name, orDot(pc.dir), pc.provider.member.name)
				for _, h := range found {
					sample.Candidates = append(sample.Candidates, fmt.Sprintf("%s:%d", h.FilePath, h.StartLine))
				}
				st.ambiguous = appendSample(st.ambiguous, sample, st.truncated, "go_ambiguous")
				continue
			}
		}

		caller, callerID, err := imp.enclosing(ctx, pc.call)
		if err != nil {
			return err
		}
		pair.Links++
		for _, h := range found {
			legs := []Leg{
				{
					Name: "import binding", Confidence: model.ConfidenceHigh, Heuristic: true,
					Detail: fmt.Sprintf("receiver %s names the import of %s %s",
						pc.call.Receiver, pc.call.ImportPath, pc.binding),
				},
				{
					Name: "module ownership", Confidence: model.ConfidenceHigh, Heuristic: true,
					Detail: fmt.Sprintf("%s is in module %s, which only %s defines (%s)",
						pc.call.ImportPath, pc.provider.module.Path, pc.provider.member.name, pc.provider.module.File),
				},
			}
			lookup := Leg{
				Name: "declaration lookup", Confidence: model.ConfidenceHigh, Heuristic: true,
				Detail: fmt.Sprintf("the one exported package-level %s %s in %s", h.Kind, h.Name, orDot(pc.dir)),
			}
			note := ""
			if len(found) > 1 {
				lookup.Confidence = model.ConfidenceMedium
				lookup.Detail = fmt.Sprintf("one of %d same-signature declarations of %s in %s "+
					"(build-constrained variants); linked to each", len(found), h.Name, orDot(pc.dir))
				note = "which variant a build compiles depends on its GOOS, GOARCH or build tags"
			}
			legs = append(legs, lookup)
			confs := make([]model.Confidence, 0, len(legs))
			for _, l := range legs {
				confs = append(confs, l.Confidence)
			}
			st.links = append(st.links, GoLink{
				FromMember: imp.name, FromSymbol: caller, FromSymbolID: callerID,
				FromFile: pc.call.FilePath, FromLine: pc.call.Line,
				Call: call, CallKind: callKindFunction, ImportPath: pc.call.ImportPath,
				ToMember: pc.provider.member.name, Module: pc.provider.module.Path,
				ToSymbol: h.Name, ToSymbolID: h.ID, ToKind: h.Kind,
				ToFile: h.FilePath, ToLine: h.StartLine,
				Confidence: minConfidence(confs...), Legs: legs, Note: note,
			})
		}
	}
	return nil
}

// sameSignatures reports whether every candidate's declaration header reads
// the same once whitespace is normalised.
//
// The rule for several declarations of one name in one package directory —
// which Go only allows across build-constrained files (do_linux.go,
// do_darwin.go): link to each at `medium` when their signatures agree, and
// refuse when they do not. Agreeing variants are one API compiled per
// platform, so a caller does reach one of them, and every one is a true
// answer to "who calls this"; refusing them would drop real callers of
// platform-split code from every cross-repository blast radius. Variants that
// disagree are not one API, and which one a caller compiles against cannot be
// told without the build configuration, so they are counted as ambiguous.
func sameSignatures(ctx context.Context, provider *goMember, hits []registry.SymbolHit) (bool, error) {
	var first string
	for i, h := range hits {
		text, err := provider.src.FileExcerpt(ctx, provider.runID, h.FilePath, h.StartLine, variantHeaderLines)
		if err != nil {
			return false, fmt.Errorf("multirepo: member %q: read %s:%d: %w", provider.name, h.FilePath, h.StartLine, err)
		}
		hdr := declHeader(text)
		if hdr == "" {
			return false, nil
		}
		if i == 0 {
			first = hdr
			continue
		}
		if hdr != first {
			return false, nil
		}
	}
	return true, nil
}

// declHeader is a declaration's text up to the opening brace of its body,
// whitespace collapsed: `func Platform() string`. "" when no brace is found in
// the excerpt, so an unreadable header never counts as a match.
func declHeader(text string) string {
	i := strings.Index(text, "{")
	if i < 0 {
		// A declaration with no body on these lines (a const, a var, a type
		// alias) is compared whole, line by line.
		line, _, _ := strings.Cut(text, "\n")
		return strings.Join(strings.Fields(line), " ")
	}
	return strings.Join(strings.Fields(text[:i]), " ")
}

// enclosing names the importer-side caller of a call: the innermost
// declaration whose span covers the call's line, by the rule the resolver
// uses (internal/resolve/infer.enclosing) so a stitched caller is the same
// symbol the importer's own graph would have recorded as the call's source.
// The synthetic caller of the file stands in when no declaration covers it.
func (gm *goMember) enclosing(ctx context.Context, c registry.GoImportedCall) (string, int64, error) {
	syms, ok := gm.files[c.FileID]
	if !ok {
		var err error
		syms, err = gm.src.FileSymbols(ctx, c.FileID)
		if err != nil {
			return "", 0, fmt.Errorf("multirepo: member %q: read symbols of %s: %w", gm.name, c.FilePath, err)
		}
		gm.files[c.FileID] = syms
	}
	var best, fileCaller *registry.EnclosingSymbol
	for i := range syms {
		s := &syms[i]
		if s.Kind == registry.FileCallerKind {
			fileCaller = s
			continue
		}
		if s.Kind == string(model.KindDependency) {
			continue
		}
		if s.StartLine > c.Line {
			break
		}
		end := s.EndLine
		if end == 0 {
			end = s.StartLine
		}
		if c.Line <= end {
			best = s
		}
	}
	switch {
	case best != nil:
		return qualifiedIn(best.Name, best.FQN, c.FilePath), best.ID, nil
	case fileCaller != nil:
		return fileCaller.Name, fileCaller.ID, nil
	default:
		return registry.FileCallerName(c.FilePath), 0, nil
	}
}

// qualifiedIn is a symbol as a person names it: Owner.member for a method, the
// bare name for a top-level declaration. The same reading the registry gives
// EdgeHit.SrcQualified.
func qualifiedIn(name, fqn, file string) string {
	rest, ok := strings.CutPrefix(fqn, file+":")
	if !ok || rest == "" {
		return name
	}
	return rest
}

// pair returns the counters for one importer and one provider module,
// creating them with their version warnings on first use.
func (st *goStitch) pair(imp *goMember, pm providerModule) *GoModulePair {
	k := pairKey{imp.name, pm.member.name, pm.module.Path}
	if p, ok := st.pairs[k]; ok {
		return p
	}
	p := &GoModulePair{
		Importer: imp.name, Provider: pm.member.name, Module: pm.module.Path,
		RequiredVersions: []string{}, Warnings: []string{}, MethodNotResolvable: map[string]int{},
	}
	if pm.member.run != nil {
		p.ProviderRevision = pm.member.run.GitRevision
	}
	p.RequiredVersions, p.Warnings = versionWarnings(imp, pm)
	st.pairs[k] = p
	return p
}

// versionWarnings compares what the importer's go.mod files say about the
// provider module with the provider's index.
//
// Every outcome is a warning or nothing; none refuses a link. The go.mod of
// a provider states no version of itself — a version is a tag on a commit —
// so a required release can only be checked against the provider's indexed
// commit when the requirement is a pseudo-version, which names its commit.
func versionWarnings(imp *goMember, pm providerModule) ([]string, []string) {
	mod := pm.module.Path
	rev := ""
	if pm.member.run != nil {
		rev = pm.member.run.GitRevision
	}
	at := "records no commit"
	if rev != "" {
		at = "is at commit " + shortRev(rev)
	}

	// Only the go.mod files governing a file that imports the module speak
	// for it, in file order; a tools module that never imports it says
	// nothing about what the importing code builds against.
	gov := imp.governing[mod]
	files := make([]string, 0, len(gov))
	for f := range gov {
		files = append(files, f)
	}
	sort.Strings(files)

	versions := map[string]bool{}
	warnings := map[string]bool{}
	requiring := 0
	for _, f := range files {
		m := gov[f]
		v, required := m.Requires[mod]
		r, replaced := m.replaceFor(mod, v)
		if required {
			requiring++
			versions[v] = true
		}
		switch {
		case replaced && isLocalReplace(r.New):
			target, want := "", ""
			if imp.root != "" && pm.member.root != "" {
				target = filepath.Clean(filepath.Join(imp.root, filepath.FromSlash(m.Dir), filepath.FromSlash(r.New)))
				want = filepath.Clean(filepath.Join(pm.member.root, filepath.FromSlash(pm.module.Dir)))
			}
			switch {
			case target == "":
				warnings[fmt.Sprintf(
					"%s's %s replaces %s with the local path %s; whether that is %s's checkout cannot "+
						"be checked, because an index outside a checkout does not say where the checkout is",
					imp.name, m.File, mod, r.New, pm.member.name)] = true
			case target != want:
				warnings[fmt.Sprintf(
					"%s's %s replaces %s with %s, which is not %s's checkout (%s): replace directive points "+
						"elsewhere, so the code %s builds against may not be the code indexed for %s",
					imp.name, m.File, mod, r.New, pm.member.name, want, imp.name, pm.member.name)] = true
			}
			// A replace that points at this provider's checkout is the one
			// case where the indexed code IS the code built against, and the
			// required version is ignored by the go command; nothing to say.
		case replaced:
			to := r.New
			if r.NewVersion != "" {
				to += " " + r.NewVersion
			}
			warnings[fmt.Sprintf(
				"%s's %s replaces %s with %s: replace directive points elsewhere, so %s does not build "+
					"against %s's code at all and these links rest on names alone",
				imp.name, m.File, mod, to, imp.name, pm.member.name)] = true
		case required:
			if pr := pseudoVersionRev(v); pr != "" && rev != "" &&
				(strings.HasPrefix(rev, pr) || strings.HasPrefix(pr, rev)) {
				continue
			}
			warnings[fmt.Sprintf(
				"%s requires %s %s; %s's index %s (go.mod has no version), so whether the indexed "+
					"code is that version cannot be checked",
				imp.name, mod, v, pm.member.name, at)] = true
		}
	}
	if requiring == 0 {
		warnings[fmt.Sprintf(
			"%s imports %s but no go.mod governing the importing files requires it, so the version it "+
				"builds against is unknown (a go.work file or a vendored copy may supply it)", imp.name, mod)] = true
	}
	return sortedKeys(versions), sortedKeys(warnings)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// checkoutRoot is the repository directory an index file belongs to, when the
// index is where 'graphlume index' writes it (<root>/.graphlume/index.db),
// and "" otherwise.
func checkoutRoot(indexPath string) string {
	if indexPath == "" {
		return ""
	}
	dir := filepath.Dir(filepath.Clean(indexPath))
	if b := filepath.Base(dir); b != ".graphlume" {
		return ""
	}
	return filepath.Dir(dir)
}

// isExportedGo reports whether a Go identifier is exported. An unexported
// name can never be reached through a package qualifier, so it is not looked
// up at all.
func isExportedGo(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(r)
}

func orDot(dir string) string {
	if dir == "" {
		return "."
	}
	return dir
}

func appendSample(list []GoCallSample, s GoCallSample, truncated map[string]int, key string) []GoCallSample {
	if len(list) >= maxGoSamples {
		truncated[key]++
		return list
	}
	return append(list, s)
}

func sortSamples(s []GoCallSample) {
	sort.Slice(s, func(i, j int) bool {
		a, b := s[i], s[j]
		switch {
		case a.Member != b.Member:
			return a.Member < b.Member
		case a.File != b.File:
			return a.File < b.File
		case a.Line != b.Line:
			return a.Line < b.Line
		default:
			return a.Call < b.Call
		}
	})
}

func goLinkLess(a, b GoLink) bool {
	switch {
	case a.FromMember != b.FromMember:
		return a.FromMember < b.FromMember
	case a.FromFile != b.FromFile:
		return a.FromFile < b.FromFile
	case a.FromLine != b.FromLine:
		return a.FromLine < b.FromLine
	case a.Call != b.Call:
		return a.Call < b.Call
	case a.ToMember != b.ToMember:
		return a.ToMember < b.ToMember
	case a.ToFile != b.ToFile:
		return a.ToFile < b.ToFile
	default:
		return a.ToLine < b.ToLine
	}
}

// report cuts the uncapped stitch into the system report's section. Nil when
// no member defines a Go module, so a system with no Go in it prints nothing
// about Go.
func (st *goStitch) report(maxLinks int) *GoModuleReport {
	if len(st.modules) == 0 {
		return nil
	}
	r := &GoModuleReport{
		Modules:            append([]GoModuleDecl(nil), st.modules...),
		Pairs:              []GoModulePair{},
		Links:              []GoLink{},
		Ambiguous:          append([]GoCallSample{}, st.ambiguous...),
		Unmatched:          append([]GoCallSample{}, st.unmatched...),
		MethodsNotResolved: append([]GoCallSample{}, st.methodsUnresolved...),
		Notes:              append([]string{}, st.notes...),
		Truncated:          map[string]int{},
		Caveats:            goCaveats(),
	}
	sort.Slice(r.Modules, func(i, j int) bool {
		if r.Modules[i].Member != r.Modules[j].Member {
			return r.Modules[i].Member < r.Modules[j].Member
		}
		return r.Modules[i].Module < r.Modules[j].Module
	})
	for _, p := range st.pairs {
		r.Pairs = append(r.Pairs, *p)
	}
	sort.Slice(r.Pairs, func(i, j int) bool {
		a, b := r.Pairs[i], r.Pairs[j]
		switch {
		case a.Importer != b.Importer:
			return a.Importer < b.Importer
		case a.Provider != b.Provider:
			return a.Provider < b.Provider
		default:
			return a.Module < b.Module
		}
	})
	for k, v := range st.truncated {
		r.Truncated[k] = v
	}
	for i, l := range st.links {
		if i >= maxLinks {
			r.Truncated["go_links"] = len(st.links) - maxLinks
			break
		}
		r.Links = append(r.Links, l)
	}
	if len(r.Truncated) == 0 {
		r.Truncated = nil
	}
	return r
}

// goCaveats are the limits of Go-module stitching, which do not vary with the
// data and are the reading rule for every count beside them.
func goCaveats() []string {
	return []string{
		"Go module links are never `exact`: no compiler checked the importer against the " +
			"provider's indexed source. One candidate is `high`; build-constrained variants with " +
			"one signature are `medium`, linked to each.",
		"Method calls are linked when the receiver's type is known from source: a type the " +
			"importer states (`c *lib.Client`, `high`), a provider variable whose declaration " +
			"states one (`lib.Default.Send()`, `medium`), or a local assigned once from a provider " +
			"function whose declaration states its result (`c := lib.NewClient()`, `medium`). " +
			"An interface's method and a method promoted through embedding are `medium`. " +
			"Chains, fields, reassigned locals and untyped declarations are counted, not linked.",
		"Only modules defined by members of this estate are linked; a module from anywhere " +
			"else is outside the system graph. Go is the only language stitched this way.",
	}
}
