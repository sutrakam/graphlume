package multirepo

import (
	"context"
	"fmt"
	"go/ast"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// Method stitching: following `x.M()` into a provider's method when x's type
// is one of the provider's types.
//
// THREE WAYS TO KNOW THE TYPE, in order of strength, and each link carries the
// one it used as its "type resolution" leg:
//
//   - receiver_kind `type`: the importer's own source states it, as a
//     parameter, a typed local or a composite literal (`c *lib.Client`,
//     `var c lib.Client`, `c := &lib.Client{}`). The parser recorded the type
//     beside the call, so the leg is `high`.
//   - receiver_kind `path`: `lib.Default.M()`, a provider variable or
//     constant whose declaration states its type (`var Default *Client`).
//     A declaration with no stated type (`var Default = &Client{}`) is not
//     followed: its initialiser is an expression, and reading the type of an
//     expression is the type checker's work. `medium` at most.
//   - receiver_kind `value`: a local initialised once from a provider call
//     whose declared result is one of the provider's types
//     (`c := lib.NewClient()` with `func NewClient() *Client`), by the rules
//     traceValue documents. `medium` at most, as the in-repository resolver
//     holds a type read through a return type below a stated one.
//
// THE METHOD, once the type is known, is found by Go's own selector rule read
// from the provider's source: a method declared on T or *T first, then one
// promoted through an embedded field at the shallowest depth that has one.
//
//   - A method declared on T: `high` for one declaration; build-constrained
//     variants with one signature are linked to each at `medium`, and
//     variants that differ are ambiguous, exactly as for functions.
//   - T an interface: the call is linked to the interface's method at
//     `medium`. It is a real target — the declaration the caller compiled
//     against, whose change breaks the caller — but which implementation runs
//     is decided at run time, so the link says that and goes no higher.
//     Counting the call instead would drop every caller of an interface from
//     the blast radius of changing that interface, which is the change most
//     likely to break another repository.
//   - A promoted method: linked at `medium`, and only when every embedded
//     field at that depth or shallower has a type declared in the same
//     package. A field whose type is in another package, or a defined type
//     whose underlying type is another named type, may supply the method
//     itself, so the call is counted as unreadable embedding rather than
//     linked to a method that may be shadowed.
//
// Everything else is counted, by reason (reasonLabels), and never linked.

// Reasons a method call was not linked. They are JSON keys of
// GoModulePair.MethodNotResolvable, so they are stable words, not prose.
const (
	// reasonUntraced: a call on a local value or an expression that could not
	// be traced to a provider call. Counted against every provider module the
	// file imports, so it is an upper bound: most such calls are on values of
	// other packages' types.
	reasonUntraced = "untraced_value"
	// reasonReassigned: the local was traced to a provider call and is
	// assigned again.
	reasonReassigned = "reassigned"
	// reasonSeveralValues: several names from one call, or a function that
	// returns several values.
	reasonSeveralValues = "several_values"
	// reasonNoDeclaredType: the variable, or the function the value came from,
	// declares no type of the provider package.
	reasonNoDeclaredType = "no_declared_type"
	// reasonForeignType: the declared type is a type of another package.
	reasonForeignType = "type_in_other_package"
	// reasonNoSuchDecl: the receiver names no type, variable or function of
	// the provider package.
	reasonNoSuchDecl = "no_such_declaration"
	// reasonNoSuchMethod: the type has no method of that name, declared or
	// promoted, or the name is a field.
	reasonNoSuchMethod = "no_such_method"
	// reasonEmbedding: the method may be promoted through an embedded field
	// whose method set is not readable here.
	reasonEmbedding = "unreadable_embedding"
	// reasonFieldAccess: `lib.Var.Field.M()`, a call through a field.
	reasonFieldAccess = "field_access"
	// reasonUnreadable: the provider package's source did not parse.
	reasonUnreadable = "unreadable_source"
	// reasonAmbiguousType: declarations the call's type depends on differ
	// across build-constrained files. Counted as ambiguous, not here.
	reasonAmbiguousType = "ambiguous_type"
)

// reasonLabels are the words the text report uses for each reason.
var reasonLabels = map[string]string{
	reasonUntraced:       "on values not traced to a provider call (an upper bound)",
	reasonReassigned:     "reassigned",
	reasonSeveralValues:  "several values",
	reasonNoDeclaredType: "no declared type",
	reasonForeignType:    "type in another package",
	reasonNoSuchDecl:     "no such type or variable",
	reasonNoSuchMethod:   "no such method",
	reasonEmbedding:      "unreadable embedding",
	reasonFieldAccess:    "through a field",
	reasonUnreadable:     "source not readable",
}

// maxEmbedDepth bounds the promotion search. Go has no bound, but an
// embedding chain deeper than this is rare enough that stopping is cheaper
// than following, and stopping counts the call rather than guessing.
const maxEmbedDepth = 5

// methodCall is a call on a value of a provider type, waiting for lookup.
type methodCall struct {
	call     registry.GoImportedCall
	provider providerModule
	dir      string
	// binding says which qualifier was bound to which import and how: the
	// import binding leg's detail.
	binding string
	// typeName is the provider type the receiver has; varName the provider
	// variable a `path` receiver names. One of them is set.
	typeName, varName string
	// typeLeg is the type resolution leg, filled once the type is known.
	typeLeg Leg
	// selfCalls are the methods of `x = x.M()` reassignments to check.
	selfCalls []string
	// failReason, failWhy record a trace that reached a provider and then
	// failed: counted against that provider, never linked.
	failReason, failWhy string
}

// methodTarget is one method declaration a call resolves to.
type methodTarget struct {
	owner string
	decl  goFuncDecl
}

// methodOutcome is the result of looking a method up on a type.
type methodOutcome struct {
	targets []methodTarget
	conf    model.Confidence
	detail  string
	note    string
	// reason is set when nothing is linked; ambiguous marks a reason that is
	// counted as ambiguous rather than not resolvable.
	reason, why string
	ambiguous   bool
	candidates  []string
}

// resolveMethod finds method on type typeName of the package, by Go's
// selector rule.
func resolveMethod(pkg *goPkgSyntax, dir, typeName, method string) methodOutcome {
	fail := func(reason, why string) methodOutcome {
		return methodOutcome{reason: reason, why: why, ambiguous: reason == reasonAmbiguousType}
	}
	if len(pkg.unparsed) > 0 {
		return fail(reasonUnreadable, fmt.Sprintf("%s of the provider does not parse", strings.Join(pkg.unparsed, ", ")))
	}
	if !isExportedGo(method) {
		return fail(reasonNoSuchMethod, fmt.Sprintf("%s is unexported, so no other package can call it", method))
	}

	type node struct {
		name string
		via  []string
	}
	type found struct {
		owner string
		via   []string
		decls []goFuncDecl
		iface bool
		field bool
	}
	root, _, reason, why := pkg.shape(typeName)
	if reason != "" {
		return fail(reason, why)
	}
	level := []node{{name: root}}
	seen := map[string]bool{root: true}
	unknown := "" // the first embedded field whose method set cannot be read
	for depth := 0; depth <= maxEmbedDepth && len(level) > 0; depth++ {
		var hits []found
		var next []node
		nextUnknown := ""
		for _, n := range level {
			name, s, reason, why := pkg.shape(n.name)
			if reason != "" {
				if reason == reasonAmbiguousType && depth == 0 {
					return fail(reason, why)
				}
				if unknown == "" {
					unknown = fmt.Sprintf("embedded %s (%s)", n.name, why)
				}
				continue
			}
			switch {
			case s.iface && len(s.ifaceMethods[method]) > 0:
				hits = append(hits, found{owner: name, via: n.via, decls: s.ifaceMethods[method], iface: true})
			case !s.iface && len(pkg.methods[name][method]) > 0:
				hits = append(hits, found{owner: name, via: n.via, decls: pkg.methods[name][method]})
			case s.fields[method]:
				hits = append(hits, found{owner: name, via: n.via, field: true})
			}
			if s.definedFrom != "" && nextUnknown == "" {
				nextUnknown = fmt.Sprintf("%s's underlying type %s, whose promoted methods are not followed", name, s.definedFrom)
			}
			for _, e := range s.embedded {
				qual, en, ok := namedType(e)
				label := printNode(pkg.fset, e)
				if !ok || qual != "" {
					if nextUnknown == "" {
						nextUnknown = fmt.Sprintf("embedded field %s of %s, a type of another package", label, name)
					}
					continue
				}
				if seen[en] {
					continue
				}
				seen[en] = true
				next = append(next, node{name: en, via: append(append([]string{}, n.via...), en)})
			}
		}
		if len(hits) > 0 {
			if unknown != "" {
				return fail(reasonEmbedding, fmt.Sprintf(
					"%s may also be promoted through %s, which could shadow it", method, unknown))
			}
			if len(hits) > 1 {
				var c []string
				for _, h := range hits {
					c = append(c, h.owner+"."+method)
				}
				sort.Strings(c)
				return methodOutcome{reason: "promoted_ambiguously", ambiguous: true, candidates: c,
					why: fmt.Sprintf("%s is promoted from %d embedded types at the same depth", method, len(hits))}
			}
			h := hits[0]
			if h.field {
				return fail(reasonNoSuchMethod, fmt.Sprintf("%s.%s is a field, not a method", h.owner, method))
			}
			return methodLinkOutcome(dir, root, method, h.owner, h.via, h.decls, h.iface)
		}
		if unknown == "" && nextUnknown != "" {
			unknown = nextUnknown
		}
		level = next
	}
	if unknown != "" {
		return fail(reasonEmbedding, fmt.Sprintf("%s is not declared on %s, and may be promoted through %s", method, root, unknown))
	}
	return fail(reasonNoSuchMethod, fmt.Sprintf("%s declares no method %s and promotes none", root, method))
}

// methodLinkOutcome grades the declarations one lookup found.
func methodLinkOutcome(dir, root, method, owner string, via []string, decls []goFuncDecl, iface bool) methodOutcome {
	out := methodOutcome{conf: model.ConfidenceHigh}
	for _, d := range decls {
		out.targets = append(out.targets, methodTarget{owner: owner, decl: d})
	}
	where := fmt.Sprintf("in %s", orDot(dir))
	switch {
	case len(via) > 0:
		out.conf = model.ConfidenceMedium
		out.detail = fmt.Sprintf("%s.%s, promoted to %s through embedded %s %s",
			owner, method, root, strings.Join(via, " then "), where)
	case iface:
		out.detail = fmt.Sprintf("%s.%s, declared by the interface %s %s", owner, method, owner, where)
	default:
		out.detail = fmt.Sprintf("the one method %s declared on %s %s", method, owner, where)
	}
	if iface {
		out.conf = model.ConfidenceMedium
		out.note = fmt.Sprintf("%s is an interface: which implementation the call reaches is decided at run time", owner)
	}
	if len(decls) > 1 {
		for _, d := range decls[1:] {
			if d.sig != decls[0].sig {
				out = methodOutcome{reason: reasonAmbiguousType, ambiguous: true, why: fmt.Sprintf(
					"%d declarations of %s.%s %s differ in signature, so which one this build calls "+
						"cannot be told from source text", len(decls), owner, method, where)}
				for _, d := range decls {
					out.candidates = append(out.candidates, fmt.Sprintf("%s:%d", d.file, d.line))
				}
				return out
			}
		}
		out.conf = model.ConfidenceMedium
		out.detail = fmt.Sprintf("one of %d same-signature declarations of %s.%s %s (build-constrained "+
			"variants); linked to each", len(decls), owner, method, where)
		if out.note == "" {
			out.note = "which variant a build compiles depends on its GOOS, GOARCH or build tags"
		}
	}
	return out
}

// declaredType reads a provider declaration's stated type as a type of the
// package, or why it is not one.
func declaredType(pkg *goPkgSyntax, e ast.Expr, what string) (string, string, string) {
	if e == nil {
		return "", reasonNoDeclaredType, what + " declares no type; its initialiser is not read"
	}
	qual, name, ok := namedType(e)
	switch {
	case !ok:
		return "", reasonNoDeclaredType, fmt.Sprintf("%s's type %s is not a named type", what, printNode(pkg.fset, e))
	case qual != "":
		return "", reasonForeignType, fmt.Sprintf("%s's type %s.%s belongs to another package", what, qual, name)
	case len(pkg.types[name]) == 0:
		return "", reasonNoDeclaredType, fmt.Sprintf("%s's type %s is not a type of the package", what, name)
	}
	return name, "", ""
}

// varType is the type a provider variable or constant declares.
func varType(pkg *goPkgSyntax, dir, name string) (typ string, reason, why string) {
	if len(pkg.unparsed) > 0 {
		return "", reasonUnreadable, fmt.Sprintf("%s of the provider does not parse", strings.Join(pkg.unparsed, ", "))
	}
	decls := pkg.vals[name]
	if len(decls) == 0 {
		return "", reasonNoSuchDecl, fmt.Sprintf("no package-level variable or constant %s in %s", name, orDot(dir))
	}
	what := "var " + name
	if decls[0].isConst {
		what = "const " + name
	}
	first, reason, why := declaredType(pkg, decls[0].typ, what)
	if reason != "" {
		return "", reason, why
	}
	for _, d := range decls[1:] {
		t, r, _ := declaredType(pkg, d.typ, what)
		if r != "" || t != first {
			return "", reasonAmbiguousType, fmt.Sprintf(
				"%d declarations of %s differ in type (build-constrained variants)", len(decls), name)
		}
	}
	return first, "", ""
}

// returnedType is the one type a provider function declares it returns.
func returnedType(pkg *goPkgSyntax, dir, fn string) (typ string, reason, why string) {
	if len(pkg.unparsed) > 0 {
		return "", reasonUnreadable, fmt.Sprintf("%s of the provider does not parse", strings.Join(pkg.unparsed, ", "))
	}
	decls := pkg.funcs[fn]
	if len(decls) == 0 {
		return "", reasonNoSuchDecl, fmt.Sprintf("no package-level function %s in %s", fn, orDot(dir))
	}
	var first string
	for i, d := range decls {
		var t, r, w string
		res := d.typ.Results
		n := 0
		if res != nil {
			for _, f := range res.List {
				n += max(1, len(f.Names))
			}
		}
		switch {
		case n == 0:
			r, w = reasonNoDeclaredType, fmt.Sprintf("%s returns nothing", fn)
		case n > 1:
			r, w = reasonSeveralValues, fmt.Sprintf("%s returns %d values", fn, n)
		default:
			t, r, w = declaredType(pkg, res.List[0].Type, fn+"'s result")
		}
		if i == 0 {
			if r != "" {
				return "", r, w
			}
			first = t
			continue
		}
		if r != "" || t != first {
			return "", reasonAmbiguousType, fmt.Sprintf(
				"%d declarations of %s declare different results (build-constrained variants)", len(decls), fn)
		}
	}
	return first, "", ""
}

// selfCallsKeepType reports whether each `x = x.M()` reassignment leaves x the
// type typ: M must be one method declared on typ (not promoted, not variants
// that differ) whose one declared result is typ again. The rule of
// internal/resolve/infer/returned.go reassignmentsKeepType.
func selfCallsKeepType(pkg *goPkgSyntax, typ string, methods []string) bool {
	for _, m := range methods {
		decls := pkg.methods[typ][m]
		if len(decls) == 0 {
			return false
		}
		for _, d := range decls {
			res := d.typ.Results
			if res == nil || len(res.List) != 1 || len(res.List[0].Names) > 1 {
				return false
			}
			qual, name, ok := namedType(res.List[0].Type)
			if !ok || qual != "" || name != typ {
				return false
			}
		}
	}
	return true
}

// methodNotLinked counts one method call that was not linked, and samples it
// unless it is an untraced value (see GoModuleReport.MethodsNotResolved).
func (st *goStitch) methodNotLinked(imp *goMember, pm providerModule, reason string, s GoCallSample) {
	pair := st.pair(imp, pm)
	pair.MethodNotResolvable[reason]++
	if reason != reasonUntraced {
		st.methodsUnresolved = appendSample(st.methodsUnresolved, s, st.truncated, "go_methods_not_resolved")
	}
}

// methodCallText is a method call as the report writes it: the receiver as
// written for a variable or a provider variable, the stated type for a
// receiver the importer typed (its own name says nothing about the target).
func methodCallText(mc methodCall) string {
	if mc.typeName != "" && mc.call.ReceiverKind == model.ReceiverType {
		return "(" + mc.call.Receiver + ")." + mc.call.Name
	}
	return mc.call.Receiver + "." + mc.call.Name
}

// linkMethods resolves every method call that reached a provider and links it
// or counts it.
func (f *Federator) linkMethods(ctx context.Context, st *goStitch, imp *goMember, calls []methodCall) error {
	for _, mc := range calls {
		if err := ctx.Err(); err != nil {
			return err
		}
		pm := mc.provider
		sample := GoCallSample{
			Member: imp.name, File: mc.call.FilePath, Line: mc.call.Line, Call: methodCallText(mc),
			Provider: pm.member.name,
		}
		notLinked := func(reason, why string) {
			sample.Reason = why
			st.methodNotLinked(imp, pm, reason, sample)
		}
		if mc.failReason != "" {
			notLinked(mc.failReason, mc.failWhy)
			continue
		}
		pkg, err := pm.member.providerPackage(ctx, mc.dir)
		if err != nil {
			return err
		}
		typeName, typeLeg := mc.typeName, mc.typeLeg
		if mc.varName != "" {
			if strings.Contains(mc.varName, ".") {
				notLinked(reasonFieldAccess, fmt.Sprintf("%s reaches the method through a field, and fields' types "+
					"are not followed", mc.call.Receiver))
				continue
			}
			typ, reason, why := varType(pkg, mc.dir, mc.varName)
			if reason == reasonAmbiguousType {
				st.pair(imp, pm).MethodAmbiguous++
				sample.Reason = why
				st.ambiguous = appendSample(st.ambiguous, sample, st.truncated, "go_ambiguous")
				continue
			}
			if reason != "" {
				notLinked(reason, why)
				continue
			}
			typeName = typ
			typeLeg = Leg{
				Name: "type resolution", Confidence: model.ConfidenceMedium, Heuristic: true,
				Detail: fmt.Sprintf("%s is declared in %s with the type %s", mc.varName, orDot(mc.dir),
					printNode(pkg.fset, pkg.vals[mc.varName][0].typ)),
			}
		}
		if len(mc.selfCalls) > 0 && !selfCallsKeepType(pkg, typeName, mc.selfCalls) {
			notLinked(reasonReassigned, fmt.Sprintf("%s is assigned again from its own method (%s), which is not "+
				"one method of %s declared to return %s", mc.call.Receiver, strings.Join(mc.selfCalls, ", "),
				typeName, typeName))
			continue
		}

		out := resolveMethod(pkg, mc.dir, typeName, mc.call.Name)
		if out.ambiguous {
			st.pair(imp, pm).MethodAmbiguous++
			sample.Reason, sample.Candidates = out.why, out.candidates
			st.ambiguous = appendSample(st.ambiguous, sample, st.truncated, "go_ambiguous")
			continue
		}
		if out.reason != "" {
			notLinked(out.reason, out.why)
			continue
		}
		hits, err := pm.member.methodSymbols(ctx, mc.dir, mc.call.Name, out.targets)
		if err != nil {
			return err
		}
		if len(hits) == 0 {
			notLinked(reasonNoSuchMethod, fmt.Sprintf("%s holds no indexed method symbol for %s.%s",
				pm.member.name, out.targets[0].owner, mc.call.Name))
			continue
		}

		caller, callerID, err := imp.enclosing(ctx, mc.call)
		if err != nil {
			return err
		}
		st.pair(imp, pm).MethodLinks++
		for _, h := range hits {
			legs := []Leg{
				{Name: "import binding", Confidence: model.ConfidenceHigh, Heuristic: true, Detail: mc.binding},
				{
					Name: "module ownership", Confidence: model.ConfidenceHigh, Heuristic: true,
					Detail: fmt.Sprintf("%s is in module %s, which only %s defines (%s)",
						mc.call.ImportPath, pm.module.Path, pm.member.name, pm.module.File),
				},
				typeLeg,
				{Name: "method lookup", Confidence: out.conf, Heuristic: true, Detail: out.detail},
			}
			confs := make([]model.Confidence, 0, len(legs))
			for _, l := range legs {
				confs = append(confs, l.Confidence)
			}
			conf := minConfidence(confs...)
			if conf == model.ConfidenceExact {
				// Unreachable while every leg is heuristic; stated so it stays so.
				conf = model.ConfidenceHigh
			}
			st.links = append(st.links, GoLink{
				FromMember: imp.name, FromSymbol: caller, FromSymbolID: callerID,
				FromFile: mc.call.FilePath, FromLine: mc.call.Line,
				Call: sample.Call, CallKind: callKindMethod, ImportPath: mc.call.ImportPath,
				ToMember: pm.member.name, Module: pm.module.Path,
				ToSymbol: qualifiedIn(h.Name, h.FQN, h.FilePath), ToSymbolID: h.ID, ToKind: h.Kind,
				ToFile: h.FilePath, ToLine: h.StartLine,
				Confidence: conf, Legs: legs, Note: out.note,
			})
		}
	}
	return nil
}

// The two kinds of call a link can come from.
const (
	callKindFunction = "function"
	callKindMethod   = "method"
)

// methodSymbols maps target declarations to the provider's indexed method
// symbols, which carry the ids callers and impact walk from. A declaration the
// index holds no symbol for is dropped, and the caller counts the call when
// none is left.
func (gm *goMember) methodSymbols(ctx context.Context, dir, method string, targets []methodTarget) ([]registry.SymbolHit, error) {
	if gm.methodHits == nil {
		gm.methodHits = map[string][]registry.SymbolHit{}
	}
	key := dir + "\x00" + method
	hits, ok := gm.methodHits[key]
	if !ok {
		var err error
		hits, err = gm.src.GoPackageMethods(ctx, gm.runID, dir, []string{method})
		if err != nil {
			return nil, fmt.Errorf("multirepo: member %q: read methods in %q: %w", gm.name, orDot(dir), err)
		}
		gm.methodHits[key] = hits
	}
	var out []registry.SymbolHit
	for _, t := range targets {
		var match *registry.SymbolHit
		for i := range hits {
			h := &hits[i]
			if h.FilePath != t.decl.file || h.FQN != h.FilePath+":"+t.owner+"."+method {
				continue
			}
			// One file can hold the same owner and name only through a
			// parse the compiler would reject; the line decides then.
			if match == nil || h.StartLine == t.decl.line {
				match = h
			}
		}
		if match != nil {
			out = append(out, *match)
		}
	}
	return out, nil
}
