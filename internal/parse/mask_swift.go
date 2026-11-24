package parse

// Swift: an attribute written on a function type.
//
// `@escaping @Sendable (_ data: Data?) -> Void` is how a parameter or a
// typealias takes a concurrency-safe closure. The grammar reads the attribute
// as one that takes arguments, reads `(_ data: Data?)` as its argument list,
// and fails there; with a second such parameter in the file it gives up on the
// whole declaration, and then on the class around it (Alamofire's
// DownloadRequest.swift: 13 of 41 definitions). See
// fixtures/langs/swift/KNOWN_GAPS.md.
//
// The mask overwrites the attribute's name with spaces when only white space
// separates it from an opening parenthesis, which makes the parenthesis a
// function type of its own. A type's attribute carries no definition, so
// nothing is lost; text of the same length keeps every offset and line.

import (
	"bytes"
	"sort"
)

var swiftTypeAttributes = []string{"@Sendable", "@MainActor"}

// findSwiftTypeAttributes returns one span for each `@Sendable` or
// `@MainActor` that is followed by `(` after white space only, outside
// comments and strings.
func findSwiftTypeAttributes(src []byte) []maskSpan {
	var out []maskSpan
	for i := 0; i < len(src); {
		if next, ok := swSkipTrivia(src, i); ok {
			i = next
			continue
		}
		if src[i] == '@' {
			for _, attr := range swiftTypeAttributes {
				end := i + len(attr)
				if end >= len(src) || string(src[i:end]) != attr {
					continue
				}
				k := end
				for k < len(src) && (src[k] == ' ' || src[k] == '\t') {
					k++
				}
				if k > end && k < len(src) && src[k] == '(' {
					out = append(out, maskSpan{start: i, repl: []byte(blankSpaces(len(attr)))})
					i = end
				}
				break
			}
		}
		i++
	}
	return out
}

func blankSpaces(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}

// swSkipTrivia returns the end of the comment or string that starts at
// src[i], if one does: `//`, a nested `/* */`, `"..."`, `"""..."""`, and the
// raw forms `#"..."#`.
func swSkipTrivia(src []byte, i int) (int, bool) {
	c := src[i]
	switch {
	case c == '/' && i+1 < len(src) && src[i+1] == '/':
		for i < len(src) && src[i] != '\n' {
			i++
		}
		return i, true
	case c == '/' && i+1 < len(src) && src[i+1] == '*':
		depth := 0
		for k := i; k+1 < len(src); k++ {
			switch {
			case src[k] == '/' && src[k+1] == '*':
				depth++
				k++
			case src[k] == '*' && src[k+1] == '/':
				depth--
				k++
				if depth == 0 {
					return k + 1, true
				}
			}
		}
		return len(src), true
	case c == '"' || c == '#':
		hashes := 0
		k := i
		for k < len(src) && src[k] == '#' {
			hashes++
			k++
		}
		if k >= len(src) || src[k] != '"' {
			return i, false
		}
		multi := k+2 < len(src) && src[k+1] == '"' && src[k+2] == '"'
		open := 1
		if multi {
			open = 3
		}
		k += open
		for ; k < len(src); k++ {
			if src[k] == '\\' && hashes == 0 {
				k++
				continue
			}
			if src[k] == '\n' && !multi {
				return k, true
			}
			if src[k] != '"' {
				continue
			}
			j := k + 1
			if multi {
				if j+1 >= len(src) || src[j] != '"' || src[j+1] != '"' {
					continue
				}
				j += 2
			}
			h := 0
			for h < hashes && j+h < len(src) && src[j+h] == '#' {
				h++
			}
			if h == hashes {
				return j + h, true
			}
		}
		return len(src), true
	}
	return i, false
}

// Swift: a condition list of three or more with a plain expression late in it.
//
// `if a == 1, b == 2, c == 3 {`, `guard a, b, c else {}` and
// `if let host = url.host, flag, host != appHost {` are ordinary Swift, and the
// grammar reads none of them: two conditions are fine, and so is a binding
// (`let`, `var`, `case`) as the last, but a plain expression after the second
// comma is an error, which costs the function around it (pulse's
// WebViewController.swift: 12 error nodes).
//
// The mask overwrites each such comma with `&`, a binary operator the grammar
// reads between two expressions. The statement's meaning is not read by anything
// here, and no definition is inside a condition; same length, newlines kept.
// Only a comma at the top level of an if, guard or while header, from the second
// comma on and before an expression, is touched.

// swiftConditionHeader is how far a header is followed, in bytes.
const swiftConditionHeader = 2000

// findSwiftConditionCommas returns one one-byte span for each such comma.
func findSwiftConditionCommas(src []byte) []maskSpan {
	var out []maskSpan
	for pos := 0; pos < len(src); {
		end := pos
		for end < len(src) && src[end] != '\n' {
			end++
		}
		if start, ok := swiftHeaderStart(src[pos:end]); ok {
			out = append(out, swiftHeaderCommas(src, pos+start)...)
		}
		pos = end + 1
	}
	return out
}

// swiftHeaderStart reports where the conditions begin on a line that opens an
// if, guard or while.
func swiftHeaderStart(line []byte) (int, bool) {
	indent := len(line) - len(bytes.TrimLeft(line, " \t"))
	rest := line[indent:]
	for _, p := range []string{"} else if ", "else if ", "if ", "guard ", "while "} {
		if bytes.HasPrefix(rest, []byte(p)) {
			return indent + len(p), true
		}
	}
	return 0, false
}

// swiftHeaderCommas scans a header from `from` to its opening brace (or the
// `else` of a guard) and returns the commas to overwrite.
func swiftHeaderCommas(src []byte, from int) []maskSpan {
	var commas []int
	depth := 0
	limit := from + swiftConditionHeader
	if limit > len(src) {
		limit = len(src)
	}
scan:
	for i := from; i < limit; i++ {
		switch c := src[i]; {
		case c == '"':
			i = skipTSString(src, i)
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < limit && src[i] != '\n' {
				i++
			}
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case c == '{' && depth <= 0:
			break scan
		case c == ',' && depth == 0:
			commas = append(commas, i)
		case c == 'e' && depth == 0 && swiftWordAt(src, i, "else"):
			break scan
		}
	}
	var out []maskSpan
	for n, at := range commas {
		if n < 1 { // the first comma is fine, and so is the second before a binding
			continue
		}
		k := at + 1
		for k < len(src) && (src[k] == ' ' || src[k] == '\t' || src[k] == '\n' || src[k] == '\r') {
			k++
		}
		if swiftWordAt(src, k, "let") || swiftWordAt(src, k, "var") || swiftWordAt(src, k, "case") {
			continue
		}
		out = append(out, maskSpan{start: at, repl: []byte("&")})
	}
	return out
}

// swiftWordAt reports whether src[i:] begins with the whole word.
func swiftWordAt(src []byte, i int, word string) bool {
	if i+len(word) > len(src) || string(src[i:i+len(word)]) != word {
		return false
	}
	if i > 0 && isTSIdentByte(src[i-1]) {
		return false
	}
	return i+len(word) == len(src) || !isTSIdentByte(src[i+len(word)])
}

// findSwiftMasks is every Swift mask, in source order.
func findSwiftMasks(src []byte) []maskSpan {
	out := append(findSwiftTypeAttributes(src), findSwiftConditionCommas(src)...)
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}
