package parse

import (
	"bytes"
	"strings"
)

// Kernel-style C for the guarded macro mask (cmacro.go): what the Linux kernel
// writes and any C codebase with its own annotation macros writes too. Only C
// is touched; C++ and Objective-C keep the rules in cmacro.go exactly as they
// were measured.
//
// Each rule produces macroCandidates, and a candidate is only ever tried, never
// trusted: the file is parsed once more and the masked text is kept only if the
// guard (maskGuard) says it has no more errors, loses no definition and gains
// one or has fewer errors. What is new here is shapes, not names:
//
//   - An attribute word between a type and a declarator name, or in front of
//     the type: `static int __init f(void)`, `void __iomem *base`,
//     `asmlinkage void __noreturn die(void)`. An identifier that starts with
//     two underscores and sits between two other words of a declaration can
//     only be an annotation, because a declaration has one name and one type.
//     A word that starts a declaration is an annotation when what follows it
//     is a complete declaration on its own (`__init int f(`, `__iomem void *x`)
//     and a type when it is not (`__u32 x`, `__be32 *p`).
//   - An annotation after a declarator: `int x __initdata = 3;`,
//     `void f(void) __releases(lock) {`, `struct s { } __packed;`. After `)`
//     or `]` any double-underscore word qualifies. After a plain name a short
//     list does, because `T a __b;` could also declare `__b`.
//   - A type written where an expression is expected, as the argument of a
//     macro call: `container_of(p, struct foo, m)`, `kzalloc_obj(struct foo)`,
//     `DEFINE_PER_CPU(unsigned long, count)`. The whole type is overwritten
//     with underscores, which is an identifier and so an expression and also a
//     parameter of a prototype.
//   - A function written as a macro call and a block at file scope:
//     `SYSCALL_DEFINE3(read, int, fd, char __user *, buf, size_t, n) { ... }`,
//     `BPF_CALL_2(name, ...) { ... }`. The head is rewritten, in the same number
//     of bytes, to `void name(` and the parameter pairs to parameters, so that
//     the grammar sees a function definition. The name is the first argument
//     (what the macro defines), except for the system call macros, whose
//     function is `sys_` + the first argument (`compat_sys_` for the compat
//     form): that is the symbol the kernel exports and the one callers, tables
//     and documentation write, and `read` alone is the name of hundreds of
//     unrelated driver functions.
//
// A short list of annotation words that have no underscores (asmlinkage,
// notrace, noinline, noinstr) is kept because the shape cannot tell them from
// a type; the survey in docs shows which are common.

// cPlainAttrs are annotation words without a leading underscore pair.
var cPlainAttrs = map[string]bool{
	"asmlinkage": true, "notrace": true, "noinline": true, "noinstr": true, "nokprobe_inline": true,
}

// cPostAttrs are the annotations that follow a plain declarator name. After a
// `)` or a `]` any attribute-shaped word qualifies and this list is not used.
var cPostAttrs = map[string]bool{
	"__initdata": true, "__initconst": true, "__exitdata": true, "__read_mostly": true,
	"__ro_after_init": true, "__cacheline_aligned": true, "__aligned": true, "__section": true,
	"__packed": true, "__used": true, "__maybe_unused": true, "__counted_by": true,
	"__weak": true, "__alias": true, "__visible": true, "__percpu": true, "__rcu": true,
}

// notAttr are double-underscore words that are keywords or builtins of the
// compilers, never annotation macros.
var notAttr = map[string]bool{
	"__attribute__": true, "__attribute": true, "__asm__": true, "__asm": true, "__extension__": true,
	"__typeof__": true, "__typeof": true, "__int128": true, "__label__": true, "__alignof__": true,
	"__alignof": true, "__declspec": true, "__thread": true, "__restrict": true, "__restrict__": true,
	"__volatile__": true, "__inline": true, "__inline__": true, "__signed__": true,
}

// typeStop are words after which a following word is a tag, a type or a
// statement, not a declaration name.
var typeStop = map[string]bool{
	"const": true, "volatile": true, "static": true, "extern": true, "register": true, "inline": true,
	"struct": true, "union": true, "enum": true, "typedef": true, "return": true, "else": true,
	"goto": true, "case": true, "sizeof": true, "restrict": true, "auto": true, "do": true,
	"unsigned": true, "signed": true, "default": true, "typeof": true, "_Alignof": true,
	"_Atomic": true, "_Noreturn": true, "_Thread_local": true,
}

// cTypeWords may begin a type written in a macro's argument list.
var cTypeWords = map[string]bool{
	"unsigned": true, "signed": true, "short": true, "long": true, "int": true, "char": true,
	"void": true, "float": true, "double": true, "_Bool": true,
}

// callNotCall are the words in front of `(` that do not call anything.
var callNotCall = map[string]bool{
	"if": true, "while": true, "for": true, "switch": true, "return": true, "sizeof": true,
	"typeof": true, "__typeof__": true, "__typeof": true, "_Alignof": true, "alignof": true,
	"__alignof__": true, "case": true, "else": true, "do": true, "defined": true,
	"__attribute__": true, "__attribute": true, "asm": true, "__asm__": true, "__asm": true,
	"_Generic": true, "_Static_assert": true, "static_assert": true, "volatile": true,
}

// statementMacroBlock handles a statement macro that takes a block, inside a
// function:
//
//	list_for_each_entry(pos, head, node) {      for_each_subsys(ss, id) {
//
// The grammar reads the call, supplies the `;` it lacks and goes on, which is
// right until the block opens with a declaration: then it reads the block as
// the body of a function and loses what follows. The space in front of the `{`
// becomes the `;`, which is the reading the grammar had already chosen, now
// without the guess. Only a call in the position of a statement qualifies.
func (s *cScan) statementMacroBlock(i int) (macroCandidate, bool) {
	after := s.skipParens(i + 1)
	if after < 0 || !s.punct(after, '{') {
		return macroCandidate{}, false
	}
	if i > 0 {
		p := s.toks[i-1]
		ok := p.kind == ctDir || p.kind == ctPunct && (p.c == ';' || p.c == '{' || p.c == '}' || p.c == ':') || s.word(i-1) == "else"
		if !ok {
			return macroCandidate{}, false
		}
	}
	gap := s.src[s.toks[after-1].end:s.toks[after].start]
	for k, b := range gap {
		if b == ' ' || b == '\t' {
			at := s.toks[after-1].end + k
			return macroCandidate{start: at, end: at + 1, name: s.word(i), expect: -1, repl: []byte{';'}}, true
		}
	}
	return macroCandidate{}, false
}

// storageBeforeMacro handles a declaration macro with storage words in front
// of it at file scope:
//
//	static DEFINE_MUTEX(lock);       static LIST_HEAD(devices);
//	static DEFINE_PER_CPU(int, n);   extern DECLARE_BITMAP(map, 64);
//
// The grammar reads `NAME(args);` without the storage words as an expression
// statement and with them as an error. The storage words are overwritten, which
// leaves the form that is read cleanly. The macro defines a variable, which the
// definition query does not record either way.
func (s *cScan) storageBeforeMacro(i int) []macroCandidate {
	after := s.skipParens(i + 1)
	if after < 0 || !s.punct(after, ';') && !s.punct(after, '=') {
		return nil
	}
	var out []macroCandidate
	j := i - 1
	for j >= 0 && s.toks[j].kind == ctIdent && cStorage[s.word(j)] {
		out = append(out, macroCandidate{start: s.toks[j].start, end: s.toks[j].end, name: s.word(j), expect: -1})
		j--
	}
	if len(out) == 0 || j >= 0 && s.toks[j].kind != ctDir && !s.punct(j, ';') && !s.punct(j, '}') {
		return nil
	}
	return out
}

// isGluedNumber reports whether a number-shaped token is really a name that
// starts with digits: a run of digits followed by letters or underscores that
// are no suffix of a number (`10000baseT_Full`, `4way_assoc`). Hex and binary
// literals, integer and float suffixes and exponents are numbers.
func isGluedNumber(tx string) bool {
	if len(tx) < 3 || tx[0] < '0' || tx[0] > '9' {
		return false
	}
	if tx[0] == '0' && (tx[1] == 'x' || tx[1] == 'X' || tx[1] == 'b' || tx[1] == 'B') {
		return false
	}
	i := 0
	for i < len(tx) && tx[i] >= '0' && tx[i] <= '9' {
		i++
	}
	rest := strings.ToLower(tx[i:])
	if rest == "" || rest[0] == '.' {
		return false
	}
	switch rest {
	case "u", "l", "ul", "lu", "ll", "ull", "llu", "f", "z", "uz", "zu":
		return false
	}
	if rest[0] == 'e' && len(rest) > 1 {
		ok := true
		for _, c := range rest[1:] {
			if c < '0' || c > '9' {
				ok = false
			}
		}
		if ok {
			return false
		}
	}
	return strings.ContainsAny(rest, "_") || len(rest) >= 3
}

// isDefineDirective reports whether a directive is a #define or #undef.
func isDefineDirective(d []byte) bool {
	d = bytes.TrimLeft(d[1:], " \t")
	return bytes.HasPrefix(d, []byte("define")) || bytes.HasPrefix(d, []byte("undef")) || bytes.HasPrefix(d, []byte("include"))
}

// isString reports whether token i is a string literal.
func (s *cScan) isString(i int) bool {
	if i < 0 || i >= len(s.toks) || s.toks[i].kind != ctOther {
		return false
	}
	b := s.src[s.toks[i].start]
	return b == '"' || b == 'L' || b == 'u' || b == 'U'
}

// stringMacro handles a macro that stands for a string, written next to string
// literals the way the kernel writes its asm templates and log formats:
//
//	"1:\n" EX_TABLE_INSN(0b, 1b)       printk(KERN_INFO "up\n")
//	pr_info("limit " __stringify(MAX) " bytes")
//
// A string literal followed by a word is never valid C (adjacent literals
// join, and nothing else follows one), and neither is an all-caps word followed
// by a literal inside an argument list. Either word, with its arguments if it
// has any, is overwritten.
func (s *cScan) stringMacro(i int) (macroCandidate, bool) {
	if !s.isString(i) {
		return macroCandidate{}, false
	}
	if s.word(i-1) == "extern" {
		return macroCandidate{}, false
	}
	j := i + 1
	if s.word(j) == "" {
		return macroCandidate{}, false
	}
	switch s.word(j) {
	case "volatile", "inline", "goto", "asm", "__asm__", "__volatile__":
		return macroCandidate{}, false
	}
	t := s.toks[j]
	c := macroCandidate{start: t.start, end: t.end, name: t.text(s.src), expect: -1}
	if s.punct(j+1, '(') {
		after := s.skipParens(j + 1)
		if after < 0 {
			return macroCandidate{}, false
		}
		c.end = s.toks[after-1].end
	}
	return c, true
}

// asmSpelling are the macros that stand for a spelling of the asm statement the
// grammar knows, each written in the same number of bytes as the macro. The
// grammar reads `asm_inline volatile(` as a declaration and loses the function
// it is in and everything after it.
var asmSpelling = map[string]string{
	"asm_inline":        "asm       ",
	"asm_goto":          "asm goto",
	"asm_volatile_goto": "asm volatile goto",
}

func isAttrWord(w string) bool {
	if cPlainAttrs[w] {
		return true
	}
	if len(w) < 4 || w[0] != '_' || w[1] != '_' || notAttr[w] || strings.HasPrefix(w, "__builtin") {
		return false
	}
	for i := 2; i < len(w); i++ {
		c := w[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

// cScan carries the token stream and the brace depth in front of each token.
type cScan struct {
	candScan
	depth []int32
}

func newCScan(src []byte, toks []ctok) *cScan {
	s := &cScan{candScan: candScan{src: src, toks: toks}}
	s.depth = make([]int32, len(toks))
	var d int32
	for i, t := range toks {
		s.depth[i] = d
		if t.kind == ctPunct {
			switch t.c {
			case '{':
				d++
			case '}':
				if d > 0 {
					d--
				}
			}
		}
	}
	return s
}

// findCCandidates returns the kernel-style candidates of a C file.
func findCCandidates(src []byte, toks []ctok) []macroCandidate {
	s := newCScan(src, toks)
	var out []macroCandidate
	var stack []int  // token index of the callee of each open parenthesis, -1 for none
	var inits []bool // for each open brace, whether it opens an initializer list
	for i := range toks {
		t := toks[i]
		switch t.kind {
		case ctDir:
			out = append(out, directiveComments(src, t)...)
			if len(inits) > 0 && inits[len(inits)-1] && isDefineDirective(src[t.start:t.end]) {
				// A #define in the middle of an initializer list is a
				// directive the grammar cannot place; the table around it
				// is read as an error and so is the file after it.
				out = append(out, macroCandidate{start: t.start, end: t.end, name: "#define", expect: -1})
			}
		case ctOther:
			if c, ok := s.stringMacro(i); ok {
				out = append(out, c)
			}
			if tx := t.text(src); isGluedNumber(tx) {
				// 10000baseT_Full, written as a macro argument that is pasted
				// into a name: not a number to the grammar, and its error
				// costs the rest of the function.
				out = append(out, macroCandidate{start: t.start, end: t.start + 1, name: tx, expect: -1, repl: []byte{'_'}})
			}
		case ctPunct:
			switch t.c {
			case '{':
				// An initializer list: its brace follows `=`, or sits inside
				// one.
				inits = append(inits, s.punct(i-1, '=') || s.word(i-1) == "enum" || s.word(i-2) == "enum" ||
					len(inits) > 0 && inits[len(inits)-1] && (s.punct(i-1, ',') || s.punct(i-1, '{')))
			case '}':
				if len(inits) > 0 {
					inits = inits[:len(inits)-1]
				}
			case '(':
				callee := -1
				if i > 0 && toks[i-1].kind == ctIdent && !callNotCall[toks[i-1].text(src)] {
					callee = i - 1
				}
				stack = append(stack, callee)
			case ')':
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
			}
		case ctIdent:
			w := t.text(src)
			if repl, ok := asmSpelling[w]; ok && (s.punct(i+1, '(') || s.word(i+1) == "volatile" || s.word(i+1) == "goto") {
				out = append(out, macroCandidate{start: t.start, end: t.end, name: w, expect: -1, repl: []byte(repl)})
				continue
			}
			if isMacroName(w) && s.isString(i+1) && (s.punct(i-1, '(') || s.punct(i-1, ',')) {
				// printk(KERN_INFO "up\n")
				out = append(out, macroCandidate{start: t.start, end: t.end, name: w, expect: -1})
				continue
			}
			if isAttrWord(w) {
				if c, ok := s.attrCandidate(i); ok {
					out = append(out, c)
				}
				continue
			}
			if len(stack) > 0 && stack[len(stack)-1] >= 0 && (w == "struct" || w == "union" || w == "enum" || cTypeWords[w]) {
				if c, ok := s.typeArgument(i); ok {
					out = append(out, c)
				}
			}
			if s.depth[i] > 0 && s.punct(i+1, '(') && !callNotCall[w] && !controlWords[w] && !cKeywords[w] {
				if c, ok := s.statementMacroBlock(i); ok {
					out = append(out, c)
				}
			}
			if s.depth[i] == 0 && isMacroName(w) && len(w) >= 4 && s.punct(i+1, '(') {
				out = append(out, s.macroFunction(i)...)
				out = append(out, s.storageBeforeMacro(i)...)
			}
		}
	}
	return out
}

// directiveComments returns the block comments written inside a preprocessor
// directive that continues onto a further line:
//
//	#define CHECK(x)  \
//		if (bad(x)) { \
//			return 0; /* success */ \
//		}
//
// The grammar reads the comment as part of the macro's body in one place and
// as code in another, errors on the line, and then reads everything after the
// directive as part of it: a whole file's definitions are lost behind one such
// comment. A comment is blanked, so the directive's body is the same text
// without it.
func directiveComments(src []byte, t ctok) []macroCandidate {
	text := src[t.start:t.end]
	if bytes.IndexByte(text, '\\') < 0 {
		return nil
	}
	var out []macroCandidate
	for i := 0; i+1 < len(text); i++ {
		switch text[i] {
		case '"', '\'':
			i = skipQuoted(text, i) - 1
		case '/':
			if text[i+1] != '*' {
				continue
			}
			end := bytes.Index(text[i+2:], []byte("*/"))
			if end < 0 {
				return out
			}
			end += i + 4
			out = append(out, macroCandidate{start: t.start + i, end: t.start + end, name: "/*", expect: -1})
			i = end - 1
		}
	}
	return out
}

// attrCandidate decides whether the attribute-shaped word at token i is an
// annotation to overwrite.
func (s *cScan) attrCandidate(i int) (macroCandidate, bool) {
	t := s.toks[i]
	c := macroCandidate{start: t.start, end: t.end, name: t.text(s.src), expect: -1}
	// The word's own argument list, when it has one, goes with it.
	next := i + 1
	if s.punct(next, '(') {
		after := s.skipParens(next)
		if after < 0 {
			return c, false
		}
		c.end = s.toks[after-1].end
		next = after
	}
	prev := i - 1
	if prev < 0 {
		return c, false
	}
	pt := s.toks[prev]
	pw := s.word(prev)

	// After a declarator: `x[4] __initdata`, `f(a) __releases(l) {`,
	// `} __packed;`.
	// A run of annotations: `f(a) __acquires(x) __releases(y) {`.
	after := next
	for isAttrWord(s.word(after)) {
		after++
		if s.punct(after, '(') {
			e := s.skipParens(after)
			if e < 0 {
				break
			}
			after = e
		}
	}
	if s.postDeclarator(after) {
		switch {
		case pt.kind == ctPunct && pt.c == ')':
			// The list must be a function's: `if (x) __free(y);` is a call.
			return c, s.closesDeclarator(prev)
		case pt.kind == ctPunct && pt.c == ']':
			return c, true
		case pt.kind == ctPunct && pt.c == '}':
			// `} __packed;`, never `} __call(x);`
			return c, cPostAttrs[c.name]
		case pt.kind == ctIdent && !typeStop[pw] && cPostAttrs[c.name]:
			return c, true
		}
	}

	// Between words: `void __iomem *p`, `int __init f(`, `struct __packed s`.
	if pt.kind == ctIdent && (pw == "struct" || pw == "union" || pw == "enum") {
		return c, s.word(next) != ""
	}
	if pt.kind == ctPunct && pt.c == '*' || pt.kind == ctIdent && !typeStop[pw] && !isAttrWord(pw) {
		return c, s.followWords(next) >= 1
	}
	// Before words, first of the declaration (after storage words and
	// qualifiers): an annotation when the rest is a declaration of its own.
	j := prev
	for j >= 0 && s.toks[j].kind == ctIdent && (cStorage[s.word(j)] || s.word(j) == "register" || s.word(j) == "auto") {
		j--
	}
	if j < 0 || s.startsDecl(j) {
		return c, s.followWords(next) >= 2
	}
	return c, false
}

// closesDeclarator reports whether the `)` at token i closes the parameter
// list of a function: an identifier that is not a control word stands in front
// of its opening parenthesis.
func (s *cScan) closesDeclarator(i int) bool {
	depth := 0
	for k := i; k >= 0 && k > i-400; k-- {
		t := s.toks[k]
		if t.kind == ctDir {
			return false
		}
		if t.kind != ctPunct {
			continue
		}
		switch t.c {
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				name := s.word(k - 1)
				if isAttrWord(name) {
					// The parentheses of an annotation: `f(a) __acquires(x)
					// __releases(y)`. Whether they follow a parameter list
					// is decided by what stands in front of the annotation.
					j := k - 2
					for isAttrWord(s.word(j)) {
						j--
					}
					return s.punct(j, ')') && s.closesDeclarator(j)
				}
				return name != "" && !controlWords[name] && !notDeclaration[name] && !isMacroName(name)
			}
		case ';', '{', '}':
			return false
		}
	}
	return false
}

// startsDecl reports whether the token at j ends what came before a
// declaration: `;`, `{`, `}`, `(` or `,` of a parameter list, a directive.
func (s *cScan) startsDecl(j int) bool {
	t := s.toks[j]
	switch t.kind {
	case ctDir:
		return true
	case ctPunct:
		switch t.c {
		case ';', '{', '}', '(', ',':
			return true
		}
	}
	return false
}

// postDeclarator reports whether the token at i can follow a declarator.
func (s *cScan) postDeclarator(i int) bool {
	if i >= len(s.toks) {
		return false
	}
	t := s.toks[i]
	return t.kind == ctPunct && (t.c == '=' || t.c == ';' || t.c == ',' || t.c == '{' || t.c == ')')
}

// followWords counts the words of a declaration that stand from token i on,
// through pointer stars and qualifiers and not counting annotation words: the
// type words and the declarator name. `void *base;` is two, `x;` is one.
func (s *cScan) followWords(i int) int {
	n := 0
	for k := i; k < len(s.toks) && k < i+24; k++ {
		t := s.toks[k]
		switch {
		case t.kind == ctPunct && t.c == '*':
		case t.kind == ctIdent:
			w := t.text(s.src)
			switch {
			case isAttrWord(w):
				if s.punct(k+1, '(') {
					after := s.skipParens(k + 1)
					if after < 0 {
						return n
					}
					if s.punct(after, '{') || s.punct(after, ';') {
						// `__printf(6, 7)\n__check_state(a, b)\n{`: a function
						// whose name starts with two underscores, not an
						// annotation.
						return n + 1
					}
					k = after - 1
				}
			case w == "const" || w == "volatile" || w == "restrict":
			default:
				n++
			}
		case t.kind == ctPunct && t.c == '(' && n >= 1 && s.punct(k+1, '*'):
			// A function pointer, `void (*showfn)(...)`: its declarator is
			// one more word.
			return n + 1
		default:
			return n
		}
	}
	return n
}

// typeArgument handles a type written as an argument of a call: `struct foo`,
// `struct foo *`, `unsigned long`. The token at i starts it and the run must
// end at `,` or `)`.
func (s *cScan) typeArgument(i int) (macroCandidate, bool) {
	prev := i - 1
	if prev < 0 || !s.punct(prev, '(') && !s.punct(prev, ',') {
		return macroCandidate{}, false
	}
	j := i
	words := 0
	switch w := s.word(j); w {
	case "struct", "union", "enum":
		if s.word(j+1) == "" {
			return macroCandidate{}, false
		}
		j += 2
		words = 2
	default:
		for cTypeWords[s.word(j)] {
			j++
			words++
		}
	}
	for s.punct(j, '*') || s.word(j) == "const" || s.word(j) == "volatile" {
		j++
	}
	if !s.punct(j, ',') && !s.punct(j, ')') {
		return macroCandidate{}, false
	}
	// `f(void)` and `f(int)` are prototypes more often than anything: a lone
	// type keyword is overwritten only when more than one argument is written.
	if words == 1 && cTypeWords[s.word(i)] && j == i+1 && s.punct(prev, '(') && s.punct(j, ')') {
		return macroCandidate{}, false
	}
	start, end := s.toks[i].start, s.toks[j-1].end
	repl := make([]byte, end-start)
	for k := range repl {
		repl[k] = '_'
		if b := s.src[start+k]; b == '\n' || b == '\r' {
			repl[k] = b
		}
	}
	return macroCandidate{start: start, end: end, name: s.word(i), expect: -1, repl: repl}, true
}

// macroFunction handles `HEAD(name, ...) {` at file scope: a function written
// as a macro call and a block. See the comment at the top of this file.
func (s *cScan) macroFunction(i int) []macroCandidate {
	head := s.word(i)
	// Only at the start of a declaration: after `;`, `}`, a directive or the
	// start of the file, past storage words.
	j := i - 1
	for j >= 0 && s.toks[j].kind == ctIdent && cStorage[s.word(j)] {
		j--
	}
	if j >= 0 && s.toks[j].kind != ctDir && !s.punct(j, ';') && !s.punct(j, '}') {
		return nil
	}
	open := i + 1
	after := s.skipParens(open)
	if after < 0 || !s.punct(after, '{') {
		return nil
	}
	first := open + 1
	if s.toks[first].kind != ctIdent || notDeclaration[s.word(first)] || cKeywords[s.word(first)] {
		return nil
	}
	// The top-level commas of the argument list.
	var commas []int
	depth := 0
	for k := open; k < after; k++ {
		t := s.toks[k]
		if t.kind != ctPunct {
			continue
		}
		switch t.c {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ',':
			if depth == 1 {
				commas = append(commas, k)
			}
		}
	}
	if len(commas) > 0 && commas[0] != first+1 || len(commas) == 0 && !s.punct(first+1, ')') {
		return nil
	}
	name := s.word(first)
	headStart := s.toks[i].start
	var spanEnd int
	var text string
	var out []macroCandidate
	switch {
	case cTestMacros[head] && len(commas) == 1 && s.word(first+2) != "" && s.punct(first+3, ')'):
		// TEST_F(fixture, name): the harness defines fixture_name.
		name += "_" + s.word(first+2)
		spanEnd = s.toks[after-1].end
		text = "void " + name + "()"
	case len(commas) == 0:
		spanEnd = s.toks[after-1].end
		text = "void " + prefixedName(head, name) + "()"
	default:
		spanEnd = s.toks[commas[0]].end
		text = "void " + prefixedName(head, name) + "("
		// Parameter pairs, `type, name`: the comma between a type and its name
		// becomes a space. Only when the macro's own digit says how many pairs
		// there are and the arguments agree.
		if n := trailingDigits(head); n > 0 && len(commas) == 2*n {
			for k := 1; k < len(commas); k += 2 {
				out = append(out, macroCandidate{
					start: s.toks[commas[k]].start, end: s.toks[commas[k]].end, name: head, expect: -1, repl: []byte{' '},
				})
			}
		}
	}
	if len(text) > spanEnd-headStart {
		return nil
	}
	repl := make([]byte, spanEnd-headStart)
	copy(repl, text)
	for k := len(text); k < len(repl); k++ {
		repl[k] = ' '
		if b := s.src[headStart+k]; b == '\n' || b == '\r' {
			repl[k] = b
		}
	}
	out = append(out, macroCandidate{
		start: headStart, end: spanEnd, name: head, expect: s.toks[first].start, repl: repl,
	})
	return out
}

// cTestMacros are the test registration macros of the kselftest and googletest
// families, whose two arguments name one function between them.
var cTestMacros = map[string]bool{"TEST": true, "TEST_F": true, "TEST_P": true, "TYPED_TEST": true}

// prefixedName is the name the macro's function has: the system call macros
// define `sys_name`, every other macro defines `name`.
func prefixedName(head, name string) string {
	h := strings.TrimRight(head, "0123456789")
	switch h {
	case "SYSCALL_DEFINE":
		return "sys_" + name
	case "COMPAT_SYSCALL_DEFINE":
		return "compat_sys_" + name
	}
	return name
}

func trailingDigits(s string) int {
	n, mul := 0, 1
	for i := len(s) - 1; i >= 0 && s[i] >= '0' && s[i] <= '9'; i-- {
		n += int(s[i]-'0') * mul
		mul *= 10
	}
	return n
}

// preferRewrite orders candidates by position, a rewrite before a blanking of
// the same start, so that the dedupe in findMacroCandidates keeps the rewrite.
func preferRewrite(a, b macroCandidate) bool {
	if a.start != b.start {
		return a.start < b.start
	}
	return a.repl != nil && b.repl == nil || bytes.Compare(a.repl, b.repl) < 0
}
