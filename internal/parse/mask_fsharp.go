package parse

import "bytes"

// F#: the statically resolved member constraint call.
//
// `(^T: (member GetAwaiter: unit -> ^A) (task))` is how an inline function or
// a computation-expression builder calls a member on a type chosen at the call
// site. gotreesitter's F# parser loops on it without consuming a token; the
// library ends the loop with its no-progress guard and reports an iteration
// limit, and everything after the call is lost. In FsToolkit.ErrorHandling
// that is the second half of two builder files. See
// fixtures/langs/fsharp/KNOWN_GAPS.md and lang_fsharp_test.go.
//
// The mask overwrites the whole parenthesised expression, from the opening
// parenthesis before `^T` to its matching closing one (the arguments are
// inside it), with spaces, keeping newlines, and puts `()` at its start so that
// `let a = ` and `if not ... then` still have an operand. What the call
// returned is not a definition, so nothing is lost by reading it as unit.
//
// Forms (all seen in real code):
//
//	(^T: (member GetAwaiter: unit -> ^A) (task))
//	(^T: (member GetAwaiter: unit -> ^A) task)
//	(^a : (static member op_Implicit : ^b -> ^a) arg)
//	(^T or ^U : (static member (+) : ^T * ^U -> ^V) (a, b))
//	(^M: (member Bind: 'd -> ('e -> 'c) -> 'c) (builder,
//	                                           m, f))
//
// The constraint written after a signature (`when ^T : (member Name: int)`) is
// not an expression and is not touched.

// findFSharpConstraintCalls returns one span per member constraint call
// outside comments, strings and character literals. A call inside another
// one is covered by the outer span.
func findFSharpConstraintCalls(src []byte) []maskSpan {
	var out []maskSpan
	for i := 0; i < len(src); {
		if next, ok := fsSkipTrivia(src, i); ok {
			i = next
			continue
		}
		if src[i] == '(' {
			if end, ok := fsConstraintCallEnd(src, i); ok {
				out = append(out, maskSpan{start: i, repl: fsFiller(src[i:end])})
				i = end
				continue
			}
		}
		if src[i] == 'e' && fsAtLineStart(src, i) && fsWordAt(src, i, "extern") {
			if end, repl, ok := fsExternDeclaration(src, i); ok {
				out = append(out, maskSpan{start: i, repl: repl})
				i = end
				continue
			}
		}
		if src[i] == 'a' && fsAtLineStart(src, i) && fsWordAt(src, i, "abstract") {
			if at, end, ok := fsAbstractTupleResult(src, i); ok {
				repl := fsBlank(src[at:end])
				repl[0] = '_'
				out = append(out, maskSpan{start: at, repl: repl})
				i = end
				continue
			}
		}
		if src[i] == 'f' && (i == 0 || !fsIdentByte(src[i-1])) && fsWordAt(src, i, "function") {
			if end, ok := fsUndentedFunctionEnd(src, i); ok {
				repl := fsBlank(src[i:end])
				copy(repl[len("function")-2:], "()")
				out = append(out, maskSpan{start: i, repl: repl})
				i = end
				continue
			}
		}
		if src[i] == 'w' && (i == 0 || !fsIdentByte(src[i-1])) && fsWordAt(src, i, "when") {
			if end, ok := fsWhenClauseEnd(src, i); ok {
				out = append(out, maskSpan{start: i, repl: fsBlank(src[i:end])})
				i = end
				continue
			}
		}
		i++
	}
	return out
}

func init() {
	// `#if A ... #else ... #endif` inside a function body or a union is not
	// valid in the grammar either (Giraffe's Core.fs: everything after one is
	// lost). F# directives are the C# ones, so the conditional mask of cond.go
	// reads them: one branch kept, the others and the directive lines blanked,
	// under the same guard.
	condLanguages["fsharp"] = true
}

// sourceNormalizers are rewrites made before the first parse, for a construct
// that costs more than a mask can save: the plain parse itself is the harm. A
// normalizer returns text of the same length with the same newlines and
// changes comments only.
var sourceNormalizers = map[string]func([]byte) []byte{
	"fsharp": fsNormalizeBareDocComments,
}

// fsNormalizeBareDocComments turns a `///` that has no text after it into
// `//` and a space. The grammar does not read a bare `///` as a comment: it
// reads the slashes as an operator, so the declaration after it is lost with
// no error to show for it, and the parse time grows with the square of the
// number of such lines (300 of them take 5.6 s; FAKE's AssemblyInfoFile.fs,
// full of them, held a worker for the 60 s ceiling). A `///` with text after
// it is read properly.
func fsNormalizeBareDocComments(src []byte) []byte {
	var out []byte
	for i := 0; i < len(src); {
		if src[i] == '/' && i+2 < len(src) && src[i+1] == '/' && src[i+2] == '/' {
			end := i + 3
			for end < len(src) && (src[end] == ' ' || src[end] == '\t') {
				end++
			}
			if end == len(src) || src[end] == '\n' || src[end] == '\r' {
				if out == nil {
					out = append([]byte(nil), src...)
				}
				out[i+2] = ' '
			}
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}
		if next, ok := fsSkipTrivia(src, i); ok {
			i = next
			continue
		}
		i++
	}
	if out == nil {
		return src
	}
	return out
}

// fsAbstractTupleResult reads an abstract member whose signature, on one line,
// ends in a tuple after the last arrow:
//
//	abstract TryLoad: context: Context -> Context * Info option
//
// The grammar reads `a -> b * c` there as an error (parenthesised, or with the
// tuple before the arrow, it is fine), and the interface it is in is lost with
// everything that follows. It returns the span of the result type, `Context *
// Info option`, which becomes `_` and spaces: the signature text is not a
// definition, and the member keeps its name.
func fsAbstractTupleResult(src []byte, at int) (start, end int, ok bool) {
	eol := at
	for eol < len(src) && src[eol] != '\n' && src[eol] != '\r' {
		eol++
	}
	depth, star, arrow := 0, false, -1
	colon := false
	for k := at; k < eol; k++ {
		if next, trivia := fsSkipTrivia(src, k); trivia {
			if src[k] == '/' {
				eol = k // a trailing comment ends the signature
				break
			}
			k = next - 1
			continue
		}
		switch c := src[k]; {
		case c == '(' || c == '[' || c == '<' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case c == '>' && k > 0 && src[k-1] != '-':
			depth--
		case c == ':' && depth == 0 && !colon:
			colon = true
		case c == '-' && k+1 < eol && src[k+1] == '>' && depth == 0 && colon:
			arrow, star = k+2, false
			k++
		case c == '*' && depth == 0 && arrow >= 0:
			star = true
		}
	}
	if arrow < 0 || !star || depth != 0 {
		return 0, 0, false
	}
	start = arrow
	for start < eol && (src[start] == ' ' || src[start] == '\t') {
		start++
	}
	end = eol
	for end > start && (src[end-1] == ' ' || src[end-1] == '\t') {
		end--
	}
	if end-start < 1 {
		return 0, 0, false
	}
	return start, end, true
}

// fsUndentedFunctionEnd reads
//
//	let (|Case|_|) = function
//	| (true, x), Some y -> Some x
//	| _ -> None
//
// where `function` ends its line and its cases start in the column of the
// line's first token. The offside rule allows it, the grammar loops on it
// (FAKE's TargetHelper.fs stops at the first one, at line 625, after which no
// definition is read). It returns the end of the last case; the span from
// `function` to there becomes `()` and spaces. A binding that is not the first
// thing on its line, or cases indented further, are read properly and are not
// matched.
func fsUndentedFunctionEnd(src []byte, at int) (int, bool) {
	// `function` is the last word of its line.
	eol := at + len("function")
	for eol < len(src) && (src[eol] == ' ' || src[eol] == '\t') {
		eol++
	}
	if eol >= len(src) || src[eol] != '\n' && src[eol] != '\r' {
		return 0, false
	}
	ls := at
	for ls > 0 && src[ls-1] != '\n' {
		ls--
	}
	indent := 0
	for ls+indent < len(src) && src[ls+indent] == ' ' {
		indent++
	}
	if !fsWordAt(src, ls+indent, "let") && !fsWordAt(src, ls+indent, "member") && !fsWordAt(src, ls+indent, "and") {
		return 0, false
	}
	end := -1
	for k := eol; k < len(src); {
		// k is at a line break; look at the next line.
		for k < len(src) && (src[k] == '\n' || src[k] == '\r') {
			k++
		}
		n := 0
		for k+n < len(src) && src[k+n] == ' ' {
			n++
		}
		le := k
		for le < len(src) && src[le] != '\n' {
			le++
		}
		blank := k+n >= len(src) || src[k+n] == '\n' || src[k+n] == '\r'
		switch {
		case blank:
			k = le
			continue
		case n > indent, n == indent && src[k+n] == '|':
			end = le
			if end > k && src[end-1] == '\r' {
				end--
			}
			k = le
			continue
		}
		break
	}
	if end < 0 {
		return 0, false
	}
	// The first case is in the column of the binding.
	first := firstNonBlankAfter(src, eol)
	col := 0
	for first-col > 0 && src[first-col-1] != '\n' {
		col++
	}
	if src[first] != '|' || col != indent {
		return 0, false
	}
	return end, true
}

// firstNonBlankAfter is the index of the first byte after pos that is not a
// blank or a line break (len(src)-1 at most).
func firstNonBlankAfter(src []byte, pos int) int {
	for pos < len(src)-1 && (src[pos] == ' ' || src[pos] == '\t' || src[pos] == '\n' || src[pos] == '\r') {
		pos++
	}
	return pos
}

// fsAtLineStart reports whether only blanks stand between the start of the
// line and src[i].
func fsAtLineStart(src []byte, i int) bool {
	for i--; i >= 0 && src[i] != '\n'; i-- {
		if src[i] != ' ' && src[i] != '\t' {
			return false
		}
	}
	return true
}

// fsExternDeclaration reads a P/Invoke declaration that starts at the `extern`
// at src[at], `extern uint32 QueryFullProcessImageName(nativeint h, uint32& n)`,
// and returns where it ends and what to write over it: the C-style parameter
// list the grammar cannot read (it stalls on the keyword) becomes a function
// `let Name()=()` of the same length, so the attribute above it, the name and
// everything after the declaration are read, and the extern is a function
// definition.
//
// The text is spaces except for `let` at the start and, when the declaration
// is on one line, `Name()=()` right-aligned at its end; when the parameters
// span lines, `Name(` stays where it was and `)=()` closes the last line.
func fsExternDeclaration(src []byte, at int) (int, []byte, bool) {
	i := at + len("extern")
	open := -1
	for k := i; k < len(src) && src[k] != '\n' && src[k] != ';'; k++ {
		if src[k] == '(' {
			open = k
			break
		}
	}
	if open < 0 {
		return 0, nil, false
	}
	nameEnd := open
	for nameEnd > i && (src[nameEnd-1] == ' ' || src[nameEnd-1] == '\t') {
		nameEnd--
	}
	nameStart := nameEnd
	for nameStart > i && fsIdentByte(src[nameStart-1]) {
		nameStart--
	}
	if nameStart == nameEnd || nameStart == i {
		return 0, nil, false
	}
	depth := 0
	end := -1
	for k := open; k < len(src) && end < 0; {
		if next, ok := fsSkipTrivia(src, k); ok {
			k = next
			continue
		}
		switch src[k] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				end = k + 1
			}
		}
		k++
	}
	if end < 0 {
		return 0, nil, false
	}
	repl := fsBlank(src[at:end])
	name := src[nameStart:nameEnd]
	copy(repl, "let")
	first := bytes.IndexByte(src[at:end], '\n')
	if first < 0 {
		first = len(repl)
	} else if first > 0 && src[at+first-1] == '\r' {
		first--
	}
	if tail := string(name) + "()=()"; len(tail)+4 <= first {
		// The whole declaration fits on the first line.
		copy(repl[first-len(tail):], tail)
		return end, repl, true
	}
	if first == len(repl) {
		return 0, nil, false
	}
	last := bytes.LastIndexByte(src[at:end], '\n')
	if len(repl)-last-1 < 4 || nameStart-at < 4 {
		return 0, nil, false
	}
	copy(repl[nameStart-at:], name)
	repl[nameStart-at+len(name)] = '('
	copy(repl[len(repl)-4:], ")=()")
	return end, repl, true
}

// fsBlank is text with every byte but a newline replaced by a space.
func fsBlank(text []byte) []byte {
	out := make([]byte, len(text))
	for i, c := range text {
		if c == '\n' || c == '\r' {
			out[i] = c
		} else {
			out[i] = ' '
		}
	}
	return out
}

// fsWhenClauseEnd reports where the type constraint clause that starts at
// the `when` at src[at] ends (the index of the `=` that closes the header),
// when it is one: `when ^T: (member M: int)`, `when 'a: struct`,
// `when ^a :> I and ^b: (static member Z: ^b)`. It must open with a type
// variable and a colon, and reach a `=` outside parentheses; a clause inside
// angle brackets (`<'T when 'T: struct>`) ends at `>` and a match guard
// (`| x when x > 0 ->`) is not led by a type variable, so neither is one.
func fsWhenClauseEnd(src []byte, at int) (int, bool) {
	i := fsSkipSpace(src, at+len("when"))
	if i >= len(src) || (src[i] != '^' && src[i] != '\'') {
		return 0, false
	}
	j := i + 1
	for j < len(src) && fsIdentByte(src[j]) {
		j++
	}
	if j == i+1 {
		return 0, false
	}
	j = fsSkipSpace(src, j)
	if j >= len(src) || src[j] != ':' {
		return 0, false
	}
	depth := 0
	for k := j; k < len(src); {
		if next, ok := fsSkipTrivia(src, k); ok {
			k = next
			continue
		}
		switch c := src[k]; {
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth < 0 {
				return 0, false
			}
		case depth == 0 && (c == '>' && src[k-1] != ':' && src[k-1] != '-' || c == '|' || c == ';' || c == '{' || c == '}' || c == '['):
			return 0, false
		case depth == 0 && c == '-' && k+1 < len(src) && src[k+1] == '>':
			return 0, false
		case depth == 0 && c == '=':
			if src[k-1] == '<' || src[k-1] == '>' || src[k-1] == '!' || k+1 < len(src) && (src[k+1] == '=' || src[k+1] == '>') {
				return 0, false
			}
			return k, true
		}
		k++
	}
	return 0, false
}

// fsFiller is the replacement for a constraint call: spaces, newlines kept,
// `()` first.
func fsFiller(text []byte) []byte {
	out := make([]byte, len(text))
	for i, c := range text {
		if c == '\n' || c == '\r' {
			out[i] = c
		} else {
			out[i] = ' '
		}
	}
	if len(out) >= 2 {
		out[0], out[1] = '(', ')'
	}
	return out
}

// fsConstraintCallEnd reports where the constraint call that opens at src[open]
// ends (one past its closing parenthesis), when src[open:] is one.
func fsConstraintCallEnd(src []byte, open int) (int, bool) {
	i := fsSkipSpace(src, open+1)
	// `^T`, then any number of `or ^U`.
	for {
		if i >= len(src) || src[i] != '^' {
			return 0, false
		}
		j := i + 1
		for j < len(src) && fsIdentByte(src[j]) {
			j++
		}
		if j == i+1 {
			return 0, false
		}
		i = fsSkipSpace(src, j)
		if i+2 < len(src) && src[i] == 'o' && src[i+1] == 'r' && (src[i+2] == ' ' || src[i+2] == '\t' || src[i+2] == '\n' || src[i+2] == '\r') {
			i = fsSkipSpace(src, i+2)
			continue
		}
		break
	}
	if i >= len(src) || src[i] != ':' {
		return 0, false
	}
	i = fsSkipSpace(src, i+1)
	if i >= len(src) || src[i] != '(' {
		return 0, false
	}
	i = fsSkipSpace(src, i+1)
	if fsWordAt(src, i, "static") {
		i = fsSkipSpace(src, i+len("static"))
	}
	if !fsWordAt(src, i, "member") {
		return 0, false
	}
	// The matching parenthesis of the outer one.
	depth := 0
	for k := open; k < len(src); {
		if next, ok := fsSkipTrivia(src, k); ok {
			k = next
			continue
		}
		switch src[k] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return k + 1, true
			}
		}
		k++
	}
	return 0, false
}

func fsIdentByte(c byte) bool {
	return c == '_' || c == '\'' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

func fsSkipSpace(src []byte, i int) int {
	for i < len(src) && (src[i] == ' ' || src[i] == '\t' || src[i] == '\n' || src[i] == '\r') {
		i++
	}
	return i
}

// fsWordAt reports whether src[i:] starts with word as a whole word.
func fsWordAt(src []byte, i int, word string) bool {
	if i+len(word) > len(src) || string(src[i:i+len(word)]) != word {
		return false
	}
	return i+len(word) == len(src) || !fsIdentByte(src[i+len(word)])
}

// fsSkipTrivia returns the end of the comment, string or character literal
// that starts at src[i], when one does.
//
// The forms: `// ...` to the end of the line; `(* ... *)`, which nests (`(*)`
// is the multiplication operator, not a comment); "..." with backslash
// escapes; @"..." verbatim, where `""` is a quote; """...""" triple-quoted;
// $"...", $@"..." and $"""...""" interpolated, read as the string they begin
// with (a hole holding a quote is not followed); and 'x', '\n', '\” and
// friends. A single quote that does not close like that is a type variable
// (`'T`) and is not trivia.
func fsSkipTrivia(src []byte, i int) (int, bool) {
	c := src[i]
	switch {
	case c == '/' && i+1 < len(src) && src[i+1] == '/':
		for i < len(src) && src[i] != '\n' {
			i++
		}
		return i, true
	case c == '(' && i+1 < len(src) && src[i+1] == '*':
		if i+2 < len(src) && src[i+2] == ')' {
			return i + 3, true
		}
		depth := 0
		for k := i; k < len(src); {
			switch {
			case src[k] == '(' && k+1 < len(src) && src[k+1] == '*' && (k+2 >= len(src) || src[k+2] != ')'):
				depth++
				k += 2
			case src[k] == '*' && k+1 < len(src) && src[k+1] == ')':
				depth--
				k += 2
				if depth == 0 {
					return k, true
				}
			default:
				k++
			}
		}
		return len(src), true
	case c == '"':
		return fsSkipString(src, i, false), true
	case c == '@' && i+1 < len(src) && src[i+1] == '"':
		return fsSkipString(src, i+1, true), true
	case c == '$' && i+1 < len(src) && src[i+1] == '"':
		return fsSkipString(src, i+1, false), true
	case c == '$' && i+2 < len(src) && src[i+1] == '@' && src[i+2] == '"', c == '@' && i+2 < len(src) && src[i+1] == '$' && src[i+2] == '"':
		return fsSkipString(src, i+2, true), true
	case c == '\'':
		// 'x'
		if i+2 < len(src) && src[i+1] != '\\' && src[i+1] != '\n' && src[i+2] == '\'' {
			return i + 3, true
		}
		// '\n', '\'', '\\', 'A', '\065'
		if i+3 < len(src) && src[i+1] == '\\' {
			for k := i + 3; k < len(src) && k <= i+8 && src[k-1] != '\n'; k++ {
				if src[k] == '\'' {
					return k + 1, true
				}
			}
		}
	}
	return i, false
}

// fsSkipString returns the end of the string whose opening quote is src[q].
func fsSkipString(src []byte, q int, verbatim bool) int {
	if !verbatim && q+2 < len(src) && src[q+1] == '"' && src[q+2] == '"' {
		for k := q + 3; k+2 < len(src); k++ {
			if src[k] == '"' && src[k+1] == '"' && src[k+2] == '"' {
				return k + 3
			}
		}
		return len(src)
	}
	for k := q + 1; k < len(src); k++ {
		switch {
		case src[k] == '\\' && !verbatim:
			k++
		case src[k] == '"' && verbatim && k+1 < len(src) && src[k+1] == '"':
			k++
		case src[k] == '"':
			return k + 1
		}
	}
	return len(src)
}
