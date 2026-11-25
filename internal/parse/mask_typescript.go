package parse

// TypeScript: `using` as the name of a variable.
//
// `using` begins a resource-management declaration (`using conn = open()`), so
// the grammar reads the word as that keyword wherever it can. A variable that
// happens to be called `using` then breaks the statement it is in: `const using
// = files.filter(...)` is read, but `using.length` and `return using` are not,
// and the parser gives up on the rest of the enclosing block. In the one pulse
// file that does this, two error nodes cost the definitions after them.
//
// The mask renames each such `using` to `usinG`, the same length with the same
// newlines, so every offset and line number is unchanged. A declaration (`using`
// followed by a name on the same line, or `await using x`) is left alone, as are
// comments, strings and template literals. The renamed word is a local variable's
// name, which nothing resolves against the index, so nothing is lost.

// findTSUsingIdentifiers returns one span for each `using` that is a plain name.
func findTSUsingIdentifiers(src []byte) []maskSpan {
	var out []maskSpan
	for i := 0; i < len(src); i++ {
		switch c := src[i]; {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			i += 2
			for i+1 < len(src) && (src[i] != '*' || src[i+1] != '/') {
				i++
			}
			i++
		case c == '\'' || c == '"' || c == '`':
			i = skipTSString(src, i)
		case c == 'u' && isTSUsing(src, i):
			if !tsUsingIsDeclaration(src, i+5) {
				out = append(out, maskSpan{start: i, repl: []byte("usinG")})
			}
			i += 4
		}
	}
	return out
}

// skipTSString returns the index of the quote that closes the string, or a
// template literal, opening at src[i]. A template's `${...}` is read as part of
// the template, which is what keeps this from needing a parser.
func skipTSString(src []byte, i int) int {
	quote := src[i]
	for i++; i < len(src); i++ {
		switch {
		case src[i] == '\\':
			i++
		case src[i] == quote:
			return i
		case src[i] == '\n' && quote != '`':
			return i // an unterminated string ends at the line
		}
	}
	return len(src)
}

// isTSUsing reports whether src[i:] is the whole word `using`.
func isTSUsing(src []byte, i int) bool {
	if i+5 > len(src) || string(src[i:i+5]) != "using" {
		return false
	}
	if i > 0 && isTSIdentByte(src[i-1]) {
		return false
	}
	return i+5 == len(src) || !isTSIdentByte(src[i+5])
}

// tsUsingIsDeclaration reports whether what follows `using` on its line is a
// binding name: `using x = ...`, `await using x`, `for (using x of y)`.
func tsUsingIsDeclaration(src []byte, from int) bool {
	k := from
	for k < len(src) && (src[k] == ' ' || src[k] == '\t') {
		k++
	}
	if k == from { // no space: `using.x`, `using)`, `using,`
		return false
	}
	return k < len(src) && isTSIdentByte(src[k]) && (src[k] < '0' || src[k] > '9')
}

func isTSIdentByte(b byte) bool {
	return b == '_' || b == '$' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
