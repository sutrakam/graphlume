package parse

import (
	ts "github.com/odvcencio/gotreesitter"

	"github.com/sutrakam/graphlume/pkg/model"
)

// Clause merging.
//
// In Elixir and Erlang a function is written as a sequence of clauses, one per
// pattern: `def f(0)` then `def f(n)`. A definition query can only match one
// clause head at a time, so a function of five clauses arrived as five
// symbols of one name and every lookup by name returned duplicates.
//
// A language opts in with registerClauseMerge. The parser then folds
// CONSECUTIVE definitions into one symbol when they agree on name, kind, owner
// and arity. The result starts where the first clause starts and ends where the
// last one ends, so a call written in any clause is still attributed to the
// function.
//
// Two things keep the fold from inventing a function:
//
//   - consecutive means no other symbol of the same owner lies between the
//     two. `f`, `g`, `f` is two definitions of f (the compiler warns about it
//     in both languages), and they stay two symbols;
//   - arity is part of the identity where the language has one. `def f(a)` and
//     `def f(a, b)` are different functions. The rule counts the head's
//     arguments as written, so `def f(a, b \\ 1)` is f/2 and `def f(a)` after
//     it is a separate f/1, which is how the language numbers them when the
//     default is expanded.

// clauseArity returns the arity of the clause head whose function name is the
// given node, as a string comparable between clauses, or "" when the head does
// not say.
type clauseArity func(name *ts.Node, lang *ts.Language) string

var clauseMerges = map[string]clauseArity{}

// registerClauseMerge makes a language's consecutive same-named definitions one
// symbol. Call it from init in the language's lang_<language>.go. arity may be
// nil for a language with no arity in its function heads, in which case
// clauses merge by name alone.
func registerClauseMerge(language string, arity clauseArity) {
	if _, dup := clauseMerges[language]; dup {
		duplicateRegistrations = append(duplicateRegistrations, language+" (clause merge)")
	}
	if arity == nil {
		arity = func(*ts.Node, *ts.Language) string { return "" }
	}
	clauseMerges[language] = arity
}

// Stub folding.
//
// A declaration with no body can precede the declaration that has one:
// TypeScript's overload signatures (`function f(a: string): void;` before
// `function f(a: any) {}`) and Python's `@overload` stubs. Each is a symbol of
// the same name, so a function with three stubs was four definitions and every
// lookup of it was ambiguous. A language opts in with registerStubFold; a
// definition whose name the check calls a stub is then folded into the next
// definition of the same name, kind and owner, whatever their arities: the
// stubs state a function's several signatures, and the arity of the whole is
// unknown unless they agree. A stub is the only thing that folds this way, so
// two getters, or a getter and a setter, stay two symbols.

// stubCheck reports whether the definition with this name node is a stub.
type stubCheck func(name *ts.Node, lang *ts.Language, src []byte) bool

var stubFolds = map[string]stubCheck{}

// registerStubFold makes a language's stubs fold into the definition that
// follows them. Call it from init in the language's lang_<language>.go.
func registerStubFold(language string, isStub stubCheck) {
	if _, dup := stubFolds[language]; dup {
		duplicateRegistrations = append(duplicateRegistrations, language+" (stub fold)")
	}
	stubFolds[language] = isStub
}

// clauseFacts is what the tree says about each definition for merging: its
// arity key, and whether it is a stub.
type clauseFacts struct {
	// clauses is set for a language that merges clauses by arity key; a
	// language that only folds stubs leaves it false, so that two plain
	// definitions of one name (a getter and a setter) stay two.
	clauses bool
	arity   map[symbolAt]string
	stub    map[symbolAt]bool
}

// symbolAt identifies a symbol before merging: where its range starts, and its
// name. The pair is what dedupeSymbols already treats as one declaration.
type symbolAt struct {
	line, col int
	name      string
}

// clauseArities reads each definition's arity and stub-ness from the tree, for
// a language that merges clauses or folds stubs; nil for every other language.
func (p *genericParser) clauseArities(tree *ts.Tree, src []byte, tags []ts.Tag) *clauseFacts {
	arity, merges := clauseMerges[p.entry.Name]
	isStub, folds := stubFolds[p.entry.Name]
	if (!merges && !folds) || tree == nil || p.lang == nil {
		return nil
	}
	root := tree.RootNode()
	if root == nil {
		return nil
	}
	out := &clauseFacts{
		clauses: merges,
		arity:   make(map[symbolAt]string, len(tags)),
		stub:    make(map[symbolAt]bool),
	}
	for _, tag := range tags {
		if len(tag.Kind) < len("definition") || tag.Kind[:len("definition")] != "definition" {
			continue
		}
		name := root.NamedDescendantForByteRange(tag.NameRange.StartByte, tag.NameRange.EndByte)
		if name == nil {
			continue
		}
		r := toRange(tag.Range)
		at := symbolAt{r.Start.Line, r.Start.Col, tag.Name}
		if merges {
			out.arity[at] = arity(name, p.lang)
		}
		if folds && isStub(name, p.lang, src) {
			out.stub[at] = true
		}
	}
	return out
}

// isClauseKind is the kinds a function can be written as. A repeated module
// attribute or type is a redefinition, not a further clause.
func isClauseKind(k model.EntityKind) bool {
	return k == model.KindFunction || k == model.KindMethod
}

// mergeClauses folds consecutive clauses of one function into one symbol, and
// stubs into the definition that follows them. The input is sorted by
// position, as dedupeSymbols leaves it.
func mergeClauses(in []model.Symbol, facts *clauseFacts) []model.Symbol {
	if len(in) < 2 || facts == nil {
		return in
	}
	type key struct {
		name  string
		kind  model.EntityKind
		arity string
	}
	at := func(s model.Symbol) symbolAt {
		return symbolAt{s.Range.Start.Line, s.Range.Start.Col, s.Name}
	}
	keyOf := func(s model.Symbol) key {
		return key{s.Name, s.Kind, facts.arity[at(s)]}
	}
	before := func(a, b model.Position) bool {
		return a.Line < b.Line || (a.Line == b.Line && a.Col < b.Col)
	}

	out := make([]model.Symbol, 0, len(in))
	// open holds the indexes of the output symbols whose range still encloses
	// the current position; the last is the owner. lastChild is the most recent
	// output symbol of each owner (-1 for none, keyed by owner index; the file
	// itself is owner -1). pending marks the output symbols that are still only
	// stubs, so that a chain of stubs folds as far as the definition that ends
	// it.
	var open []int
	lastChild := map[int]int{}
	pending := map[int]bool{}
	for _, s := range in {
		for len(open) > 0 && !before(s.Range.Start, out[open[len(open)-1]].Range.End) {
			open = open[:len(open)-1]
		}
		owner := -1
		if len(open) > 0 {
			owner = open[len(open)-1]
		}
		if i, ok := lastChild[owner]; ok && isClauseKind(s.Kind) && !before(s.Range.Start, out[i].Range.End) {
			byStub := pending[i] && out[i].Name == s.Name && out[i].Kind == s.Kind
			if byStub || (facts.clauses && keyOf(out[i]) == keyOf(s)) {
				if before(out[i].Range.End, s.Range.End) {
					out[i].Range.End = s.Range.End
				}
				if byStub && out[i].ParamCount != s.ParamCount {
					// The stubs state several signatures; no one count is the
					// function's.
					out[i].ParamCount = model.ArityUnknown
				}
				pending[i] = pending[i] && facts.stub[at(s)]
				// The merged symbol covers s, so what s contains is inside it.
				open = append(open, i)
				continue
			}
		}
		out = append(out, s)
		lastChild[owner] = len(out) - 1
		pending[len(out)-1] = facts.stub[at(s)]
		open = append(open, len(out)-1)
	}
	return out
}
