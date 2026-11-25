package parse

// Kotlin: two forms the grammar reads wrongly, each costing the rest of the
// file it is in.
//
//   - `i++; continue`. A postfix increment or decrement followed by `;` and
//     another statement on the same line is an error. `i++` on its own, `i++` and
//     a newline, and `i += 1; continue` are all fine. The mask rewrites `++;` as
//     `+1;`, an expression statement of the same length that the grammar reads.
//   - a block comment before `abstract class` in a file whose `package a.b` line
//     has no semicolon. With `open`, `data`, `sealed` or no modifier the same file is fine,
//     and a `;` after the package line fixes this one. A comment cannot be
//     deleted without moving offsets, so the mask turns the block comment into
//     line comments (`/**` becomes `///`, each ` *` becomes `//`), same length,
//     newlines kept.
//
// Neither rewrite touches a definition: a statement's value is unused and a
// comment defines nothing. See the Kotlin rows of docs/accuracy.md.

import (
	"bytes"
	"regexp"
	"sort"
)

// kotlinAbstractAfterComment finds a block comment that ends its line and is
// followed by a line starting `abstract`. The comment is matched on its own, so
// the match cannot reach across code.
var kotlinAbstractAfterComment = regexp.MustCompile(`(/\*(?:[^*]|\*+[^*/])*\*+/)[ \t]*\r?\n[ \t\r\n]*abstract\b`)

// findKotlinMasks returns the spans, in source order.
func findKotlinMasks(src []byte) []maskSpan {
	var out []maskSpan
	out = append(out, findKotlinPostfixStatements(src)...)
	out = append(out, findKotlinCommentBeforeAbstract(src)...)
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

// findKotlinPostfixStatements finds each `++;` and `--;` outside comments and
// strings.
func findKotlinPostfixStatements(src []byte) []maskSpan {
	if !bytes.Contains(src, []byte("++;")) && !bytes.Contains(src, []byte("--;")) {
		return nil
	}
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
		case c == '"':
			i = skipKotlinString(src, i)
		case c == '\'':
			i = skipKotlinChar(src, i)
		case (c == '+' || c == '-') && i+2 < len(src) && src[i+1] == c && src[i+2] == ';':
			out = append(out, maskSpan{start: i, repl: []byte{c, '1', ';'}})
			i += 2
		}
	}
	return out
}

// skipKotlinString returns the index of the quote that closes the string, a
// raw string included, opening at src[i].
func skipKotlinString(src []byte, i int) int {
	if bytes.HasPrefix(src[i:], []byte(`"""`)) {
		end := bytes.Index(src[i+3:], []byte(`"""`))
		if end < 0 {
			return len(src)
		}
		return i + 3 + end + 2
	}
	for i++; i < len(src); i++ {
		switch src[i] {
		case '\\':
			i++
		case '"', '\n':
			return i
		}
	}
	return len(src)
}

func skipKotlinChar(src []byte, i int) int {
	for i++; i < len(src); i++ {
		switch src[i] {
		case '\\':
			i++
		case '\'', '\n':
			return i
		}
	}
	return len(src)
}

// findKotlinCommentBeforeAbstract returns the spans that turn the block comment
// into line comments, or nil if a line of it cannot be turned. The comment must
// end its line: on the same line as `abstract` it would comment the class out.
func findKotlinCommentBeforeAbstract(src []byte) []maskSpan {
	if !bytes.Contains(src, []byte("abstract")) || !bytes.Contains(src, []byte("package ")) {
		return nil
	}
	var out []maskSpan
	for _, m := range kotlinAbstractAfterComment.FindAllSubmatchIndex(src, -1) {
		spans, ok := kotlinCommentAsLineComments(src, m[2], m[3])
		if ok {
			out = append(out, spans...)
		}
	}
	return out
}

// kotlinCommentAsLineComments turns the block comment src[start:end] into line
// comments, or reports that a line of it cannot be turned.
func kotlinCommentAsLineComments(src []byte, start, end int) ([]maskSpan, bool) {
	var out []maskSpan
	lineStart := start
	for pos := start; pos <= end; pos++ {
		if pos < end && src[pos] != '\n' {
			continue
		}
		line := src[lineStart:pos]
		sp, ok := kotlinLineCommentSpan(line, lineStart, lineStart == start)
		if !ok {
			return nil, false
		}
		if sp.repl != nil {
			out = append(out, sp)
		}
		lineStart = pos + 1
	}
	return out, true
}

// kotlinLineCommentSpan turns the first two characters of one comment line into
// `//`. The first line starts with `/*`; a later one with optional blanks then `*`
// or `*/`.
func kotlinLineCommentSpan(line []byte, at int, first bool) (maskSpan, bool) {
	if first { // `/*` or `/**`
		if len(line) < 2 {
			return maskSpan{}, false
		}
		return maskSpan{start: at, repl: []byte("//")}, true
	}
	trimmed := bytes.TrimLeft(line, " \t")
	if len(trimmed) == 0 {
		return maskSpan{}, true // a blank line inside the comment stays blank
	}
	indent := len(line) - len(trimmed)
	if indent < 1 || trimmed[0] != '*' {
		return maskSpan{}, false
	}
	// The blank before the star and the star become the slashes: ` * x` is `// x`.
	return maskSpan{start: at + indent - 1, repl: []byte("//")}, true
}
