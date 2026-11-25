package parse

import (
	"strings"

	ts "github.com/odvcencio/gotreesitter"
)

// # Variables constructed by declaration (patterns in lang_cpp.go)
//
//	MutexLock l(mu);                 a construction of MutexLock
//	BenchmarkInstance inst(a, b);    a construction of BenchmarkInstance
//	ValueUnion buff{n};              a construction of ValueUnion
//	ns::Thing t(3);  Box<int> b(4);  of Thing, of Box
//	Foo k, m(2);                     of Foo (once for m)
//
// Each is recorded like Java's `new`: the type is the tag, with the kind
// reference.call.new, so the resolver binds it to the class as an
// instantiation. Not recorded: a declaration of a built-in type (`int x(3);`,
// `auto q(3);`), a type in std (a library type is not in the repository), and
// any declaration that is a function declaration.
//
// The most vexing parse is the case that cannot be told from the tree.
// `Foo f(a);` is the declaration of a function f taking an a, or the
// construction of a Foo from a, and the grammar reads it as the function. The
// rule here: inside a function body, a declaration of a simple type whose
// parenthesised list holds one to four bare names and nothing else (`MutexLock
// l(mu);`, `Inst i(a, b);`) is a construction, because a function prototype
// written inside a body is all but unknown. Outside a function body (a
// namespace, a class) the same text is skipped as a possible function
// declaration, and so are `Foo h();` and `Foo f(int);`, which can only be one.
// A bare-name list that includes a qualified or templated name (`Foo f(ns::x)`)
// is not matched either.
//
// # Test and registration macros
//
// `TEST(Suite, Name) { ... }` is a function definition to the grammar, with no
// return type, and was indexed as a function called TEST: 99 of them in
// benchmark, none distinguishable. For the googletest family (TEST, TEST_F,
// TEST_P, TYPED_TEST, BENCHMARK, BENCHMARK_F) the symbol is named by its
// arguments, `Suite.Name`, so each test is one distinctly named function.
// BENCHMARK_F(Fixture, Name)(benchmark::State& st) { } is read through its inner
// declarator. Any other upper-case name in function position with no return
// type (a registration macro, `REGISTER_THING(x) { ... }`) defines nothing here
// and is dropped. A constructor declared in a class body also has no return
// type and is left alone.
func init() {
	definitionRefiners["cpp"] = refineCppMacros
}

var cppTestMacros = map[string]bool{
	"TEST": true, "TEST_F": true, "TEST_P": true, "TYPED_TEST": true,
	"BENCHMARK": true, "BENCHMARK_F": true,
}

func isUpperMacroName(s string) bool {
	if len(s) < 2 || s[0] < 'A' || s[0] > 'Z' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

func refineCppMacros(root *ts.Node, lang *ts.Language, _ string, src []byte, tags []ts.Tag) map[uint32]definitionFix {
	var fixes map[uint32]definitionFix
	for _, tag := range tags {
		if !strings.HasPrefix(tag.Kind, "definition") || !isUpperMacroName(tag.Name) {
			continue
		}
		name := root.NamedDescendantForByteRange(tag.NameRange.StartByte, tag.NameRange.EndByte)
		if name == nil || name.Type(lang) != "identifier" {
			continue
		}
		decl := name.Parent()
		if decl == nil || decl.Type(lang) != "function_declarator" {
			continue
		}
		def := decl.Parent()
		if def != nil && def.Type(lang) == "function_declarator" {
			def = def.Parent()
		}
		if def == nil || def.Type(lang) != "function_definition" || def.ChildByFieldName("type", lang) != nil {
			continue
		}
		if p := def.Parent(); p != nil && p.Type(lang) == "field_declaration_list" {
			continue
		}
		if fixes == nil {
			fixes = map[uint32]definitionFix{}
		}
		if !cppTestMacros[tag.Name] {
			fixes[tag.NameRange.StartByte] = definitionFix{drop: true}
			continue
		}
		var args []string
		if params := decl.ChildByFieldName("parameters", lang); params != nil {
			for i := 0; i < params.NamedChildCount(); i++ {
				args = append(args, strings.Join(strings.Fields(params.NamedChild(i).Text(src)), ""))
			}
		}
		if len(args) == 0 || len(args) > 2 {
			fixes[tag.NameRange.StartByte] = definitionFix{drop: true}
			continue
		}
		fixes[tag.NameRange.StartByte] = definitionFix{name: strings.Join(args, ".")}
	}
	return fixes
}
