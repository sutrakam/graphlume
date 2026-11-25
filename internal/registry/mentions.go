package registry

import (
	"path"
	"regexp"
	"strings"
)

// Line-level reading shared by the two text checks (a function passed as a
// value, a subtype the extractor missed). Everything here is one-sided like
// commentOnly: when a line is in doubt it is read as no evidence, so a check
// loses a mention and never invents one.

// importStarts open a statement that brings a name into scope without using
// it. `from` opens one only when the line also says `import` (Python), so a
// SQL or prose line that starts with the word is not taken for one.
var importStarts = []string{
	"import ", "use ", "using ", "#include", "#import", "require_relative", "require ",
	"pub use ", "pub(crate) use ", "pub(super) use ", "pub(in ", "extern crate ",
	"@import ", "@use ", "load(",
}

// reexportStarts open an `export { a, b } from './x'` statement, which is an
// import with a different first word. A local `export { a }` is not one.
var reexportStarts = []string{"export {", "export *", "export type {", "export type *"}

// bindingStarts open a declaration that may be a CommonJS import:
// `const isStream = require('./is-stream.js')`, `const { a, b } = require('x')`.
var bindingStarts = []string{"const ", "let ", "var ", "export const "}

// requireBinding matches the end of such a statement.
var requireBinding = regexp.MustCompile(`=\s*(?:await\s+)?(?:require|import)\s*\(`)

func startsImport(trimmed string) (isImport, conditional bool) {
	for _, p := range bindingStarts {
		if strings.HasPrefix(trimmed, p) {
			return true, true
		}
	}
	for _, p := range importStarts {
		if strings.HasPrefix(trimmed, p) {
			// `pub(in path) use` is rare; the prefix only needs to be a use.
			if p == "pub(in " && !strings.Contains(trimmed, ") use ") {
				continue
			}
			return true, false
		}
	}
	if strings.HasPrefix(trimmed, "from ") && strings.Contains(trimmed, " import") {
		return true, false
	}
	for _, p := range reexportStarts {
		if strings.HasPrefix(trimmed, p) {
			return true, true
		}
	}
	return false, false
}

// importLineSet returns the 1-based numbers of the lines of text that belong to
// an import, `use`, `require` or `include` statement, including the lines of a
// statement that spans several:
//
//	import {
//	  mergePath,
//	} from './url'
//
// A statement continues while a bracket it opened is still open, or its line
// ends in a backslash. The names listed inside are not uses of anything.
func importLineSet(text string) map[int]bool {
	lines := strings.Split(text, "\n")
	var out map[int]bool
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimLeft(lines[i], " \t")
		isImport, conditional := startsImport(trimmed)
		if !isImport {
			continue
		}
		end := i
		depth := bracketDelta(lines[i])
		for (depth > 0 || strings.HasSuffix(strings.TrimRight(lines[end], " \t\r"), "\\")) &&
			end+1 < len(lines) && end-i < 400 {
			end++
			depth += bracketDelta(lines[end])
		}
		if conditional {
			stmt := strings.Join(lines[i:end+1], " ")
			bound := strings.HasPrefix(trimmed, "export {") || strings.HasPrefix(trimmed, "export *") ||
				strings.HasPrefix(trimmed, "export type")
			if (bound && !strings.Contains(stmt, " from ")) || (!bound && !requireBinding.MatchString(stmt)) {
				i = end
				continue
			}
		}
		if out == nil {
			out = map[int]bool{}
		}
		for n := i; n <= end; n++ {
			out[n+1] = true
		}
		i = end
	}
	return out
}

// bracketDelta is how many more brackets of any kind a line opens than closes,
// ignoring the inside of string literals.
func bracketDelta(line string) int {
	var quote byte
	d := 0
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '{' || c == '(' || c == '[':
			d++
		case c == '}' || c == ')' || c == ']':
			d--
		}
	}
	return d
}

// identifierOffsets returns where name occurs as a whole identifier in line,
// outside string literals.
func identifierOffsets(line, name string) []int {
	var out []int
	var quote byte
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			switch c {
			case '\\':
				i++
			case quote:
				quote = 0
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case strings.HasPrefix(line[i:], name):
			end := i + len(name)
			if (i == 0 || !identByte(line[i-1])) && (end == len(line) || !identByte(line[end])) {
				out = append(out, i)
				i = end - 1
			}
		}
	}
	return out
}

// declaringKeyword matches the end of text that introduces a function: the
// keyword of its language, an optional receiver (Go's `func (s *T) name`) and
// an optional `self.` (Ruby's `def self.name`).
var declaringKeyword = regexp.MustCompile(`(?:\b(?:function|def|defp|defn|fn|func|fun|sub|proc|procedure|macro)\b\s*(?:\*\s*)?(?:self\.)?|\bfunc\s*\([^)]*\)\s*)$`)

// cDeclaredLanguages declare a function by writing its return type before it,
// with no keyword: `void jv_free(jv);`, `public abstract void run();`.
var cDeclaredLanguages = map[string]bool{
	"c": true, "cpp": true, "objc": true, "cuda": true, "arduino": true, "java": true, "c_sharp": true,
}

// statementWords begin a statement that USES what follows, so a name after one
// is a use and not the name of a declaration.
var statementWords = map[string]bool{
	"return": true, "else": true, "case": true, "throw": true, "new": true, "await": true, "yield": true,
	"typeof": true, "delete": true, "sizeof": true, "goto": true, "do": true, "in": true, "of": true,
	"if": true, "while": true, "for": true, "switch": true, "and": true, "or": true, "not": true,
	"co_return": true, "co_yield": true, "co_await": true, "assert": true, "defer": true, "go": true,
}

// headerExts are the files that declare what a source file of the same stem
// defines.
var headerExts = map[string]bool{".h": true, ".hpp": true, ".hh": true, ".hxx": true, ".h++": true, ".cuh": true, ".inl": true}

func isHeader(p string) bool { return headerExts[strings.ToLower(path.Ext(p))] }

func pathStem(p string) string {
	base := path.Base(p)
	return strings.TrimSuffix(base, path.Ext(base))
}

// declaresFunction reports whether every occurrence of name on line is the
// DECLARATION of a function so spelled and not a use of one: a `def`, `fn`,
// `func` or `function` line, a C or Java prototype (`void jv_free(jv);`), or a
// line in a header whose source file defines the name.
//
// The definition itself is excluded elsewhere, by the lines it spans. A
// prototype or an overload signature sits outside that span, and it is the
// function being declared, not something passing it around.
func declaresFunction(line, name, lang string, pairedHeader bool) bool {
	offsets := identifierOffsets(line, name)
	if len(offsets) == 0 {
		return false
	}
	for _, off := range offsets {
		prefix := line[:off]
		rest := strings.TrimLeft(line[off+len(name):], " \t")
		if declaringKeyword.MatchString(prefix) {
			continue
		}
		if !strings.HasPrefix(rest, "(") {
			return false
		}
		if cDeclaredLanguages[lang] && returnTypePrefix(prefix) {
			continue
		}
		if pairedHeader && pairedHeaderPrefix(prefix) {
			continue
		}
		return false
	}
	return true
}

// returnTypePrefix reports whether prefix reads as modifiers and a return type
// and nothing else: `static inline void`, `jv *`, `std::string`. A call has
// nothing before it, or an operator, a dot, a bracket or a statement word.
func returnTypePrefix(prefix string) bool {
	p := strings.TrimSpace(strings.ReplaceAll(prefix, `extern "C"`, ""))
	// A qualifier on the name itself (`Foo::`) belongs to the name.
	for strings.HasSuffix(p, "::") {
		p = strings.TrimSpace(strings.TrimSuffix(p, "::"))
		i := len(p)
		for i > 0 && identByte(p[i-1]) {
			i--
		}
		p = strings.TrimSpace(p[:i])
	}
	if p == "" {
		return false
	}
	if strings.Contains(strings.ReplaceAll(p, "::", ""), ":") {
		return false
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		if !identByte(c) && c != ' ' && c != '\t' && c != '*' && c != '&' && c != ':' && c != '<' && c != '>' && c != ',' {
			return false
		}
	}
	last := p
	if i := strings.LastIndexAny(p, " \t*&<>,"); i >= 0 {
		last = strings.TrimSpace(p[i+1:])
		if last == "" {
			return true
		}
	}
	return !statementWords[last]
}

// pairedHeaderPrefix is the looser reading used inside a header that pairs with
// the file defining the name: everything there that is not an assignment, a
// member access or a statement is the declaration of the thing the source file
// defines, whatever macros and attributes decorate it.
func pairedHeaderPrefix(prefix string) bool {
	if strings.ContainsAny(prefix, "=.") || strings.Contains(prefix, "->") || strings.Contains(prefix, "#define") {
		return false
	}
	fields := strings.Fields(prefix)
	if len(fields) == 0 {
		return false
	}
	return !statementWords[fields[len(fields)-1]]
}

// isJsxClosing reports whether name on line is inside a JSX/TSX closing element
// (e.g. </Button> or </MainNav>), which is the syntactic close of a rendered
// component and not a function passed as a value.
func isJsxClosing(line, name string) bool {
	idx := strings.Index(line, "</"+name)
	if idx < 0 {
		return false
	}
	rest := strings.TrimLeft(line[idx+2+len(name):], " \t")
	return strings.HasPrefix(rest, ">")
}
