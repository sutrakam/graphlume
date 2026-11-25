package infer

import (
	"strings"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// # Macro invocations are not function calls
//
// `format!(...)`, `vec![...]` and `println!(...)` invoke a macro. A macro
// lives in its own namespace, and a function of the same name is not what the
// invocation reaches. fd bound std's `format!` to a test function named
// `format` at `high`, twice in thirty.
//
// The parser emits `reference.call.macro` for an invocation (name without the
// bang, a path in front as a scope receiver) and indexes `macro_rules! name` as a
// `function` whose qualified name ends in `!` (`src/error.rs:print_error!`), the
// one thing in the index that tells it from an `fn`; its kind stays `function`
// so that every tool that looks a function up by name still finds it. So:
//
//   - a macro invocation binds only to a macro definition (in the same file
//     first, then the one of that name in the repository), and otherwise to
//     nothing: never to a function;
//   - any other call never binds to a macro definition.
//
// A symbol of kind `macro` counts as a definition too, for a parser that gives
// macros a kind of their own.

const (
	// macroRefSuffix ends the kind of a reference to a macro invocation.
	macroRefSuffix = ".macro"
	// macroSymbolKind is the kind of a symbol that defines a macro, where a
	// parser gives macros a kind of their own.
	macroSymbolKind = "macro"
	// macroFQNMarker ends the qualified name of a Rust macro_rules! definition,
	// which is indexed as a function: `src/error.rs:print_error!`. The parser
	// writes it (internal/parse/lang_rust.go); no Rust identifier ends in it,
	// and the language test keeps Ruby's `save!` out.
	macroFQNMarker = "!"
)

// isMacroDefinition reports whether a symbol defines a macro.
func isMacroDefinition(s registry.SymbolIndexEntry) bool {
	if s.Kind == macroSymbolKind {
		return true
	}
	return s.Kind == string(model.KindFunction) && strings.HasSuffix(s.FQN, macroFQNMarker) &&
		languageOfCandidate(s) == "rust"
}

// macroVisibleFrom reports whether a macro definition can be seen from a scope
// of its file: one written inside a function body is visible only in it.
func macroVisibleFrom(scope string, s registry.SymbolIndexEntry) bool {
	s.FQN = strings.TrimSuffix(s.FQN, macroFQNMarker)
	return visibleFrom(scope, s)
}

// isMacroInvocation reports whether a reference is a macro invocation.
func isMacroInvocation(refKind string) bool { return strings.HasSuffix(refKind, macroRefSuffix) }

// withoutMacros drops macro definitions from the candidates of a call that is
// not a macro invocation: a function call never reaches one.
func withoutMacros(cands []registry.SymbolIndexEntry) []registry.SymbolIndexEntry {
	has := false
	for _, s := range cands {
		if isMacroDefinition(s) {
			has = true
			break
		}
	}
	if !has {
		return cands
	}
	kept := make([]registry.SymbolIndexEntry, 0, len(cands))
	for _, s := range cands {
		if !isMacroDefinition(s) {
			kept = append(kept, s)
		}
	}
	return kept
}

// callMacro binds a macro invocation to a macro definition, and to nothing
// else. In the file that defines it the macro is in scope from its definition
// on; elsewhere `#[macro_use]` and `#[macro_export]` make it crate-wide, which
// the index does not record, so the one macro of that name in the repository is
// a weaker claim.
func (r *Resolver) callMacro(c call) decision {
	var macros, here []registry.SymbolIndexEntry
	for _, s := range c.cands {
		if !isMacroDefinition(s) || !macroVisibleFrom(c.scope, s) {
			continue
		}
		macros = append(macros, s)
		if s.FileID == c.fr.FileID {
			here = append(here, s)
		}
	}
	if first, ok := firstDeclaration(here); ok {
		return bound(candidate{first, model.ConfidenceHigh, "macro defined in the same file"})
	}
	if len(here) > 1 {
		return ambiguousCall
	}
	if len(macros) == 0 {
		if c.spec.isCoreSyntax(c.ref.Name) || c.spec.isCoreFunction(c.ref.Name) {
			return externalCall
		}
		return unresolvedCall
	}
	if first, ok := firstDeclaration(macros); ok && !r.callsTestHelper(c, first) {
		return bound(candidate{first, model.ConfidenceMedium, "the only macro of this name in the repository"})
	}
	if len(macros) > 1 {
		return ambiguousCall
	}
	return unresolvedCall
}
