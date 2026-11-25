package langdetect

import (
	"bytes"
	"regexp"
)

var (
	cplusplusToken = []byte("__cplusplus")

	// cplusplusIfdef is `ifdef __cplusplus`. cplusplusNegated is a condition
	// that is true for C: `!defined(__cplusplus)`, `!__cplusplus`.
	cplusplusIfdef   = regexp.MustCompile(`\Aifdef[ \t]+__cplusplus\b`)
	cplusplusNegated = regexp.MustCompile(`![ \t]*(defined[ \t]*\(?[ \t]*)?__cplusplus\b`)
)

// maskCplusplusGuards blanks the text a C compiler would not see: whatever
// sits inside `#ifdef __cplusplus`, `#if defined(__cplusplus)` or
// `#if __cplusplus >= 201103L`, up to the matching `#else` or `#endif`.
//
// A header that hides part of itself from C compilers is, by that act, a C
// header: the block exists so that one file can be included from both
// languages. What is inside it is C++, and reading it as evidence made C
// libraries C++. In redis, 26 CRoaring headers open with
//
//	#ifdef __cplusplus
//	extern "C" {
//	namespace roaring {
//	#endif
//
// and each was routed to the C++ grammar on the strength of that `namespace`.
// The C++ markers are therefore tested against the header as a C compiler
// reads it. A header that is C++ throughout still shows its classes outside
// any such block and is unaffected.
//
// Blanked bytes become spaces and newlines are kept, so offsets and line
// structure are unchanged. The directive lines themselves stay. The input is
// returned as is, with no copy, when nothing was blanked; that includes every
// header that never mentions __cplusplus.
func maskCplusplusGuards(head []byte) []byte {
	if !bytes.Contains(head, cplusplusToken) {
		return head
	}
	var out []byte
	depth := 0 // nesting of #if blocks inside a guard; 0 when outside one
	for pos := 0; pos < len(head); {
		end := len(head)
		if n := bytes.IndexByte(head[pos:], '\n'); n >= 0 {
			end = pos + n
		}
		directive := preprocessorDirective(head[pos:end])

		masked := depth > 0
		switch {
		case directive == nil:
		case depth == 0:
			if opensCplusplusBlock(directive) {
				depth = 1
			}
		case bytes.HasPrefix(directive, []byte("if")):
			depth++
		case bytes.HasPrefix(directive, []byte("endif")):
			depth--
			masked = depth > 0
		case depth == 1 && (bytes.HasPrefix(directive, []byte("else")) || bytes.HasPrefix(directive, []byte("elif"))):
			// The other branch is what a C compiler sees, unless it is itself
			// conditional on C++.
			depth, masked = 0, false
			if opensCplusplusBlock(directive) {
				depth = 1
			}
		}

		if masked {
			if out == nil {
				out = append([]byte(nil), head...)
			}
			for i := pos; i < end; i++ {
				if out[i] != '\r' {
					out[i] = ' '
				}
			}
		}
		pos = end + 1
	}
	if out == nil {
		return head
	}
	return out
}

// opensCplusplusBlock reports whether a directive starts a block compiled only
// as C++.
func opensCplusplusBlock(directive []byte) bool {
	switch {
	case bytes.HasPrefix(directive, []byte("ifdef")):
		return cplusplusIfdef.Match(directive)
	case bytes.HasPrefix(directive, []byte("ifndef")):
		return false
	case bytes.HasPrefix(directive, []byte("if")), bytes.HasPrefix(directive, []byte("elif")):
		return bytes.Contains(directive, cplusplusToken) && !cplusplusNegated.Match(directive)
	}
	return false
}

// preprocessorDirective returns what follows the `#` of a preprocessor line,
// with the blanks around the `#` removed, or nil for any other line.
func preprocessorDirective(line []byte) []byte {
	line = bytes.TrimLeft(line, " \t")
	if len(line) < 2 || line[0] != '#' {
		return nil
	}
	return bytes.TrimRight(bytes.TrimLeft(line[1:], " \t"), " \t\r")
}
