package move

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/scanner"
	"go/token"
	"sort"
	"strings"
)

// parsed is one file: its bytes, its lines and its syntax tree.
//
// The tree is built with the parser's identifier resolution left on. That
// resolution is file-local and lexical, which is exactly what the import
// analysis needs: an identifier it leaves unresolved is one the file does not
// declare, so it is the package's, an import's, or the universe's.
type parsed struct {
	path  string
	src   []byte
	ast   *ast.File
	tf    *token.File
	lines []int // lines[i] is the byte offset at which line i (0-indexed) starts
	eol   string
	// clean: the file was gofmt-formatted as it stood, so the edited file is
	// formatted too. A file that was not is edited in place and left as its
	// author formatted it.
	clean bool
}

func parseFile(fset *token.FileSet, path string, src []byte) (*parsed, error) {
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, refuse(path, errorLine(err), "does not parse as Go, so nothing in it can be located exactly: %s", firstError(err))
	}
	p := &parsed{path: path, src: src, ast: f, tf: fset.File(f.Pos()), lines: []int{0}, eol: "\n"}
	for i, b := range src {
		if b == '\n' && i+1 < len(src) {
			p.lines = append(p.lines, i+1)
		}
	}
	if i := bytes.IndexByte(src, '\n'); i > 0 && src[i-1] == '\r' {
		p.eol = "\r\n"
	}
	return p, nil
}

// noteFormatting records whether the file is gofmt-formatted as it stands.
// Only the two files being edited need it; the package's other files are
// parsed for their declarations alone.
func (p *parsed) noteFormatting() {
	out, err := format.Source(p.src)
	p.clean = err == nil && bytes.Equal(out, p.src)
}

func firstError(err error) string {
	if list := (scanner.ErrorList)(nil); errors.As(err, &list) && len(list) > 0 {
		return list[0].Msg
	}
	return err.Error()
}

func errorLine(err error) int {
	if list := (scanner.ErrorList)(nil); errors.As(err, &list) && len(list) > 0 {
		return list[0].Pos.Line
	}
	return 0
}

// offset is pos as a byte offset into src. It ignores //line directives,
// which is what cutting bytes needs.
func (p *parsed) offset(pos token.Pos) int { return p.tf.Offset(pos) }

// lineIndex is the 0-indexed line holding byte offset off.
func (p *parsed) lineIndex(off int) int {
	return sort.Search(len(p.lines), func(i int) bool { return p.lines[i] > off }) - 1
}

// line is pos as a 1-indexed line of the file as written.
func (p *parsed) line(pos token.Pos) int { return p.lineIndex(p.offset(pos)) + 1 }

// lineBounds returns the byte range of line i, with and without its
// terminator.
func (p *parsed) lineBounds(i int) (start, textEnd, end int) {
	start = p.lines[i]
	end = len(p.src)
	if i+1 < len(p.lines) {
		end = p.lines[i+1]
	}
	textEnd = end
	for textEnd > start && (p.src[textEnd-1] == '\n' || p.src[textEnd-1] == '\r') {
		textEnd--
	}
	return start, textEnd, end
}

// span is a run of whole lines, 0-indexed, inclusive.
type span struct{ first, last int }

// wholeLines returns the lines from pos to end (exclusive), provided the node
// has them to itself: nothing but white space before it on its first line,
// and nothing but white space or a // comment after it on its last. A node
// that shares a line with other code cannot be cut out by lines, and is
// refused rather than cut by a guess at where it stops.
func (p *parsed) wholeLines(pos, end token.Pos, what string) (span, error) {
	from, to := p.offset(pos), p.offset(end)
	first := p.lineIndex(from)
	last := p.lineIndex(max(from, to-1))
	start, _, _ := p.lineBounds(first)
	if len(bytes.TrimSpace(p.src[start:from])) != 0 {
		return span{}, refuse(p.path, first+1, "%s shares its first line with other code; split the line, then try again", what)
	}
	_, textEnd, _ := p.lineBounds(last)
	if rest := bytes.TrimSpace(p.src[to:textEnd]); len(rest) != 0 && !bytes.HasPrefix(rest, []byte("//")) {
		return span{}, refuse(p.path, last+1, "%s is followed by other code or a /* */ comment on its last line; "+
			"split the line, then try again", what)
	}
	return span{first, last}, nil
}

// text is the lines of s without the final terminator, with every line
// ending normalised to "\n".
func (p *parsed) text(s span) string {
	start, _, _ := p.lineBounds(s.first)
	_, textEnd, _ := p.lineBounds(s.last)
	return strings.ReplaceAll(string(p.src[start:textEnd]), "\r\n", "\n")
}

// located is the declaration to move, found.
type located struct {
	decl Decl
	// node is what moves: a *ast.FuncDecl, a whole *ast.GenDecl, or one spec
	// of a grouped declaration (then group is the declaration it leaves).
	node  ast.Node
	group *ast.GenDecl
	tok   token.Token // CONST, VAR, TYPE; ILLEGAL for a function
	cut   span        // the lines that leave the source
	// text is the declaration as it is written into the target: complete,
	// top-level, "\n" line endings, no final newline.
	text string
	// assembled is text before any formatting: the cut lines themselves,
	// with the group's keyword in front for a member of a group. For a
	// declaration that moves whole it is text.
	assembled string
	// values are a moved variable's initialisers; empty for anything else.
	values []ast.Expr
}

// label names the declaration for a message.
func (t *located) label() string {
	if t.decl.Receiver != "" {
		return fmt.Sprintf("method %s.%s", t.decl.Receiver, t.decl.Name)
	}
	return fmt.Sprintf("%s %s", t.decl.Kind, t.decl.Name)
}

// recvBase is the name of a method's receiver type, "" for a function.
func recvBase(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return ""
	}
	t := fd.Recv.List[0].Type
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
		case *ast.ParenExpr:
			t = x.X
		case *ast.IndexExpr:
			t = x.X
		case *ast.IndexListExpr:
			t = x.X
		case *ast.Ident:
			return x.Name
		default:
			return "?"
		}
	}
}

func specNames(s ast.Spec) []*ast.Ident {
	switch s := s.(type) {
	case *ast.TypeSpec:
		return []*ast.Ident{s.Name}
	case *ast.ValueSpec:
		return s.Names
	}
	return nil
}

// candidate is one declaration of the requested name.
type candidate struct {
	fn   *ast.FuncDecl
	gen  *ast.GenDecl
	spec ast.Spec
	pos  token.Pos
}

// locate finds the one declaration the request names, and refuses when the
// file gives no single answer.
func locate(src *parsed, name, recv string) (*located, error) {
	var (
		found   []candidate
		methods []string // receivers of methods with this name, for the hint
	)
	for _, d := range src.ast.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Name.Name != name {
				continue
			}
			base := recvBase(d)
			if base != "" && recv == "" {
				methods = append(methods, base)
			}
			if base == recv {
				found = append(found, candidate{fn: d, pos: d.Pos()})
			}
		case *ast.GenDecl:
			if recv != "" || d.Tok == token.IMPORT {
				continue
			}
			for _, s := range d.Specs {
				for _, id := range specNames(s) {
					if id.Name == name {
						found = append(found, candidate{gen: d, spec: s, pos: id.Pos()})
					}
				}
			}
		}
	}

	switch {
	case len(found) == 0 && recv != "":
		return nil, refuse(src.path, 0, "no method %s with receiver type %s is declared in this file", name, recv)
	case len(found) == 0 && len(methods) > 0:
		sort.Strings(methods)
		return nil, refuse(src.path, 0, "%s is declared here only as a method (of %s). A method moves only when asked "+
			"for by its receiver: move %s.%s", name, strings.Join(methods, ", "), methods[0], name)
	case len(found) == 0:
		return nil, refuse(src.path, 0, "no top-level declaration named %s in this file", name)
	case len(found) > 1:
		lines := make([]string, len(found))
		for i, c := range found {
			lines[i] = fmt.Sprint(src.line(c.pos))
		}
		return nil, refuse(src.path, src.line(found[0].pos), "%s is declared %d times in this file (lines %s), "+
			"so the name does not say which declaration to move", name, len(found), strings.Join(lines, ", "))
	}

	c := found[0]
	var (
		t   *located
		err error
	)
	if c.fn != nil {
		t, err = locateFunc(src, c.fn)
	} else {
		t, err = locateSpec(src, c.gen, c.spec, name)
	}
	if err != nil {
		return nil, err
	}
	t.decl.Path = src.path
	t.decl.Line, t.decl.EndLine = t.cut.first+1, t.cut.last+1
	if err := checkDirectives(src, t); err != nil {
		return nil, err
	}
	return t, nil
}

func locateFunc(src *parsed, fn *ast.FuncDecl) (*located, error) {
	t := &located{node: fn, decl: Decl{Name: fn.Name.Name, Kind: "func", Receiver: recvBase(fn)}}
	if t.decl.Receiver != "" {
		t.decl.Kind = "method"
	}
	start := fn.Pos()
	if fn.Doc != nil {
		start = fn.Doc.Pos()
	}
	var err error
	if t.cut, err = src.wholeLines(start, fn.End(), t.label()); err != nil {
		return nil, err
	}
	t.text = src.text(t.cut)
	t.assembled = t.text
	return t, nil
}

func locateSpec(src *parsed, gen *ast.GenDecl, spec ast.Spec, name string) (*located, error) {
	t := &located{tok: gen.Tok, decl: Decl{Name: name, Kind: gen.Tok.String()}}
	if vs, ok := spec.(*ast.ValueSpec); ok {
		if len(vs.Names) > 1 {
			var others []string
			for _, id := range vs.Names {
				if id.Name != name {
					others = append(others, id.Name)
				}
			}
			return nil, refuse(src.path, src.line(vs.Pos()), "%s is declared in one statement with %s; "+
				"a name cannot be moved out of a shared declaration. Split the statement, then try again",
				name, strings.Join(others, ", "))
		}
		if gen.Tok == token.VAR {
			t.values = vs.Values
		}
	}

	// Not grouped, or the only member of its group: the whole declaration
	// moves, byte for byte.
	if !gen.Lparen.IsValid() || len(gen.Specs) == 1 {
		t.node = gen
		start := gen.Pos()
		if gen.Doc != nil {
			start = gen.Doc.Pos()
		}
		var err error
		if t.cut, err = src.wholeLines(start, gen.End(), t.label()); err != nil {
			return nil, err
		}
		t.text = src.text(t.cut)
		t.assembled = t.text
		return t, nil
	}

	// One member of a group. A constant group is a unit whenever a member's
	// value depends on its position: iota counts the members above it, and a
	// member with no value repeats the one above.
	if gen.Tok == token.CONST {
		if why := positional(gen); why != "" {
			return nil, refuse(src.path, src.line(spec.Pos()), "%s is part of a constant group in which %s, so "+
				"taking a member out would change the values of the others. Move the whole group by hand", name, why)
		}
	}
	t.node, t.group = spec, gen
	start := spec.Pos()
	if doc := specDoc(spec); doc != nil {
		start = doc.Pos()
	}
	var err error
	if t.cut, err = src.wholeLines(start, spec.End(), t.label()); err != nil {
		return nil, err
	}
	// The member becomes a declaration of its own: its doc comment, then the
	// group's keyword in front of the member exactly as written. It was
	// indented one level inside the group; gofmt of this one declaration
	// removes that. gofmt also puts a doc comment that is now a top-level one
	// into its standard form (a trailing empty "//" line goes, a list is
	// re-indented), as it would the first time the file was formatted.
	//
	// A doc comment ends on the line above the member (that is what makes it
	// one), and wholeLines has just established that nothing but white space
	// precedes the cut's first line; so the lines above the member's own are
	// its doc comment, whole.
	specOff := src.offset(spec.Pos())
	specLine := src.lineIndex(specOff)
	doc := ""
	if t.cut.first < specLine {
		doc = strings.TrimLeft(src.text(span{t.cut.first, specLine - 1}), " \t") + "\n"
	}
	_, textEnd, _ := src.lineBounds(t.cut.last)
	body := strings.ReplaceAll(string(src.src[specOff:textEnd]), "\r\n", "\n")
	t.assembled = doc + gen.Tok.String() + " " + body
	out, ferr := format.Source([]byte(t.assembled))
	if ferr != nil {
		return nil, refuse(src.path, src.line(spec.Pos()), "%s could not be rewritten as a declaration of its own (%v)", name, ferr)
	}
	t.text = strings.TrimRight(string(out), "\n")
	return t, nil
}

func specDoc(s ast.Spec) *ast.CommentGroup {
	switch s := s.(type) {
	case *ast.TypeSpec:
		return s.Doc
	case *ast.ValueSpec:
		return s.Doc
	}
	return nil
}

// positional reports why the values in a constant group depend on the
// members' positions, or "" when they do not.
func positional(gen *ast.GenDecl) string {
	for _, s := range gen.Specs {
		vs, ok := s.(*ast.ValueSpec)
		if !ok {
			continue
		}
		if len(vs.Values) == 0 {
			return "a member takes its value from the one above it"
		}
		usesIota := false
		for _, v := range vs.Values {
			ast.Inspect(v, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && id.Name == "iota" {
					usesIota = true
				}
				return !usesIota
			})
		}
		if usesIota {
			return "iota numbers the members"
		}
	}
	return ""
}

// checkDirectives refuses a declaration carrying a compiler or tool directive
// that binds it to the file it is in.
func checkDirectives(src *parsed, t *located) error {
	first, _, _ := src.lineBounds(t.cut.first)
	_, _, last := src.lineBounds(t.cut.last)
	for _, cg := range src.ast.Comments {
		for _, c := range cg.List {
			if off := src.offset(c.Pos()); off < first || off >= last {
				continue
			}
			for prefix, why := range map[string]string{
				"//go:embed":    "the file holding it must import \"embed\", which only a blank import shows",
				"//go:linkname": "the file holding it must import \"unsafe\", which only a blank import shows",
				"//go:generate": "the command it runs is given this file's name and line",
				"//export ":     "it is a cgo export",
			} {
				if strings.HasPrefix(c.Text, prefix) {
					return refuse(src.path, src.line(c.Pos()), "%s carries a %s directive, and %s",
						t.label(), strings.TrimSpace(prefix), why)
				}
			}
		}
	}
	return nil
}
