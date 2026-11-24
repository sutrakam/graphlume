package mcp

import (
	"fmt"
	"strings"
	"unicode"
)

// The new name of a rename is checked before any plan is drawn.
//
// rename used the new name only to render the plan, so a name with a space,
// or a word the language reserves, came back as a plan of "proven" edits:
// every site proved by the compiler, ready to apply, for an edit that cannot
// compile. The binding is proven; the result is not. So the name is checked
// against the target's language first, and a name that cannot be an
// identifier there is refused with the reason, the way a name with a space is.

// reservedWords are the words each language reserves in every position: a
// definition can never be named by one. Contextual keywords (Go's predeclared
// identifiers, C#'s `value`, Kotlin's `data`) are left out on purpose, since
// the language accepts them as names.
var reservedWords = map[string]map[string]bool{
	"go": wordSet("break case chan const continue default defer else fallthrough for func go goto if " +
		"import interface map package range return select struct switch type var"),
	"java": wordSet("abstract assert boolean break byte case catch char class const continue default do double " +
		"else enum extends final finally float for goto if implements import instanceof int interface long native " +
		"new package private protected public return short static strictfp super switch synchronized this throw " +
		"throws transient try void volatile while true false null _"),
	"python": wordSet("False None True and as assert async await break class continue def del elif else except " +
		"finally for from global if import in is lambda nonlocal not or pass raise return try while with yield"),
	"javascript": wordSet("break case catch class const continue debugger default delete do else enum export extends " +
		"false finally for function if import in instanceof new null return super switch this throw true try typeof " +
		"var void while with yield let static implements interface package private protected public await"),
	"kotlin": wordSet("as break class continue do else false for fun if in interface is null object package return " +
		"super this throw true try typealias typeof val var when while"),
	"rust": wordSet("as break const continue crate else enum extern false fn for if impl in let loop match mod move " +
		"mut pub ref return self Self static struct super trait true type unsafe use where while async await dyn " +
		"abstract become box do final macro override priv typeof unsized virtual yield try"),
	"csharp": wordSet("abstract as base bool break byte case catch char checked class const continue decimal default " +
		"delegate do double else enum event explicit extern false finally fixed float for foreach goto if implicit in " +
		"int interface internal is lock long namespace new null object operator out override params private protected " +
		"public readonly ref return sbyte sealed short sizeof stackalloc static string struct switch this throw true " +
		"try typeof uint ulong unchecked unsafe ushort using virtual void volatile while"),
	"ruby": wordSet("BEGIN END alias and begin break case class def defined? do else elsif end ensure false for if in " +
		"module next nil not or redo rescue retry return self super then true undef unless until when while yield " +
		"__FILE__ __LINE__ __ENCODING__"),
	"php": wordSet("abstract and array as break callable case catch class clone const continue declare default do echo " +
		"else elseif empty enddeclare endfor endforeach endif endswitch endwhile eval exit extends final finally fn for " +
		"foreach function global goto if implements include instanceof insteadof interface isset list match namespace " +
		"new or print private protected public readonly require return static switch throw trait try unset use var " +
		"while xor yield"),
	"scala": wordSet("abstract case catch class def do else extends false final finally for forSome if implicit import " +
		"lazy match new null object override package private protected return sealed super this throw trait try true " +
		"type val var while with yield"),
	"swift": wordSet("associatedtype class deinit enum extension fileprivate func import init inout internal let open " +
		"operator private protocol public rethrows static struct subscript typealias var break case continue default " +
		"defer do else fallthrough for guard if in repeat return switch where while as Any catch false is nil super " +
		"self Self throw throws true try"),
	"c": wordSet("auto break case char const continue default do double else enum extern float for goto if inline int " +
		"long register restrict return short signed sizeof static struct switch typedef union unsigned void volatile " +
		"while _Bool _Complex _Imaginary"),
}

func init() {
	// TypeScript reserves JavaScript's words; its type names (string, number)
	// are legal identifiers and are not added.
	reservedWords["typescript"] = reservedWords["javascript"]
	cpp := map[string]bool{}
	for w := range reservedWords["c"] {
		cpp[w] = true
	}
	for _, w := range strings.Fields("alignas alignof and asm bool catch char16_t char32_t class const_cast constexpr " +
		"decltype delete dynamic_cast explicit export false friend mutable namespace new noexcept not nullptr operator " +
		"or private protected public reinterpret_cast static_assert static_cast template this thread_local throw true " +
		"try typeid typename using virtual wchar_t xor") {
		cpp[w] = true
	}
	reservedWords["cpp"] = cpp
}

// languageAlias maps the language names the index records to the tables above.
var languageAlias = map[string]string{
	"golang": "go", "js": "javascript", "jsx": "javascript", "mjs": "javascript", "ts": "typescript", "tsx": "typescript",
	"vue": "typescript", "py": "python", "kt": "kotlin", "kts": "kotlin", "rs": "rust", "c_sharp": "csharp", "cs": "csharp",
	"c++": "cpp", "cc": "cpp", "rb": "ruby",
}

func wordSet(words string) map[string]bool {
	set := make(map[string]bool)
	for _, w := range strings.Fields(words) {
		set[w] = true
	}
	return set
}

// checkNewName says why name cannot replace an identifier in language, or ""
// when it can. language is the index's name for the target's language; an
// unknown language is held to the identifier shape only.
func checkNewName(language, name string) string {
	if name == "" {
		return "the new name is empty"
	}
	lang := strings.ToLower(strings.TrimSpace(language))
	if a, ok := languageAlias[lang]; ok {
		lang = a
	}
	dollar := lang == "javascript" || lang == "typescript" || lang == "java" || lang == "kotlin" || lang == "scala"
	for i, r := range name {
		ok := r == '_' || unicode.IsLetter(r) || (dollar && r == '$') || (i > 0 && unicode.IsDigit(r))
		if lang == "ruby" && i == len(name)-1 && (r == '?' || r == '!') {
			ok = true // Ruby method names may end in ? or !
		}
		if !ok {
			if i == 0 && unicode.IsDigit(r) {
				return fmt.Sprintf("%q starts with a digit, which no identifier may", name)
			}
			return fmt.Sprintf("%q contains %q, which an identifier cannot hold", name, r)
		}
	}
	if reservedWords[lang][name] {
		return fmt.Sprintf("%q is a reserved word in %s and cannot name a definition", name, displayLanguage(lang))
	}
	return ""
}

func displayLanguage(lang string) string {
	switch lang {
	case "go":
		return "Go"
	case "javascript":
		return "JavaScript"
	case "typescript":
		return "TypeScript"
	case "csharp":
		return "C#"
	case "cpp":
		return "C++"
	case "php":
		return "PHP"
	}
	if lang == "" {
		return "this language"
	}
	return strings.ToUpper(lang[:1]) + lang[1:]
}
