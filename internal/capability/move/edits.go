package move

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/token"
	"sort"
	"strings"
)

// The edits are made on whole lines of the files as written, at positions the
// syntax tree gives. Nothing is re-printed from the tree: a file's
// formatting, comments and blank lines outside the lines that move are the
// bytes they were. Only a file that was gofmt-formatted to begin with is
// passed through gofmt afterwards, which then touches nothing but what the
// edit disturbed — the order of an import group, the alignment of a
// declaration group.

// cutSource returns the source without the declaration and without the
// imports it alone used, and the lines it removed for the imports.
func cutSource(src *parsed, t *located, imp *importPlan) (out []byte, importCuts []span, err error) {
	var cuts []span
	removed := make(map[*ast.ImportSpec]bool, len(imp.remove))
	for _, r := range imp.remove {
		removed[r.spec] = true
	}
	seen := make(map[*ast.GenDecl]bool)
	for _, r := range imp.remove {
		if seen[r.decl] {
			continue
		}
		seen[r.decl] = true
		all := true
		for _, s := range r.decl.Specs {
			if is, ok := s.(*ast.ImportSpec); !ok || !removed[is] {
				all = false
			}
		}
		if all {
			// Every import of the declaration goes: the declaration goes.
			start := r.decl.Pos()
			if r.decl.Doc != nil {
				start = r.decl.Doc.Pos()
			}
			c, err := src.wholeLines(start, r.decl.End(), "the import declaration")
			if err != nil {
				return nil, nil, err
			}
			cuts = append(cuts, c)
			continue
		}
		for _, s := range r.decl.Specs {
			is, ok := s.(*ast.ImportSpec)
			if !ok || !removed[is] {
				continue
			}
			start := is.Pos()
			if is.Doc != nil {
				start = is.Doc.Pos()
			}
			c, err := src.wholeLines(start, is.End(), "the import of "+is.Path.Value)
			if err != nil {
				return nil, nil, err
			}
			cuts = append(cuts, c)
		}
	}
	importCuts = append([]span(nil), cuts...)
	out = cutLines(src, append(cuts, t.cut))
	if src.clean {
		if out, err = gofmt(src, out); err != nil {
			return nil, nil, err
		}
	}
	return out, importCuts, nil
}

// gofmt formats an edited file that was formatted before the edit.
func gofmt(p *parsed, out []byte) ([]byte, error) {
	formatted, err := format.Source(out)
	if err != nil {
		return nil, refuse(p.path, 0, "internal check failed: the edited file does not format (%v). "+
			"Nothing was written; this is a defect in graphlume move, please report it", err)
	}
	return formatted, nil
}

// cutLines removes the given lines and tidies the blank lines each cut
// leaves behind: never two in a row where the cut was, none directly inside
// a group's parentheses, none at the end of the file.
func cutLines(p *parsed, cuts []span) []byte {
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].first < cuts[j].first })
	var (
		kept  [][]byte
		joins []int // indexes into kept at which a cut was made
		next  int
	)
	for i := range p.lines {
		for next < len(cuts) && cuts[next].last < i {
			next++
		}
		if next < len(cuts) && i >= cuts[next].first && i <= cuts[next].last {
			if i == cuts[next].first {
				joins = append(joins, len(kept))
			}
			continue
		}
		start, _, end := p.lineBounds(i)
		kept = append(kept, p.src[start:end])
	}
	for i := len(joins) - 1; i >= 0; i-- {
		kept = tidy(kept, joins[i])
	}
	return bytes.Join(kept, nil)
}

func blank(line []byte) bool { return len(bytes.TrimSpace(line)) == 0 }

// opensGroup: the line is "var (", "const (", "type (" or "import (".
func opensGroup(line []byte) bool {
	f := strings.Fields(string(line))
	if len(f) < 2 || f[1] != "(" || (len(f) > 2 && !strings.HasPrefix(f[2], "//")) {
		return false
	}
	switch f[0] {
	case "var", "const", "type", "import":
		return true
	}
	return false
}

func closesGroup(line []byte) bool { return bytes.HasPrefix(bytes.TrimSpace(line), []byte(")")) }

// tidy removes the surplus blank lines around join j, the index of the first
// kept line after a cut.
func tidy(kept [][]byte, j int) [][]byte {
	drop := func(i int) { kept = append(kept[:i], kept[i+1:]...) }
	// A later join's tidying may already have removed lines at or after this
	// one (two cuts with only blank lines between them and the end of file).
	j = min(j, len(kept))
	for j > 0 {
		switch {
		case j >= len(kept): // the cut reached the end of the file
			if !blank(kept[j-1]) {
				return kept
			}
			drop(j - 1)
			j--
		case blank(kept[j]) && (blank(kept[j-1]) || opensGroup(kept[j-1])):
			drop(j)
		case blank(kept[j-1]) && closesGroup(kept[j]):
			drop(j - 1)
			j--
		default:
			return kept
		}
	}
	return kept
}

// extendTarget returns the target with the imports the moved code needs and
// the declaration appended.
func extendTarget(dst *parsed, t *located, imp *importPlan) ([]byte, error) {
	inserts, err := importInserts(dst, imp.add)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	insert := func(lines []string) {
		if len(lines) > 0 && b.Len() > 0 && !bytes.HasSuffix(b.Bytes(), []byte("\n")) {
			b.WriteString(dst.eol) // the file's last line had no terminator
		}
		for _, l := range lines {
			b.WriteString(l + dst.eol)
		}
	}
	for i := range dst.lines {
		insert(inserts[i])
		start, _, end := dst.lineBounds(i)
		b.Write(dst.src[start:end]) // with the terminator it has
	}
	insert(inserts[len(dst.lines)])
	out := append(bytes.TrimRight(b.Bytes(), " \t\r\n"), dst.eol+dst.eol...)
	out = append(out, strings.ReplaceAll(t.text, "\n", dst.eol)...)
	out = append(out, dst.eol...)
	if dst.clean {
		return gofmt(dst, out)
	}
	return out, nil
}

// importInserts returns the lines to put before each line index of the
// target so that it imports every spec in add.
func importInserts(dst *parsed, add []importSpec) (map[int][]string, error) {
	inserts := make(map[int][]string)
	if len(add) == 0 {
		return inserts, nil
	}
	sort.Slice(add, func(i, j int) bool { return add[i].path < add[j].path })

	var decls []*ast.GenDecl
	for _, d := range dst.ast.Decls {
		if gen, ok := d.(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
			decls = append(decls, gen)
		}
	}

	// A parenthesised import declaration laid out one import per line: each
	// new import goes after the last existing one of its kind (standard
	// library, or not), so a file that keeps the two apart stays that way.
	for _, gen := range decls {
		if !gen.Lparen.IsValid() || len(gen.Specs) == 0 || !onOwnLines(dst, gen) {
			continue
		}
		var lastStd, lastOther = -1, -1
		for _, s := range gen.Specs {
			is, ok := s.(*ast.ImportSpec)
			if !ok {
				continue
			}
			line := dst.lineIndex(dst.offset(is.End() - 1))
			if stdLike(strings.Trim(is.Path.Value, "\"`")) {
				lastStd = line
			} else {
				lastOther = line
			}
		}
		first := gen.Specs[0].Pos()
		if is, ok := gen.Specs[0].(*ast.ImportSpec); ok && is.Doc != nil {
			first = is.Doc.Pos()
		}
		firstLine := dst.lineIndex(dst.offset(first))
		closeLine := dst.lineIndex(dst.offset(gen.Rparen))
		var newStd, newOther []string
		for _, a := range add {
			if stdLike(a.path) {
				newStd = append(newStd, "\t"+a.text())
			} else {
				newOther = append(newOther, "\t"+a.text())
			}
		}
		switch {
		case len(newStd) == 0:
		case lastStd >= 0:
			inserts[lastStd+1] = append(inserts[lastStd+1], newStd...)
		default: // the first standard-library import: its own group, on top
			inserts[firstLine] = append(append(inserts[firstLine], newStd...), "")
		}
		switch {
		case len(newOther) == 0:
		case lastOther >= 0:
			inserts[lastOther+1] = append(inserts[lastOther+1], newOther...)
		default: // the first import from outside it: its own group, at the bottom
			inserts[closeLine] = append(append(inserts[closeLine], ""), newOther...)
		}
		return inserts, nil
	}

	lines := importDecl(add)

	// Other import declarations: a new one after the last of them.
	if len(decls) > 0 {
		last := decls[len(decls)-1]
		c, err := dst.wholeLines(last.Pos(), last.End(), "the last import declaration")
		if err != nil {
			return nil, err
		}
		inserts[c.last+1] = lines
		return inserts, nil
	}

	// No imports at all: a declaration after the package clause, set off by
	// blank lines.
	c, err := dst.wholeLines(dst.ast.Package, dst.ast.Name.End(), "the package clause")
	if err != nil {
		return nil, err
	}
	at := c.last + 1
	lines = append([]string{""}, lines...)
	if at < len(dst.lines) {
		if start, textEnd, _ := dst.lineBounds(at); !blank(dst.src[start:textEnd]) {
			lines = append(lines, "")
		}
	}
	inserts[at] = lines
	return inserts, nil
}

// onOwnLines reports whether a parenthesised declaration has its parentheses
// and each of its specs on lines of their own.
func onOwnLines(p *parsed, gen *ast.GenDecl) bool {
	prev := p.lineIndex(p.offset(gen.Lparen))
	for _, s := range gen.Specs {
		start := s.Pos()
		if is, ok := s.(*ast.ImportSpec); ok && is.Doc != nil {
			start = is.Doc.Pos()
		}
		if p.lineIndex(p.offset(start)) <= prev {
			return false
		}
		prev = p.lineIndex(p.offset(s.End() - 1))
	}
	return p.lineIndex(p.offset(gen.Rparen)) > prev
}

// importDecl returns the lines of one import declaration for the given
// specs, sorted by path: a single import on one line, several in a block with
// the standard library first and the rest after a blank line.
func importDecl(add []importSpec) []string {
	if len(add) == 1 {
		return []string{"import " + add[0].text()}
	}
	var std, other []string
	for _, a := range add {
		if stdLike(a.path) {
			std = append(std, "\t"+a.text())
		} else {
			other = append(other, "\t"+a.text())
		}
	}
	lines := append([]string{"import ("}, std...)
	if len(std) > 0 && len(other) > 0 {
		lines = append(lines, "")
	}
	return append(append(lines, other...), ")")
}

// stdLike: the path's first element has no dot, the convention import
// grouping follows. It decides only where a line is placed, never whether an
// import is needed.
func stdLike(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

// newTarget returns a new file for the declaration: the source's build
// constraint and package clause, the imports the moved code needs, the
// declaration.
func newTarget(src *parsed, t *located, imp *importPlan) ([]byte, error) {
	var b strings.Builder
	expr, err := goBuildLine(src)
	if err != nil {
		return nil, err
	}
	if expr != "" {
		b.WriteString("//go:build " + expr + "\n\n")
	}
	b.WriteString("package " + src.ast.Name.Name + "\n\n")

	add := append([]importSpec(nil), imp.add...)
	sort.Slice(add, func(i, j int) bool { return add[i].path < add[j].path })
	if len(add) > 0 {
		b.WriteString(strings.Join(importDecl(add), "\n") + "\n\n")
	}
	b.WriteString(t.text + "\n")

	out := []byte(b.String())
	if src.clean {
		// The declaration came out of a formatted file, so the new file is
		// formatted. One taken from an unformatted file keeps its author's
		// formatting, as it would have in an existing target.
		if out, err = gofmt(src, out); err != nil {
			return nil, err
		}
	}
	if src.eol != "\n" {
		out = bytes.ReplaceAll(out, []byte("\n"), []byte(src.eol))
	}
	return out, nil
}
