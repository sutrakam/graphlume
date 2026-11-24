package langdetect

import (
	"regexp"
	"sync"
)

// The content rules, one ordered list per ambiguous extension.
//
// HOW A RULE LIST IS READ. The rules for an extension are tried top to bottom
// against the first HeadBytes of the file and the first that matches decides.
// A rule matches when any one of its markers is found. Order therefore carries
// meaning: a marker only one language can produce (`@implementation`,
// `:- module`, `<?hh`) is tested before one several share (a `%` comment,
// `module`). A rule may recognise a language there is no grammar for; the file
// is then skipped under its real name instead of being parsed as something
// else.
//
// WHERE THE RULES COME FROM. GitHub Linguist's heuristics.yml (MIT licence) is
// the public reference for telling these languages apart, and where a rule
// follows it the comment says "after Linguist". Nothing is copied wholesale:
// Linguist's patterns are Ruby regular expressions tuned to rank several
// hundred languages, and these are literal markers tuned to choose between the
// grammars compiled in here. Several are stricter, several look at more than
// one marker, and the `.h` neighbour rule has no counterpart there.
//
// EVERY EXPRESSION IS RE2 and runs over one line of at most HeadBytes, so a
// hostile file cannot make a rule slow; it can only make it answer.

// contentRule is one "if the head looks like this, the file is that" test.
type contentRule struct {
	language   string // grammar name, or "" when there is no grammar
	recognised string // what the content is when language is ""

	// markers: any one found is a match. also, when set, must match as well,
	// for languages whose single markers are each shared with something else.
	markers []marker
	also    []marker

	// The same two lists, arranged for searching. See markerSet.
	markerSet, alsoSet *markerSet
}

func (c *contentRule) match(s *source) bool {
	return c.markerSet.found(s) && (c.alsoSet == nil || c.alsoSet.found(s))
}

// extRule holds everything known about one ambiguous extension.
type extRule struct {
	content []contentRule

	// neighbours places a file by its directory when no content rule matched.
	neighbours func(Neighbours) (language string, ok bool)

	// noGrammar marks an extension the library maps to a language only some of
	// its files are. When no content rule matches, the file has no grammar at
	// all, which is what a plain-text file is.
	noGrammar bool

	// quiet marks an extension whose plain reading is safe when no rule
	// matched, so the file is not reported as ambiguous. A `.h` with no C++ or
	// Objective-C construct in it is, for the purpose of parsing it, C.
	quiet bool

	// stem lets an inner extension decide: Foo.cs.pp is C#.
	stem bool

	// prepare rewrites the head before the content rules see it. Only `.h`
	// uses it, to hide what a C compiler would not see: maskCplusplusGuards.
	prepare func(head []byte) []byte
}

// unsettled is the result when the content was not read or no rule matched.
func (r *extRule) unsettled(byName string) Result {
	switch {
	case r.noGrammar, byName == "":
		return Result{}
	case r.quiet:
		return Result{Language: byName, Method: ByExtension}
	default:
		return Result{Language: byName, Method: ByAmbiguousExtension}
	}
}

// names reports whether lang is one this extension can be, so that a `#!`
// line is trusted only when it agrees with a possible reading of the file.
func (r *extRule) names(lang string) bool {
	for i := range r.content {
		if r.content[i].language == lang {
			return true
		}
	}
	return false
}

func is(language string, markers ...marker) contentRule {
	return contentRule{language: language, markers: markers, markerSet: newMarkerSet(markers)}
}

// known recognises a language no grammar exists for.
func known(name string, markers ...marker) contentRule {
	return contentRule{recognised: name, markers: markers, markerSet: newMarkerSet(markers)}
}

func (c contentRule) and(markers ...marker) contentRule {
	c.also, c.alsoSet = markers, newMarkerSet(markers)
	return c
}

func join(lists ...[]marker) []marker {
	var out []marker
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

// objcMarkers: after Linguist's `objectivec` pattern, widened with the
// directives a header or a category file starts with.
func objcMarkers() []marker {
	return []marker{
		atWord("@interface"), atWord("@implementation"), atWord("@protocol"),
		atWord("@property"), atWord("@class"), atWord("@end"), atWord("@import"),
		atWord("@synthesize"), atWord("@dynamic"), atWord("@autoreleasepool"),
		atWord("@optional"), atWord("@required"),
		at("#import", `[ \t]+[<"]`), at("# import", `[ \t]+[<"]`),
		has("NS_ASSUME_NONNULL_BEGIN"), has("NS_ENUM("), has("NS_OPTIONS("),
	}
}

// cPreprocessor is a C preprocessor line, for extensions where nothing else
// that could be in the file starts a line with `#`.
func cPreprocessor() []marker {
	return []marker{at("#", `[ \t]*(include|define|ifdef|ifndef|if|pragma|endif)\b`)}
}

// objcPreprocessor is the same evidence for `.m` and `.mm`, held to the exact
// spelling of a directive. GNU Octave comments with `#`, so "# if the matrix
// is singular" and "# define the grid" are MATLAB-family prose, not C: the
// directive has to follow the `#` directly and be followed by what a real one
// is followed by.
func objcPreprocessor() []marker {
	return []marker{
		at("#include", `[ \t]*[<"]`),
		at("#define", `[ \t]+[A-Za-z_]\w*`),
		at("#ifdef", `[ \t]+[A-Za-z_]\w*[ \t]*$`),
		at("#ifndef", `[ \t]+[A-Za-z_]\w*[ \t]*$`),
		at("#pragma", `[ \t]+(mark|once|clang|GCC)\b`),
		at("#if", `[ \t]+(defined\b|!|[A-Z_][A-Z0-9_]*\b)`),
	}
}

// notACommentLine matches a line whose first character is not the `*` of a
// block comment's body or the `/` that opens a comment.
var notACommentLine = regexp.MustCompile(`\A[ \t]*[^*/ \t]`)

// cppMarkers: C++ in a header, after Linguist's `cpp` patterns. A class,
// namespace or template declaration, an access specifier, a C++-only standard
// header, a std:: name or a scope-resolved call.
//
// `extern "C"` under `#ifdef __cplusplus` is deliberately not a marker: it is
// how a C header says it may be included from C++. Nor is a test of
// `__cplusplus` itself, which Linguist does count: a C header tests it to
// choose between <cstdint> and <stdint.h>. Nor is a bare keyword such as
// `virtual`: "virtual address" in a comment made three C headers C++.
func cppMarkers() []marker {
	// `Name::method(`, but not on a line that is a comment: git's transport.h
	// documents a callback as "transport.c::transport_get_remote_refs()".
	scope := has("::", `~?[A-Za-z_]\w*[ \t]*\(`)
	scope.afterWord = true
	scope.line = notACommentLine
	std := has("std::", `\w`)
	std.wordBefore = true
	operator := has("operator", `[ \t]*(\(\)|\[\]|[-+*/%=<>!&|^~]+)[ \t]*\(`)
	operator.wordBefore = true

	return []marker{
		// `class Name {`, `class EXPORT_MACRO Name : public Base`, `class Name;`
		// At most two identifiers: "class of the device" in a comment that
		// does not start its lines with `*` is not a declaration.
		at("class", `[ \t]+[A-Za-z_]\w*([ \t]+[A-Za-z_]\w*)?[ \t]*(final[ \t]*)?([:{;]|$)`),
		has("enum class", `[ \t]+\w`),
		at("namespace", `([ \t]+[\w:]+)?[ \t]*(\{|$)`),
		at("inline namespace", `[ \t]`),
		at("using", `[ \t]+(namespace[ \t]+)?[\w:]+[ \t]*[;=]`),
		at("template", `[ \t]*<|[ \t]+(class|struct)[ \t]+\w+[ \t]*<`),
		at("public", `[ \t]*:[ \t]*$`), at("private", `[ \t]*:[ \t]*$`), at("protected", `[ \t]*:[ \t]*$`),
		at("constexpr", `[ \t]`),
		at("virtual", `[ \t]+[\w~:<>]`), at("explicit", `[ \t]+[\w~:<>]`),
		at("#", `[ \t]*include[ \t]*<(c[a-z]+|string|string_view|vector|map|set|list|array|memory|utility|functional|algorithm|iostream|ostream|istream|sstream|fstream|iosfwd|atomic|mutex|thread|chrono|optional|variant|tuple|type_traits|unordered_map|unordered_set|initializer_list|iterator|limits|numeric|deque|queue|stack|bitset|exception|stdexcept|span|new|typeinfo|condition_variable|future|random|regex|filesystem|concepts|compare)>`),
		std, scope, operator,
		word("nullptr"), word("noexcept"),
		word("static_cast", `[ \t]*<`), word("dynamic_cast", `[ \t]*<`),
		word("reinterpret_cast", `[ \t]*<`), word("const_cast", `[ \t]*<`),
		word("decltype", `[ \t]*\(`),
		has("__has_cpp_attribute"),
	}
}

// bitbakeMarkers: an inherit/require/include line (after Linguist), a quoted
// assignment to an upper-case variable, or a task body.
func bitbakeMarkers() []marker {
	const files = `[ \t]+[\w.${}/+-]+([ \t]+[\w.${}/+-]+)*[ \t]*$`
	return []marker{
		col0("inherit", files), col0("require", files), col0("include", files),
		onLine(`= "`, `[A-Z][A-Z0-9_]*([_:][\w${}+.-]+|\[\w+\])*[ \t]*(\?\?|\?|:|\+|\.)?=`),
		onLine(`() {`, `((fakeroot[ \t]+)?python[ \t]+)?[a-z_][\w:${}+.-]*[ \t]*\(\)[ \t]*\{`),
		col0("addtask "), col0("EXPORT_FUNCTIONS "), col0("BBCLASSEXTEND"),
	}
}

// pascalMarkers: after Linguist's `end.` test, widened with the section
// keywords that stand alone on a line in Pascal and nowhere else here.
func pascalMarkers() []marker {
	const routine = `[ \t]*[\w.+*/<>=-]+.*;[ \t]*$`
	return folded(
		at("unit", `[ \t]+[\w.]+[ \t]*;`), at("program", `[ \t]+\w+[ \t]*;`), at("library", `[ \t]+\w+[ \t]*;`),
		at("uses", `[ \t]*$|[ \t]+[\w., ]+[;,]?[ \t]*$`),
		at("interface", `[ \t]*$`), at("implementation", `[ \t]*$`),
		at("begin", `[ \t]*$`), at("end.", `[ \t]*$`), at("end;", `[ \t]*$`),
		at("{$", `[a-z]+`),
		at("procedure", routine), at("function", routine), at("operator", routine),
		at("constructor", routine), at("destructor", routine),
		at("var", `[ \t]*$`), at("type", `[ \t]*$`), at("const", `[ \t]*$`),
	)
}

func xmlStart() []marker {
	return []marker{opens("<?xml"), opens("<!DOCTYPE"), opens("<TS"), opens("<map")}
}

func hackHeader() []marker { return []marker{opens("<?hh")} }

func glslMarkers() []marker {
	return []marker{
		at("#version", `[ \t]+\d`), at("layout", `[ \t]*\(`), at("uniform", `[ \t]+\w`),
		at("void", `[ \t]+main[ \t]*\(`),
	}
}

var (
	rulesOnce sync.Once
	rules     map[string]*extRule
)

// extensionRules returns the rule table, compiled on first use: a command that
// never detects a language should not pay for its regular expressions.
func extensionRules() map[string]*extRule {
	rulesOnce.Do(func() { rules = buildRules() })
	return rules
}

func buildRules() map[string]*extRule {
	return map[string]*extRule{
		// .txt — a Vim help file, or plain text. The library says Vim help
		// for every one, so a repository's llms.txt, requirements.txt and
		// licence text were indexed as vimdoc. A help file announces itself
		// with a `*tag*` ending a line that starts with it or that is
		// right-aligned after two or more spaces, or with a `vim:` modeline
		// setting the help file type. Anything else has no grammar.
		".txt": {noGrammar: true, content: []contentRule{
			is("vimdoc",
				col0("*", `[^\s*|"]+\*[ \t\r]*$`),
				onLine("*", `.*\S[ \t]{2,}\*[^\s*|"]+\*[ \t\r]*$`),
				has("vim:", `.*\b(ft|filetype)=help\b`),
			),
		}},

		// .m — Objective-C, MATLAB, MUMPS, Mercury, Wolfram, Limbo, MUF.
		// The library says MATLAB for all of them. Order after Linguist:
		// languages with an unmistakable directive first, MATLAB's `%` comment
		// late because a MUMPS routine may be named with a leading `%`.
		".m": {content: []contentRule{
			is("objc", join(objcMarkers(), objcPreprocessor())...),
			known("mercury", at(":-", `[ \t]*(module|interface|implementation|import_module|use_module|pred|func|type|mode|inst|pragma)\b`)),
			known("limbo", col0("implement", `[ \t]+\w+[ \t]*;`), onLine("module", `\w+[ \t]*:[ \t]*module\b`)),
			// A MUMPS comment starts with `;`, a routine starts with a label
			// followed by one, and an indented `set`, `write`, `new`, `kill`
			// or `quit` followed by a space is a command. MATLAB never begins
			// a line with `;` and calls with parentheses.
			known("mumps",
				at(";"),
				onLine(" ;", `[%A-Za-z][A-Za-z0-9]*(\([^)]*\))?[ \t]+;`),
				onLine("\t;", `[%A-Za-z][A-Za-z0-9]*(\([^)]*\))?[ \t]+;`),
				onLine("set ", `[ \t]+set(:\S+)?[ \t]+[\w^$%!"(']`),
				onLine("write ", `[ \t]+write(:\S+)?[ \t]+[\w^$%!"(']`),
				onLine("new ", `[ \t]+new(:\S+)?[ \t]+[\w^$%!"(']`),
				onLine("kill ", `[ \t]+kill(:\S+)?[ \t]+[\w^$%!"(']`),
				onLine("quit ", `[ \t]+quit(:\S+)?[ \t]+[\w^$%!"(']`),
			),
			is("wolfram", has("(*")).and(has("*)")),
			is("wolfram", at("BeginPackage["), at("Begin["), at("Needs["), at("Notebook[")),
			is("matlab", atWord("function"), atWord("classdef"), at("%")),
			known("muf", col0(": ")),
			// A MATLAB script with no comment and no function: assignments
			// ended by a semicolon, or a block closed by a bare `end`.
			is("matlab",
				onLine(";", `[ \t]*[A-Za-z]\w*(\([^)]*\))?[ \t]*=[^=].*;[ \t]*$`),
				at("end", `[ \t]*;?[ \t]*$`),
			),
		}},

		// .mm — Objective-C++, or a FreeMind map. The library says XML.
		".mm": {content: []contentRule{
			is("xml", xmlStart()...),
			is("objc", join(objcMarkers(), objcPreprocessor())...),
		}},

		// .h — C, C++ or Objective-C. The library says C.
		//
		// Content first, read as a C compiler would read it: what sits inside
		// an `#ifdef __cplusplus` block is hidden (see maskCplusplusGuards).
		// Then the directory: a header beside C++ sources and no C source is
		// C++, one beside Objective-C sources and nothing else is Objective-C.
		// A directory holding both C and C++ sources says nothing, and the
		// header stays C. A header no rule claims is C and is not reported as
		// ambiguous, because one with no C++ or Objective-C construct in it
		// parses the same under all three grammars.
		".h": {
			prepare: maskCplusplusGuards,
			content: []contentRule{
				is("objc", objcMarkers()...),
				is("cpp", cppMarkers()...),
			},
			neighbours: func(n Neighbours) (string, bool) {
				switch {
				case n.CPP && !n.C:
					return "cpp", true
				case n.ObjC && !n.C && !n.CPP:
					return "objc", true
				}
				return "", false
			},
			quiet: true,
		},

		// .hh — C++ or Hack. The library says C++, and Hack parsed as C++
		// produced a function named `if`. After Linguist: `<?hh` is Hack.
		".hh": {content: []contentRule{
			is("hack", hackHeader()...),
			is("cpp", join(cppMarkers(), cPreprocessor())...),
			// A header-less Hack file still declares PHP-style functions over
			// `$` variables.
			is("hack", at("function", `[ \t]+\w+[ \t]*[(<]`), has(" function ", `\w+[ \t]*[(<]`)).and(has("$", `\w`)),
		}},

		// .php — PHP, or Hack written before the .hack extension existed.
		".php": {content: []contentRule{is("hack", hackHeader()...)}, quiet: true},

		// .fs — F#, Forth, a GLSL fragment shader, or RenderScript's
		// Filterscript. The library says Forth, which has no definition query,
		// so every F# file was skipped. Order after Linguist; a Forth colon
		// definition is tested in the first column only, because F# indents a
		// type annotation the same way.
		".fs": {content: []contentRule{
			known("filterscript", at("#pragma", `[ \t]+(rs|version)\b`), has("__attribute__((kernel))")),
			is("glsl", join(glslMarkers(), []marker{
				at("precision", `[ \t]+(lowp|mediump|highp)[ \t]`), at("varying", `[ \t]+\w`), at("attribute", `[ \t]+\w`),
				at("in", `[ \t]+(vec[234]|mat[234]|float|int)[ \t]`), at("out", `[ \t]+(vec[234]|mat[234]|float|int)[ \t]`),
			})...),
			is("forth",
				col0(": ", `\S+`), col0(`\ `), col0("( ", `[^)]*\)`),
				onLine(" constant ", `[0-9a-fA-F$]+ constant \S`), onLine(" value ", `[0-9a-fA-F$]+ value \S`),
				col0("variable "), col0("create "), col0("require "), col0("include "), col0("new-device"), col0(`s" `),
			),
			is("fsharp",
				atWord("#light"),
				at("namespace", `[ \t]+(rec[ \t]+)?[\w.]+`),
				at("module", `[ \t]+((rec|internal|private|public)[ \t]+)*[\w.]+`),
				at("open", `[ \t]+[\w.]+`),
				at("let", `[ \t]+((rec|inline|mutable|private|internal|public)[ \t]+)*[\w(]`),
				at("type", `[ \t]+((internal|private|public)[ \t]+)?\w`),
				at("[<", `\w+`),
			),
		}},

		// .pp — a Puppet manifest or Free Pascal source. The library says
		// Pascal, which has no definition query, so every manifest was
		// skipped. Pascal's markers are tested first because they cannot occur
		// in a manifest; a hash rocket, a `$variable` assignment or a resource
		// declaration is Puppet (after Linguist).
		".pp": {stem: true, content: []contentRule{
			is("pascal", pascalMarkers()...),
			is("puppet",
				at("class", `[ \t]+[a-z][\w:]*[ \t]*(\(|\{|inherits\b|$)`),
				at("define", `[ \t]+[a-z][\w:]*`),
				at("node", `[ \t]+(default\b|['"/\w])`),
				at("include", `[ \t]+[\w:]+[ \t]*$`), at("contain", `[ \t]+[\w:]+[ \t]*$`), at("require", `[ \t]+[\w:]+[ \t]*$`),
				has(" => "),
				at("$", `[\w:]+[ \t]*=`),
				onLine("{", `[ \t]*[a-z][\w:]*[ \t]*\{[ \t]*(['"$\[]|$)`),
				at("hiera_include("), at("hiera("), at("create_resources("), at("notice("), at("fail("),
			),
		}},

		// .v — V, Verilog or Coq/Rocq. The library says V; Verilog and Coq
		// parsed as V yielded nothing and a 243 KB proof took 2.5 seconds.
		// After Linguist: `Proof.`/`Qed.` and `Require Import` are Coq, a
		// backtick directive or `endmodule` is Verilog, `fn` and `:=` are V.
		// V is tested before Verilog's port keywords because a V struct may
		// well have a field named `input`.
		".v": {content: []contentRule{
			known("coq",
				word("Proof.", `(\s|$)`), word("Qed.", `(\s|$)`), word("Defined.", `(\s|$)`), word("Admitted.", `(\s|$)`),
				at("Require", `([ \t]+(Import|Export))?[ \t]+\w`), at("From", `[ \t]+\S+[ \t]+Require\b`),
				atWord("Theorem"), atWord("Lemma"), atWord("Definition"), atWord("Inductive"),
				atWord("CoInductive"), atWord("Fixpoint"), atWord("Ltac"), atWord("Notation"),
				atWord("Axiom"), atWord("Corollary"), atWord("Proposition"), atWord("Hypothesis"),
				at("Set Implicit"), at("Open Scope"), at("Module", `[ \t]+\w+[ \t]*\.`),
			),
			is("verilog",
				atWord("endmodule"), at("always", `[ \t]*@`), atWord("always_ff"), atWord("always_comb"),
				atWord("always_latch"), at("initial", `[ \t]+begin\b`), at("assign", `[ \t]+\w`),
				atWord("`timescale"), atWord("`define"), atWord("`include"), atWord("`ifdef"),
				atWord("`ifndef"), atWord("`default_nettype"), atWord("`pragma"),
			),
			is("v",
				at("fn", `[ \t]+(\([^)]*\)[ \t]*)?\w+[ \t]*[(<\[]`),
				at("pub fn", `[ \t]`),
				at("import", `[ \t]+[\w.]+[ \t]*(\{.*)?$`),
				at("struct", `[ \t]+\w+[ \t]*\{`), at("pub struct", `[ \t]+\w+[ \t]*\{`),
				at("enum", `[ \t]+\w+[ \t]*\{`), at("pub enum", `[ \t]+\w+[ \t]*\{`),
				at("interface", `[ \t]+\w+[ \t]*\{`),
				at("mut", `[ \t]+\w`),
				has(" := "), has("$if "), has("$else "),
			),
			is("verilog",
				at("module", `[ \t]+\w+[ \t]*(#[ \t]*)?[(;]`),
				at("input", `[ \t]+(\[|\w)`), at("output", `[ \t]+(\[|\w)`), at("inout", `[ \t]+(\[|\w)`),
				at("wire", `[ \t]+(\[|\w)`), at("reg", `[ \t]+(\[|\w)`),
				at("parameter", `[ \t]+(\[|\w)`), at("localparam", `[ \t]+(\[|\w)`),
			),
		}},

		// .inc — an include file in whatever the project is written in. The
		// library says Pascal. Only the readings a grammar exists for, or that
		// would otherwise be misread as one, are told apart.
		".inc": {content: []contentRule{
			is("php", opens("<?php"), opens("<?=")),
			is("html", folded(opens("<!doctype html"), opens("<html"), opens("<head"), opens("<body"),
				opens("<div"), opens("<ul"), opens("<ol"), opens("<table"), opens("<p>"), opens("</"))...),
			known("pov-ray", at("#declare", `[ \t]`), at("#local", `[ \t]`), at("#macro", `[ \t]`), at("#version", `[ \t]`)),
			known("pawn",
				at("native", `[ \t]+[\w:\[\]]+.*[;({]`), at("stock", `[ \t]+[\w:\[\]]+.*[;({]`),
				at("forward", `[ \t]+[\w:\[\]]+.*[;({]`), at("public", `[ \t]+[\w:\[\]]+.*[;({]`),
				at("methodmap", `[ \t]+\w`), atWord("#endinput"), atWord("#tryinclude"),
				at("#include", `[ \t]+<a_samp>`),
			),
			known("nasl",
				at("include", `[ \t]*\([ \t]*['"][\w./-]+['"][ \t]*\)[ \t]*;`),
				at("global_var", `[ \t]`), at("local_var", `[ \t]`),
			),
			is("bitbake", bitbakeMarkers()...),
			is("sql", folded(
				at("select", `[ \t]+[\w*'"@]`), at("insert", `[ \t]+into[ \t]`),
				at("update", `[ \t]+\w+[ \t]+set[ \t]`), at("delete", `[ \t]+from[ \t]`),
				at("create", `[ \t]+(or[ \t]+replace[ \t]+)?(table|view|index|procedure|function|trigger|database)[ \t]`),
				at("alter", `[ \t]+table[ \t]`), at("drop", `[ \t]+(table|view|index|procedure)[ \t]`),
				at("flush", `[ \t]+\w+[ \t]*;`), at("truncate", `[ \t]+table[ \t]`),
			)...),
			is("pascal", pascalMarkers()...),
			is("cpp", cppMarkers()...),
			is("c", at("#", `[ \t]*(include[ \t]*[<"]|define[ \t]+\w|ifdef[ \t]+\w|ifndef[ \t]+\w|pragma[ \t]+\w)`)),
			is("asm", folded(
				at("mov", `[ \t]+\S+,`), at("push", `[ \t]+\w+[ \t]*(;.*)?$`), at("jmp", `[ \t]+\w`),
				at("db", `[ \t]+[\w'"]`), atWord("%define"), atWord("%macro"), has(" equ "),
			)...),
		}},

		// .bb — a BitBake recipe, a Babashka (Clojure) script or BlitzBasic.
		// The library says BitBake. After Linguist.
		".bb": {content: []contentRule{
			is("clojure",
				at("(ns "), at("(def"), at("(let "), at("(require"), at("(import"),
				at("(println"), at("(when "), at("(doseq"),
			),
			is("bitbake", bitbakeMarkers()...),
			known("blitzbasic", folded(
				at("end function"), at("function", `[ \t]+\w+`), at("type", `[ \t]+\w+[ \t]*$`), at("end type"),
				at("local", `[ \t]+\w`), at("global", `[ \t]+\w`), at("field", `[ \t]+\w`),
				at("print", `[ \t]`), at("graphics", `[ \t]+\d`),
			)...),
		}},

		// .gs — Google Apps Script (JavaScript), a GLSL geometry shader, Gosu
		// or Genie. The library says JavaScript. After Linguist.
		".gs": {content: []contentRule{
			is("glsl", join(glslMarkers(), []marker{word("EmitVertex"), word("EndPrimitive"), word("gl_Position")})...),
			known("gosu", col0("uses", `[ \t]+[\w.*]+[ \t]*$`), col0("package", `[ \t]+[\w.]+[ \t]*$`)),
			known("genie",
				col0("[indent=", `\d+\]`), col0("init", `[ \t]*$`), col0("uses", `[ \t]*$`),
				at("def", `[ \t]+\w`), at("construct", `[ \t]*\(`), at("prop", `[ \t]+\w`),
			),
			is("javascript",
				at("function", `[ \t]+\w+[ \t]*\(`),
				at("var", `[ \t]+\w+[ \t]*=`), at("let", `[ \t]+\w+[ \t]*=`), at("const", `[ \t]+\w+[ \t]*=`),
				has("= function"),
			),
		}},

		// .tl — Teal, or Telegram's Type Language. The library says Teal, and
		// a 74 KB schema parsed as Teal took 1.4 seconds. A TL schema is a list
		// of `name#id field:type = Type;` combinators, tested first because
		// its `---functions---` divider starts like a Teal comment; Teal has
		// Lua's keywords and `--` comments (after Linguist).
		".tl": {content: []contentRule{
			known("type-language",
				col0("---functions---"), col0("---types---"),
				onLine("#", `[a-z][\w.]*#[0-9a-f]{1,8}[ \t]`),
				onLine("? =", `[a-z][\w.]*[ \t]+\?[ \t]*=[ \t]*\w+;`),
			),
			is("teal",
				at("--"), at("local", `[ \t]+\w`), at("global", `[ \t]+\w`),
				at("function", `[ \t]+[\w.:]+[ \t]*\(`), atWord("return"), at("end", `[ \t]*$`),
				at("record", `[ \t]+\w`), at("enum", `[ \t]+\w`), at("interface", `[ \t]+\w`),
			),
		}},

		// .es — an Erlang escript or ECMAScript. The library says JavaScript.
		// After Linguist.
		".es": {content: []contentRule{
			is("erlang",
				at("%"), at("-module("), at("-export("), at("-include"), at("-mode("),
				at("main", `[ \t]*\(.*\)[ \t]*->`),
			),
			is("javascript",
				atWord("import"), atWord("export"),
				at("const", `[ \t]+\w`), at("let", `[ \t]+\w`), at("var", `[ \t]+\w`),
				at("function", `[ \t]+\w`), at("class", `[ \t]+\w`),
				at("//"), at("/*"), has("module.exports"), has(`'use strict'`), has(`"use strict"`),
			),
		}},

		// .pl — Perl, Prolog or Raku. The library says Perl. After Linguist,
		// with Perl's unmistakable openers tested before Prolog's `:-`.
		".pl": {content: []contentRule{
			known("raku", at("use v6"), at("unit module"), at("unit class"), at("my class")),
			is("perl",
				at("use", `[ \t]+(strict|warnings|v?5[\d._]*|[A-Z][\w:]*)\b`),
				at("package", `[ \t]+[\w:]+[ \t]*[;{]`),
				at("sub", `[ \t]+\w+`),
				at("my", `[ \t]*[($@%]`), at("our", `[ \t]*[($@%]`), at("local", `[ \t]*[($@%]`),
				at("require", `[ \t]+[\w:]+[ \t]*;`),
				col0("__END__"), col0("=pod"), col0("=head1"), col0("=cut"),
			),
			is("prolog",
				at(":-", `[ \t]*\w`),
				onLine(":-", `[a-z]\w*(\(.*\))?[ \t]*:-`),
				onLine(").", `[a-z]\w*\(.*\)\.[ \t]*(%.*)?$`),
			),
			is("perl", at("$", `\w+[ \t]*=`), at("@", `\w+[ \t]*=`), at("print", `[ \t]`)),
		}},

		// .ts — TypeScript, or a Qt Linguist translation file. After Linguist.
		".ts": {content: []contentRule{is("xml", xmlStart()...)}, quiet: true},

		// .tsx — TSX, or a Tiled tileset.
		".tsx": {content: []contentRule{is("xml", xmlStart()...)}, quiet: true},

		// .mat — MATLAB data is binary and never reaches a grammar; a text
		// .mat is almost always a Unity material, which is YAML.
		".mat": {content: []contentRule{is("yaml", opens("%YAML"), opens("--- !u!"))}},

		// .pro — Prolog, but also Android ProGuard rules (`proguard-rules.pro`) and
		// Qt qmake project files. The library says Prolog, which reads a list of
		// `-keep class ...` rules as thirty-six syntax errors and defines nothing.
		".pro": {content: []contentRule{
			known("proguard",
				at("-keep"), at("-dontwarn"), at("-dontobfuscate"), at("-dontoptimize"),
				at("-dontshrink"), at("-assumenosideeffects"), at("-optimizations"),
				at("-printmapping"), at("-adaptresourcefilenames"), at("-ignorewarnings"),
				at("-allowaccessmodification"), at("-renamesourcefileattribute"),
				at("-repackageclasses"), at("-injars"), at("-libraryjars"),
			),
			known("qmake",
				col0("TEMPLATE"), col0("SOURCES"), col0("HEADERS"), col0("TARGET"),
				col0("CONFIG"), col0("QT"),
			),
			is("prolog",
				at(":-", `[ \t]*\w`),
				onLine(":-", `[a-z]\w*(\(.*\))?[ \t]*:-`),
				onLine(").", `[a-z]\w*\(.*\)\.[ \t]*(%.*)?$`),
			),
		}},

		// .rd — R documentation, which the library reads as R source. It is a
		// LaTeX-like markup with no R definitions in it.
		".rd": {content: []contentRule{
			known("rd",
				col0(`\name{`), col0(`\alias{`), col0(`\title{`), col0(`\docType{`),
				col0(`\description{`), col0(`\usage{`), col0(`\arguments{`),
				col0("% Generated by roxygen2"),
			),
		}},

		// .re — Reason, or a re2c scanner written in C++. The library says
		// C++. After Linguist.
		".re": {content: []contentRule{
			is("cpp",
				at("#", `[ \t]*(if|ifdef|ifndef|define|pragma|include)\b`),
				at("template", `[ \t]*<`), has("/*!re2c"),
			),
			known("reason",
				at("module", `[ \t]+(type[ \t]+)?\w+`),
				at("include", `[ \t]+\w+[ \t]*;`), at("open", `[ \t]+\w+[ \t]*;`),
				at("let", `[ \t]+(rec[ \t]+)?\w+`), at("type", `[ \t]+\w+`), at("switch", `[ \t]*\(`),
			),
		}},

		// .lib — a Faust library among static libraries, Liberty timing files
		// and much else. The library has no entry; only Faust is claimed.
		".lib": {content: []contentRule{
			is("faust",
				at("import", `[ \t]*\([ \t]*"[\w./-]+\.lib"[ \t]*\)`),
				at("declare", `[ \t]+\w+[ \t]+"`),
				has(`library("`),
			),
		}},
	}
}
