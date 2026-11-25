package move

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/scanner"
	"go/token"
	"sort"
	"strings"
)

// selfCheck proves the two new files are the two old files with exactly the
// planned difference, and returns the sites of that difference.
//
// It does not trust the editing code above. It scans all four files into
// tokens and requires that, import declarations aside:
//
//   - the source's tokens after the move are its tokens before, less the
//     lines that were cut, in the same order;
//   - the target's tokens after are its tokens before followed by the
//     declaration's;
//   - the declaration's tokens in the target are the tokens cut from the
//     source (with the group's keyword in front, for a member of a group);
//   - no comment was lost or invented. Where gofmt formats the declaration
//     it may put its doc comment into standard form, so there the comments
//     are compared as gofmt writes them;
//
// and that each file's imports are its old imports with exactly the planned
// additions or removals. A failure here is a defect in this package, never
// the user's code, and it stops the move before anything is written.
func selfCheck(req Request, src, dst *parsed, t *located, imp *importPlan, importCuts []span, sourceAfter, targetAfter []byte) ([]Site, error) {
	internal := func(file, format string, args ...any) error {
		return refuse(file, 0, "internal check failed: %s. Nothing was written; this is a defect in "+
			"graphlume move, please report it", fmt.Sprintf(format, args...))
	}
	fset := token.NewFileSet()
	sa, err := parseFile(fset, req.From, sourceAfter)
	if err != nil {
		return nil, internal(req.From, "the source would no longer parse (%v)", err)
	}
	ta, err := parseFile(fset, req.To, targetAfter)
	if err != nil {
		return nil, internal(req.To, "the target would not parse (%v)", err)
	}
	if sa.ast.Name.Name != src.ast.Name.Name || ta.ast.Name.Name != src.ast.Name.Name {
		return nil, internal(req.To, "a package clause changed")
	}

	// The cut lines hold the declaration and nothing else; the lines cut for
	// the imports hold no code at all once import declarations are set aside.
	within := func(spans []span) func(off int) bool {
		return func(off int) bool {
			line := src.lineIndex(off)
			for _, c := range spans {
				if line >= c.first && line <= c.last {
					return true
				}
			}
			return false
		}
	}
	inMoved, inImportCut := within([]span{t.cut}), within(importCuts)
	movedCode, movedComments := scan(src, inMoved)
	from, to := src.offset(t.node.Pos()), src.offset(t.node.End())
	if nodeCode, _ := scan(src, func(off int) bool { return off >= from && off < to }); !equal(movedCode, nodeCode) {
		return nil, internal(req.From, "the lines to cut hold more than the declaration")
	}
	if stray, _ := scan(src, inImportCut); len(stray) != 0 {
		return nil, internal(req.From, "the lines to cut for the imports hold other code")
	}

	// The source: what was there, less the cut lines.
	srcCode, srcComments := scan(src, func(off int) bool { return !inMoved(off) && !inImportCut(off) })
	saCode, saComments := scan(sa, nil)
	if !equal(srcCode, saCode) {
		return nil, internal(req.From, "the source's code outside the moved declaration would change")
	}
	if !sameSet(srcComments, saComments) {
		return nil, internal(req.From, "a comment in the source would be lost or duplicated")
	}

	// The declaration as assembled from the cut lines: every token and every
	// comment of those lines, the group's keyword in front for a member.
	asmCode, asmComments := scanText(t.assembled)
	want := movedCode
	if t.group != nil {
		keyword, _ := scanText(t.tok.String())
		want = append(keyword, movedCode...)
	}
	if !equal(asmCode, want) || !sameSet(asmComments, movedComments) {
		return nil, internal(req.From, "the declaration would not arrive as it left")
	}
	// The declaration as written: the same code, and the same comments once
	// both are in gofmt's form.
	textCode, textComments := scanText(t.text)
	canonAsm, err1 := gofmtComments(t.assembled)
	canonText, err2 := gofmtComments(t.text)
	if err1 != nil || err2 != nil || !equal(textCode, asmCode) || !sameSet(canonAsm, canonText) {
		return nil, internal(req.From, "the declaration would not be written as it was assembled")
	}
	// What arrives in the target: gofmt's form where the target is formatted
	// after the edit, the text as written where it is not.
	if formatted := (dst != nil && dst.clean) || (dst == nil && src.clean); formatted {
		textComments = canonText
	}

	// The target: what was there, then the declaration.
	var dstCode, dstComments []string
	if dst != nil {
		dstCode, dstComments = scan(dst, nil)
	} else {
		dstCode, _ = scanText("package " + src.ast.Name.Name)
		if expr, _ := goBuildLine(src); expr != "" {
			dstComments = []string{"//go:build " + expr}
		}
	}
	taCode, taComments := scan(ta, nil)
	if !equal(taCode, append(append([]string(nil), dstCode...), textCode...)) {
		return nil, internal(req.To, "the target's existing code would change")
	}
	if !sameSet(taComments, append(append([]string(nil), dstComments...), textComments...)) {
		return nil, internal(req.To, "a comment in the target would be lost or duplicated")
	}

	// The imports.
	wantSrc := importSet(src)
	for _, r := range imp.remove {
		wantSrc[r.text()]--
	}
	if !sameCounts(wantSrc, importSet(sa)) {
		return nil, internal(req.From, "the source's imports would not be the planned ones")
	}
	wantDst := map[string]int{}
	if dst != nil {
		wantDst = importSet(dst)
	}
	for _, a := range imp.add {
		wantDst[a.text()]++
	}
	if !sameCounts(wantDst, importSet(ta)) {
		return nil, internal(req.To, "the target's imports would not be the planned ones")
	}

	// Build constraints, once more on the result.
	before, _ := constraintOf(src)
	if after, err := constraintOf(sa); err != nil || after != before {
		return nil, internal(req.From, "the source's build constraint would change")
	}
	if after, err := constraintOf(ta); err != nil || after != before {
		return nil, internal(req.To, "the target would not be built under the source's constraint")
	}

	// The declaration is in the target once and in the source no more.
	if n := len(declSpans(sa, t)); n != 0 {
		return nil, internal(req.From, "%s would still be declared in the source", t.label())
	}
	arrived := declSpans(ta, t)
	if len(arrived) != 1 {
		return nil, internal(req.To, "%s would be declared %d times in the target", t.label(), len(arrived))
	}

	return sites(req, src, ta, t, imp, arrived[0], dst == nil), nil
}

// scan returns a file's code tokens and its comments, import declarations
// left out. keep, when not nil, selects tokens by byte offset.
//
// Tokens are compared, not text, so that formatting cannot produce a false
// difference; the three things gofmt itself may change in a token are
// normalised away — automatic and redundant semicolons, the case of a number
// literal's prefix and exponent, and white space inside a comment.
func scan(p *parsed, keep func(off int) bool) (code, comments []string) {
	type rng struct{ from, to int }
	var imports []rng
	for _, d := range p.ast.Decls {
		if gen, ok := d.(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
			imports = append(imports, rng{p.offset(gen.Pos()), p.offset(gen.End())})
		}
	}
	return scanBytes(p.src, func(off int, comment bool) bool {
		if keep != nil && !keep(off) {
			return false
		}
		if comment {
			return true // a comment inside an import declaration is still a comment to account for
		}
		for _, r := range imports {
			if off >= r.from && off < r.to {
				return false
			}
		}
		return true
	})
}

// gofmtComments returns the comments of a top-level declaration as gofmt
// writes them: a doc comment in its standard form, the rest as they are.
func gofmtComments(decl string) ([]string, error) {
	out, err := format.Source([]byte("package p\n\n" + decl + "\n"))
	if err != nil {
		return nil, err
	}
	_, comments := scanText(string(out))
	return comments, nil
}

func scanText(text string) (code, comments []string) {
	return scanBytes([]byte(text), func(int, bool) bool { return true })
}

func scanBytes(src []byte, keep func(off int, comment bool) bool) (code, comments []string) {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, src, func(token.Position, string) {}, scanner.ScanComments)
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			return code, comments
		}
		off := file.Offset(pos)
		switch {
		case tok == token.SEMICOLON:
		case tok == token.COMMENT:
			if keep(off, true) {
				comments = append(comments, normalComment(lit))
			}
		case !keep(off, false):
		case tok == token.INT || tok == token.FLOAT || tok == token.IMAG:
			code = append(code, tok.String()+":"+strings.ToLower(lit))
		case lit != "":
			code = append(code, tok.String()+":"+lit)
		default:
			code = append(code, tok.String())
		}
	}
}

// normalComment drops the white space gofmt is free to change: at the end of
// any line, and at the start of the continuation lines of a block comment.
func normalComment(lit string) string {
	lines := strings.Split(strings.ReplaceAll(lit, "\r", ""), "\n")
	for i, l := range lines {
		l = strings.TrimRight(l, " \t")
		if i > 0 {
			l = strings.TrimLeft(l, " \t")
		}
		lines[i] = l
	}
	return strings.Join(lines, "\n")
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameSet(a, b []string) bool {
	a, b = append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(a)
	sort.Strings(b)
	return equal(a, b)
}

func importSet(p *parsed) map[string]int {
	out := make(map[string]int)
	for _, s := range importsOf(p, nil) {
		out[s.text()]++
	}
	return out
}

func sameCounts(a, b map[string]int) bool {
	for k, n := range a {
		if n != 0 && b[k] != n {
			return false
		}
	}
	for k, n := range b {
		if n != 0 && a[k] != n {
			return false
		}
	}
	return true
}

// declSpans returns the lines of every declaration of t's name in p,
// counted the way locate counts them.
func declSpans(p *parsed, t *located) []span {
	var out []span
	add := func(doc *ast.CommentGroup, from, to token.Pos) {
		if doc != nil {
			from = doc.Pos()
		}
		out = append(out, span{p.lineIndex(p.offset(from)), p.lineIndex(p.offset(to - 1))})
	}
	for _, d := range p.ast.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Name.Name == t.decl.Name && recvBase(d) == t.decl.Receiver && t.tok == token.ILLEGAL {
				add(d.Doc, d.Pos(), d.End())
			}
		case *ast.GenDecl:
			if t.tok == token.ILLEGAL || d.Tok != t.tok {
				continue
			}
			for _, s := range d.Specs {
				for _, id := range specNames(s) {
					if id.Name == t.decl.Name {
						add(d.Doc, d.Pos(), d.End())
					}
				}
			}
		}
	}
	return out
}

// sites lists what changes, the target first: that is the order the files
// are written in.
func sites(req Request, src, ta *parsed, t *located, imp *importPlan, arrived span, created bool) []Site {
	var out []Site
	if created {
		detail := "new file, package " + src.ast.Name.Name
		if expr, _ := goBuildLine(src); expr != "" {
			detail += ", //go:build " + expr
		}
		out = append(out, Site{Path: req.To, Line: 1, Action: ActionCreateFile, Detail: detail})
	}
	var added []Site
	for _, a := range imp.add {
		for _, s := range importsOf(ta, nil) {
			if s.text() == a.text() {
				added = append(added, Site{Path: req.To, Line: ta.line(s.spec.Pos()), Action: ActionAddImport,
					Detail: a.describe() + ", which the moved code uses"})
				break
			}
		}
	}
	sort.Slice(added, func(i, j int) bool { return added[i].Line < added[j].Line })
	out = append(out, added...)

	what := t.label()
	if hasDoc(t) {
		what += " with its doc comment"
	}
	out = append(out,
		Site{Path: req.To, Line: arrived.first + 1, EndLine: arrived.last + 1, Action: ActionInsert, Detail: what},
		Site{Path: req.From, Line: t.cut.first + 1, EndLine: t.cut.last + 1, Action: ActionRemove, Detail: what})

	var removed []Site
	for _, r := range imp.remove {
		removed = append(removed, Site{Path: req.From, Line: src.line(r.spec.Pos()), Action: ActionRemoveImport,
			Detail: r.describe() + ", which nothing else in this file uses"})
	}
	sort.Slice(removed, func(i, j int) bool { return removed[i].Line < removed[j].Line })
	return append(out, removed...)
}

func hasDoc(t *located) bool {
	switch n := t.node.(type) {
	case *ast.FuncDecl:
		return n.Doc != nil
	case *ast.GenDecl:
		return n.Doc != nil
	case ast.Spec:
		return specDoc(n) != nil
	}
	return false
}
