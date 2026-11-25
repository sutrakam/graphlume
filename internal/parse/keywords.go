package parse

import "strings"

// Keyword names are never definitions.
//
// A grammar that meets text it cannot parse recovers by guessing, and the guess
// is sometimes a function declarator whose "name" is the keyword in front of a
// parenthesis: `if (x) {` read as a function called `if`. Measured on projects
// the parser was not tuned on, C# produced methods named `if` and `return`
// (serilog) and C produced functions named `if` and `switch` (jq). No program
// declares such a name, and a symbol for one is noise that a lookup by name can
// return and a call can bind to.
//
// The sets hold the control-flow and declaration keywords that the language
// reserves in every context, so the language can never use one as the name of a
// definition. They are deliberately short and certain. A word that is a keyword
// only in some positions (C# `get`, `value`, `var`; Kotlin `data`, `inner`), or
// that the language allows as a member name (JavaScript and TypeScript accept
// `delete`, `catch` or `default` as a method name, Ruby `end` and `class`), is
// NOT listed: dropping a real method is worse than keeping a recovery artefact.
//
// JavaScript, TypeScript, Ruby and the languages not listed have no set; the
// guard is a no-op for them.
var keywordNames = map[string]map[string]bool{
	"c": keywordSet("if else for while do switch case return goto break continue default sizeof"),
	"cpp": keywordSet("if else for while do switch case return goto break continue default sizeof " +
		"try catch throw new delete namespace template typename"),
	"c_sharp": keywordSet("if else for foreach while do switch case return goto break continue default " +
		"try catch finally throw new lock using fixed checked unchecked sizeof typeof"),
	"java": keywordSet("if else for while do switch case return break continue default " +
		"try catch finally throw new synchronized assert"),
	"go":     keywordSet("if else for switch select case return goto break continue defer go range func"),
	"rust":   keywordSet("if else for while loop match return break continue fn let mod impl struct enum trait where"),
	"python": keywordSet("if elif else for while try except finally with return def class lambda import from pass raise yield assert"),
	"kotlin": keywordSet("if else for while do when return break continue try catch throw fun class object interface"),
	"scala":  keywordSet("if else for while do match case return try catch finally throw new def class object trait"),
	// make: the directives of GNU make and of automake's Makefile.am, which
	// reads `if ENABLE_X` ... `endif` as a conditional. jq's Makefile.am had two
	// symbols named `if`. A target or variable cannot be named by a directive.
	"make": keywordSet("if else endif ifeq ifneq ifdef ifndef include define endef export unexport override vpath"),
}

func keywordSet(words string) map[string]bool {
	set := make(map[string]bool)
	for _, w := range strings.Fields(words) {
		set[w] = true
	}
	return set
}

// isKeywordName reports whether name is a reserved word of the language and so
// cannot be the name of a definition there.
func isKeywordName(language, name string) bool {
	return keywordNames[language][name]
}
