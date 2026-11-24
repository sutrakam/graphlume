package scipimport

import (
	"context"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/resolve/scip"
	"github.com/sutrakam/graphlume/pkg/model"
)

// Proof collects, across every compiler index imported for a run, what the
// answer layer needs to stop counting an unbound reference as a possible use:
// which files the compilers read, and which positions they resolved to
// something this repository does not define.
//
// It is filled by an Importer that was given one (Importer.Collect) and written
// once, after the last import, by Apply. It is not filled for an index the user
// supplied: an index built from some other commit would claim coverage of
// files it never read.
//
// WHAT COUNTS AS RULED OUT. A reference row is spelled like the definition an
// answer is about, was bound to nothing (or only by the resolver's guess from
// the name, which is then undone: see registry.MarkRuledOut), and sits where a
// compiler resolved an occurrence spelled the same way to a symbol of another
// package, one this repository does not define. A reference a compiler itself
// bound is never touched. The test is made per (file, line, name):
//
//   - every occurrence of that name on the line must resolve outside the
//     repository, or to repository code the index holds no symbol for (see
//     below), and none to a symbol joined to an indexed definition, nor to a
//     package that owns a definition in any imported index, since a sibling
//     project's symbol arrives under its package name and has no counterpart
//     here;
//   - there must be at least as many such occurrences as the parser recorded
//     references of that name on the line, so a second reference the compiler
//     did not resolve cannot hide behind the first.
//
// A position the compiler did not resolve at all (a receiver of unknown type, a
// dynamic attribute) is never ruled out: no occurrence, no proof. That is what
// keeps the rule sound for Python and for TypeScript's `any`, where the Go
// rule "the checker read the file and bound nothing" would be false.
//
// TWO MORE ANSWERS SETTLE A REFERENCE, both about code in the repository that
// the index holds no definition for, which is not a use of any definition it
// does hold:
//
//   - A symbol the compiler's own documents define and that has no counterpart
//     among the indexed symbols (a class or function written inside another
//     function, a method of such a class). It counts like an outside symbol,
//     except that one that can hold a value (a variable, a constant, a field:
//     `const request = context.request`) settles only the references the
//     resolver left unbound, and withdraws nothing, like the local below,
//     unless the file that defines it holds an indexed symbol of the same
//     name that no compiler definition is joined to: then the two sides may
//     simply have failed to join, and that symbol is the definition asked about.
//   - A bare call (no receiver) the resolver left unbound, whose callee the
//     compiler resolved to a local variable, parameter or inner function the
//     same document declares. The call is of that binding. It reaches a
//     definition of the name only through a use of the name as a value
//     (`const f = obj.method; f()`, `const { jsx } = await import(...)`), which
//     is a different fact, reported as one by the answer ("it may be passed as
//     a value") and enough on its own to keep an answer incomplete. The same
//     guard on the name applies. A reference the resolver DID bind is left
//     bound and its edge is kept: the alias may be exactly what it found.
//
// WHAT STAYS UNKNOWN IS MARKED WHEN THE REASON IS KNOWN. A position resolved to
// a package the repository provides that no document defines, or to the
// project's own package inside a document that also names such a package, is the
// indexer reading an INSTALLED COPY of the repository's own library (possibly
// another version than the checkout). The unbound reference there stays
// counted and is marked (registry.ResolutionSourceInstalled) so the answer can
// say why.
//
// INHERITANCE AND OVERRIDES need nothing special, because the compiler already
// resolved them: a call on a subclass instance that does not override the
// method is resolved to the BASE method's symbol, which is the definition and
// is bound to it; a call that reaches an override is resolved to the
// override's symbol, a different definition (bound to it, or unmatched and
// settled by the first rule), and is not a use of the base method. A call the
// compiler gave no occurrence for is unknown whichever way the receiver was
// typed, and stays counted.
type Proof struct {
	covered map[string]string
	sites   map[siteAt]*siteTally
	own     map[string]struct{}
	// declared are the local symbols a document declares and the index holds no
	// symbol for, by the position of each occurrence.
	declared map[siteCol]int
	// matchedIDs are the indexed symbols some compiler definition was joined to.
	matchedIDs map[int64]struct{}
	// installed are the repository's own packages an indexer resolved to an
	// installed copy, normalised (see normalizePackage).
	installed map[string]struct{}
	// installedDocs are the documents that name a symbol of an installed copy.
	installedDocs map[string]struct{}
}

// siteCol is one occurrence's position.
type siteCol struct {
	path      string
	line, col int
}

// siteTarget is what the import knows of the symbol an occurrence resolved to.
type siteTarget struct {
	// matched: the index defines it and a repository symbol was found for it.
	matched bool
	// defined: some imported document defines it.
	defined bool
	// defPath is the document that defines it, and kind what it is.
	defPath string
	kind    model.EntityKind
}

// holdsAValue reports whether a symbol of this kind can hold another
// definition's value: `const request = context.request`, a property assigned a
// method. A call through one of those is a call of the binding that may reach
// the definition it aliases, so it settles less than a call of a function,
// method or class declaration, which is a call of that declaration and no other.
func holdsAValue(k model.EntityKind) bool {
	switch k {
	case model.KindVariable, model.KindConstant, model.KindField:
		return true
	}
	return false
}

type siteAt struct {
	path string
	line int
	name string
}

type siteTally struct {
	outside int
	inRepo  int
	// other counts occurrences of repository declarations the index holds no
	// symbol for (a function, method or class), alias those that are a variable,
	// constant or field, and otherIn the documents that define them.
	other int
	alias int
	// installed counts occurrences of a symbol of an installed copy of the
	// repository's own package, and ownUndefined those of a symbol of a package
	// the repository provides that no document defines: an indexer that read an
	// installed copy spells that copy's modules under the project's own package
	// (pallets/click: `click.types`/ParamType#to_info_dict().), so they count
	// only in a document that also names the installed copy.
	installed    int
	ownUndefined int
	otherIn      map[string]struct{}
	pkgs         map[string]struct{}
}

// NewProof returns an empty Proof.
func NewProof() *Proof {
	return &Proof{
		covered: map[string]string{},
		sites:   map[siteAt]*siteTally{},
		own:     map[string]struct{}{},

		declared:   map[siteCol]int{},
		matchedIDs: map[int64]struct{}{},
	}
}

// cover records that tool read path and which packages it defines.
func (p *Proof) cover(path, tool string, packages []string) {
	p.covered[path] = tool
	for _, pkg := range packages {
		p.own[normalizePackage(pkg)] = struct{}{}
	}
}

// AddOwn declares a package the repository itself provides, as "manager name"
// the way a SCIP symbol spells it. The indexes name the repository's own
// definitions after the project, but an indexer that finds an INSTALLED copy of
// the same library (pallets/click's own tests run against the click in site-
// packages) resolves imports to that copy, under the library's real name: a
// reference resolved there is a use of repository code resolved to a duplicate
// outside it, and must not be read as "something else".
func (p *Proof) AddOwn(manager, name string) {
	if manager != "" && name != "" {
		p.own[normalizePackage(manager+" "+name)] = struct{}{}
	}
}

// setInstalled records which of the repository's own packages the indexer
// resolved to an installed copy (ImportResult.InstalledCopies).
func (p *Proof) setInstalled(pkgs []string) {
	for _, pk := range pkgs {
		if p.installed == nil {
			p.installed = map[string]struct{}{}
		}
		p.installed[pk] = struct{}{}
	}
}

// seenAgainstInstalledCopy reports whether the occurrences at a position say the
// indexer read an installed copy of the repository's own package there.
func (p *Proof) seenAgainstInstalledCopy(path string, t *siteTally) bool {
	if t.installed > 0 {
		return true
	}
	if t.ownUndefined == 0 {
		return false
	}
	_, doc := p.installedDocs[path]
	return doc
}

// matchedSymbol records that a compiler definition was joined to the indexed
// symbol id, so the symbol is accounted for.
func (p *Proof) matchedSymbol(id int64) { p.matchedIDs[id] = struct{}{} }

// provided reports whether the repository itself provides the package, given as
// "manager name" in the form normalizePackage gives.
func (p *Proof) provided(pkg string) bool {
	_, ok := p.own[pkg]
	return ok
}

// normalizePackage makes the spellings of one package compare equal: case, and
// the hyphen or underscore a distribution name and an import name differ by.
func normalizePackage(pkg string) string {
	return strings.ReplaceAll(strings.ToLower(pkg), "-", "_")
}

// site records one occurrence and what it resolved to.
func (p *Proof) site(path string, s scip.Site, t siteTarget) {
	if s.Local {
		// A local symbol is a variable or a parameter: nothing about it says
		// what name it is spelled as, so it can neither rule a reference out by
		// name nor block a ruling on the same line. One this document declares
		// is recorded by position, for a bare call written at exactly it.
		if s.Declared && !t.matched && s.Len > 0 {
			p.declared[siteCol{path, s.Line, s.Col}] = s.Len
		}
		return
	}
	if s.Name == "" {
		return
	}
	k := siteAt{path: path, line: s.Line, name: s.Name}
	tl := p.sites[k]
	if tl == nil {
		tl = &siteTally{}
		p.sites[k] = tl
	}
	switch {
	case t.matched:
		tl.inRepo++
	case t.defined:
		if holdsAValue(t.kind) {
			tl.alias++
		} else {
			tl.other++
		}
		if tl.otherIn == nil {
			tl.otherIn = map[string]struct{}{}
		}
		tl.otherIn[t.defPath] = struct{}{}
	case s.Package == "":
		// A symbol whose package cannot be read is treated as the repository's
		// own: nothing can be proven about it.
		tl.inRepo++
	default:
		pk := normalizePackage(s.Package)
		if _, ok := p.installed[pk]; ok {
			tl.installed++
			if p.installedDocs == nil {
				p.installedDocs = map[string]struct{}{}
			}
			p.installedDocs[path] = struct{}{}
		}
		if _, ok := p.own[pk]; ok {
			tl.ownUndefined++
		}
		tl.outside++
		if tl.pkgs == nil {
			tl.pkgs = map[string]struct{}{}
		}
		tl.pkgs[s.Package] = struct{}{}
	}
}

// ProofStats is what Apply wrote.
type ProofStats struct {
	// Files is how many indexed files a compiler read.
	Files int
	// RuledOut is how many references were marked.
	RuledOut int
	// Unbound is how many of them the resolver had bound to a definition of the
	// repository: the compiler resolved that position outside it, so the
	// binding was removed.
	Unbound int
	// InstalledCopy is how many unbound references were marked as seen where the
	// compiler read an installed copy of the repository's own package.
	InstalledCopy int
}

// Apply writes the proof: the covered files and the ruled-out references. It
// first clears what an earlier resolve recorded, so the run describes the
// indexes imported now and no others.
func (p *Proof) Apply(ctx context.Context, reg *registry.Registry, runID int64) (ProofStats, int, error) {
	var st ProofStats
	if err := reg.ClearCompilerProof(ctx, runID); err != nil {
		return st, 0, err
	}
	byTool := map[string][]string{}
	for path, tool := range p.covered {
		byTool[tool] = append(byTool[tool], path)
	}
	tools := make([]string, 0, len(byTool))
	for t := range byTool {
		tools = append(tools, t)
	}
	sort.Strings(tools)
	for _, t := range tools {
		n, err := reg.RecordCompilerCoverage(ctx, runID, t, byTool[t])
		if err != nil {
			return st, 0, err
		}
		st.Files += n
	}
	if st.Files == 0 {
		return st, 0, nil
	}

	var (
		cur      siteAt
		have     bool
		refs     int
		markable []int64
		unbound  []int64
		local    []int64
	)
	var marks, installed []int64
	ruled := map[siteAt]struct{}{}
	// named memoises whether the index holds a symbol of a name in a file that
	// no compiler definition accounts for.
	type fileName struct{ path, name string }
	named := map[fileName]bool{}
	var guardErr error
	holds := func(path, name string) bool {
		k := fileName{path, name}
		if v, ok := named[k]; ok {
			return v
		}
		ids, err := reg.SymbolsNamed(ctx, runID, path, name)
		if err != nil && guardErr == nil {
			guardErr = err
		}
		// An error is read as "it does": nothing is ruled out on a failed check.
		v := err != nil
		for _, id := range ids {
			if _, matched := p.matchedIDs[id]; !matched {
				v = true
			}
		}
		named[k] = v
		return v
	}
	// A group that needs the registry's guard is judged after the stream ends:
	// the guard asks the registry, which cannot be asked while the references
	// are still being read. Every other group is judged at once.
	type candidate struct {
		at       siteAt
		refs     int
		markable []int64
		unbound  []int64
		local    []int64
	}
	var candidates []candidate
	decide := func(c candidate) {
		t := p.sites[c.at]
		v := none
		if t != nil {
			v = p.ruledOut(t, c.refs, c.at.name, holds)
		}
		switch {
		case v == strong:
			ruled[c.at] = struct{}{}
			marks = append(marks, c.markable...)
		case v == weak:
			// Not added to ruled: no heuristic edge is withdrawn on this ground.
			marks = append(marks, c.unbound...)
		case t != nil && p.seenAgainstInstalledCopy(c.at.path, t):
			installed = append(installed, c.unbound...)
		case len(c.local) > 0 && !holds(c.at.path, c.at.name):
			// Not added to ruled: no heuristic edge is withdrawn on this ground.
			marks = append(marks, c.local...)
		}
	}
	flush := func() {
		if !have {
			return
		}
		t := p.sites[cur]
		c := candidate{cur, refs, markable, unbound, local}
		switch {
		case t == nil && len(local) == 0:
		case len(local) > 0 || len(t.otherIn) > 0:
			c.markable, c.unbound, c.local = append([]int64(nil), markable...),
				append([]int64(nil), unbound...), append([]int64(nil), local...)
			candidates = append(candidates, c)
		default:
			decide(c)
		}
		markable, unbound, local, refs, have = markable[:0], unbound[:0], local[:0], 0, false
	}
	err := reg.CoveredReferences(ctx, runID, func(r registry.RefSite) error {
		k := siteAt{path: r.Path, line: r.Line, name: r.Name}
		if have && k != cur {
			flush()
		}
		cur, have = k, true
		refs++
		if !r.ByCompiler {
			markable = append(markable, r.ID)
			if !r.Resolved {
				unbound = append(unbound, r.ID)
			}
			// Only a reference nothing bound: one the resolver bound is left
			// as it is, because `const { jsx } = await import('./base')` makes
			// `jsx(...)` a call of the definition through an alias, and a
			// binding the resolver found for it is not undone by a local.
			if !r.Resolved && p.declaredBareCall(r) {
				local = append(local, r.ID)
			}
		}
		return nil
	})
	if err != nil {
		return st, 0, err
	}
	flush()
	for _, c := range candidates {
		decide(c)
	}
	if guardErr != nil {
		return st, 0, guardErr
	}
	n, wasBound, err := reg.MarkRuledOut(ctx, marks)
	st.RuledOut, st.Unbound = n, wasBound
	if err != nil {
		return st, 0, err
	}
	if st.InstalledCopy, err = reg.MarkInstalledCopy(ctx, installed); err != nil {
		return st, 0, err
	}
	withdrawn, err := p.withdraw(ctx, reg, runID, ruled)
	return st, withdrawn, err
}

// withdraw deletes the heuristic call edges the compilers proved false: an edge
// whose call site is in a covered file, whose target is declared in a covered
// file, and where the compiler resolved every call of that name on that line
// outside the repository. Same rule, same conditions as the Go type check's
// withdrawal (see Importer.withdrawContradicted), made with the same test that
// rules a reference out.
func (p *Proof) withdraw(ctx context.Context, reg *registry.Registry, runID int64, ruled map[siteAt]struct{}) (int, error) {
	if len(ruled) == 0 {
		return 0, nil
	}
	langs, err := reg.CoveredLanguages(ctx, runID)
	if err != nil {
		return 0, err
	}
	var drop []int64
	for _, lang := range langs {
		calls, err := reg.HeuristicCalls(ctx, runID, lang)
		if err != nil {
			return 0, err
		}
		for _, h := range calls {
			if _, ok := p.covered[h.File]; !ok {
				continue
			}
			if _, ok := p.covered[h.DstPath]; !ok {
				continue
			}
			if _, ok := ruled[siteAt{path: h.File, line: h.Line, name: h.DstName}]; ok {
				drop = append(drop, h.ID)
			}
		}
	}
	return reg.DeleteEdges(ctx, drop)
}

// declaredBareCall reports whether a reference is a call with no receiver
// written at exactly the position the compiler resolved to a local binding the
// document itself declares, of the reference's own length.
func (p *Proof) declaredBareCall(r registry.RefSite) bool {
	if r.ReceiverKind != model.ReceiverBare && r.ReceiverKind != "" {
		return false
	}
	n, ok := p.declared[siteCol{r.Path, r.Line, r.Col}]
	return ok && n == len(r.Name)
}

// ruledOut applies the rule on the type's comment to one (file, line, name)
// with refs references recorded on it. holds says whether the index has a
// symbol of a name in a file that no compiler definition accounts for.
func (p *Proof) ruledOut(t *siteTally, refs int, name string, holds func(path, name string) bool) verdict {
	if t.inRepo > 0 || t.outside+t.other+t.alias < refs {
		return none
	}
	for pkg := range t.pkgs {
		if _, own := p.own[normalizePackage(pkg)]; own {
			return none
		}
	}
	for path := range t.otherIn {
		if holds(path, name) {
			return none
		}
	}
	if t.alias > 0 {
		return weak
	}
	return strong
}

// verdict is how far an answer about a position settles its references.
type verdict int

const (
	// none: nothing is settled.
	none verdict = iota
	// weak: the references the resolver left unbound are settled; a binding it
	// found is kept, because the compiler's symbol may be an alias of it.
	weak
	// strong: every reference at the position is a use of something else, so a
	// binding to the definition asked about is undone and its heuristic edge
	// withdrawn.
	strong
)
