package gotypes

import (
	"go/scanner"
	"go/token"
)

// stripBodies blanks the body of every top-level function declaration in src,
// keeping every newline so that line numbers do not move.
//
// WHY: dependencies and the standard library are checked with
// IgnoreFuncBodies, because their bodies cannot change what a repository's
// code resolves to. But go/parser has no such mode, and the AST of a body is
// most of the memory of a package: a generated 10 MB file in a dependency
// (modernc.org/sqlite/lib is one) is gigabytes of syntax tree that the checker
// then ignores. Blanking the bodies before parsing makes that cost a few
// megabytes and the standard library closure several times faster to load.
//
// It is applied to dependencies only, never to a repository file: for those
// the bodies are the point.
//
// A function literal in a package-level initialiser is left alone (its `func`
// is not at the start of a declaration), and so is anything this scanner does
// not recognise: in the worst case a body is kept, which costs memory and
// nothing else. A wrong cut would at worst produce a parse error in a
// dependency, which degrades that package's declarations to missing, never to
// wrong.
func stripBodies(src []byte) []byte {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, src, func(token.Position, string) {}, 0)

	type cut struct{ from, to int }
	var cuts []cut

	const (
		outside = iota
		signature
		body
	)
	var (
		state     = outside
		atStart   = true
		prev      = token.ILLEGAL
		depth     int // parentheses and brackets inside a signature
		typeBrace int // braces of struct and interface types inside a signature
		bodyDepth int
		bodyFrom  int
		topDepth  int // nesting outside any function declaration
	)
	for {
		pos, tok, _ := s.Scan()
		if tok == token.EOF {
			break
		}
		off := file.Offset(pos)
		switch state {
		case outside:
			switch tok {
			case token.LPAREN, token.LBRACK, token.LBRACE:
				topDepth++
			case token.RPAREN, token.RBRACK, token.RBRACE:
				if topDepth > 0 {
					topDepth--
				}
			case token.FUNC:
				if atStart && topDepth == 0 {
					state, depth, typeBrace = signature, 0, 0
				}
			}
		case signature:
			switch tok {
			case token.LPAREN, token.LBRACK:
				depth++
			case token.RPAREN, token.RBRACK:
				depth--
			case token.LBRACE:
				switch {
				case depth > 0 || typeBrace > 0 || prev == token.STRUCT || prev == token.INTERFACE:
					typeBrace++
				default:
					state, bodyDepth, bodyFrom = body, 1, off
				}
			case token.RBRACE:
				if typeBrace > 0 {
					typeBrace--
				}
			case token.SEMICOLON:
				if depth <= 0 && typeBrace == 0 {
					state = outside
				}
			}
		case body:
			switch tok {
			case token.LBRACE:
				bodyDepth++
			case token.RBRACE:
				bodyDepth--
				if bodyDepth == 0 {
					cuts = append(cuts, cut{from: bodyFrom + 1, to: off})
					state = outside
				}
			}
		}
		atStart = tok == token.SEMICOLON
		prev = tok
	}
	if len(cuts) == 0 {
		return src
	}
	out := make([]byte, len(src))
	copy(out, src)
	for _, c := range cuts {
		for i := c.from; i < c.to && i < len(out); i++ {
			if out[i] != '\n' {
				out[i] = ' '
			}
		}
	}
	return out
}
