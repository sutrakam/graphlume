package registry

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The text-side check for a subtype the extractor missed.
//
// A type that extends or implements T leaves a supertype reference naming T,
// and that reference is what becomes the extends or implements edge. When the
// extractor recorded NO reference for a line that declares such a type, the
// graph has nothing to say about it and only the text does. This reads those
// lines.
//
// It is a SIGNAL OF DOUBT and never a finding, and it is worth only as much as
// its precision. Matching any line that holds the name and one
// of the words class, struct, interface, trait, impl, extends or implements
// fires, as measured on fourteen projects, on a generic
// constraint (`<TPreset extends Preset>`), a wildcard bound (`? extends
// TypeName`), a forward declaration (`class BenchmarkReporter;`), a
// constructor parameter, and on Markdown. A line counts only when it
//
//   - is in a source file of a language that can reference the type
//     (ReferencingLanguages);
//   - declares ANOTHER named type, in the form its language writes it
//     (declaration per language family below, never one loose pattern);
//   - names the target in that declaration's SUPERTYPE LIST, as a whole name at
//     the top nesting level: not inside `<...>` (a constraint, or a type
//     argument of some other supertype), not inside `(...)` (a parameter), not
//     after `where`.
//
// A declaration that has no supertype list (a forward declaration, the
// target's own declaration) therefore cannot count. An ANONYMOUS class or
// object expression that names the target is counted apart: it is a subtype,
// it has no name, and a subtype list cannot carry it.
//
// Languages with no pattern here (Go, whose interfaces are satisfied
// structurally and whose embedded types are references; the data and
// configuration formats) yield no line at all, which loses a doubt and never
// invents one.

// declarationFileCap bounds how many files the subtype check reads. It is
// larger than mentionFileCap because the question differs: "is the name used
// anywhere besides its definition" is answered by the first few files, while
// "is there a subtype the graph does not hold" needs the files that declare
// types, and a base class that many files mention (moshi's JsonAdapter) has
// its subclasses and its anonymous instances spread among them.
const declarationFileCap = 48

// UnrecordedSubtypes is where the text declares a type that extends or
// implements a name and no reference of the index explains the line.
type UnrecordedSubtypes struct {
	// Known is false when the text index could not answer for this name.
	Known bool

	// Complete is false when so many files hold the name that not all were
	// read, so the counts may be short.
	Complete bool

	// Named is how many lines declare another NAMED type with the target in its
	// supertype list. Sample is the first few, ordered by path and line.
	Named  int
	Sample []Place

	// Anonymous is how many lines build an anonymous class or object that
	// extends or implements the target. AnonymousSample is the first few.
	Anonymous       int
	AnonymousSample []Place
}

// UnrecordedSubtypeDeclarations reports lines that declare a subtype of name
// and are recorded as nothing. See the package comment above.
func (r *Registry) UnrecordedSubtypeDeclarations(ctx context.Context, runID int64, name string, defs []DefSpan) (UnrecordedSubtypes, error) {
	langs, err := r.definitionLanguages(ctx, runID, name)
	if err != nil {
		return UnrecordedSubtypes{}, err
	}
	files, capped, err := r.mentionFiles(ctx, runID, name, ReferencingLanguages(langs), declarationFileCap)
	if err != nil {
		return UnrecordedSubtypes{}, err
	}
	if files == nil {
		return UnrecordedSubtypes{}, nil
	}
	recorded, ok, err := r.supertypeReferenceLines(ctx, runID, name)
	if err != nil || !ok {
		return UnrecordedSubtypes{}, err
	}

	var named, anon []Place
	for _, f := range files {
		if declFamilyOf(f.lang) == famNone {
			continue
		}
		all := strings.Split(f.content, "\n")
		imports := importLineSet(f.content)
		for _, line := range identifierLinesText(f.content, name) {
			p := Place{Path: f.path, Line: line.n}
			if recorded[p] || insideAny(p, defs) || imports[line.n] || !identifierInCode(line.text, name) {
				continue
			}
			switch classifySubtypeLine(all, line.n-1, name, f.lang) {
			case declNamed:
				named = append(named, p)
			case declAnonymous:
				anon = append(anon, p)
			}
		}
	}
	sortPlaces(named)
	sortPlaces(anon)
	out := UnrecordedSubtypes{Known: true, Complete: !capped, Named: len(named), Anonymous: len(anon)}
	if capped && len(named) == 0 && len(anon) == 0 {
		return UnrecordedSubtypes{}, nil
	}
	out.Sample = named[:min(len(named), unrecordedSample)]
	out.AnonymousSample = anon[:min(len(anon), unrecordedSample)]
	return out, nil
}

func sortPlaces(ps []Place) {
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].Path != ps[j].Path {
			return ps[i].Path < ps[j].Path
		}
		return ps[i].Line < ps[j].Line
	})
}

// supertypeReferenceLines is the set of lines holding a SUPERTYPE reference the
// index recorded with this spelling: an extends, an implements, an inherits. A
// line whose only reference to the name is a call, an instantiation or a type
// use is not explained as a subtype by it, and `new Target() {` is exactly that:
// an instantiation that is also an anonymous subclass. ok is false for a hub
// whose references are too many to subtract by line.
func (r *Registry) supertypeReferenceLines(ctx context.Context, runID int64, name string) (map[Place]bool, bool, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT f.path, sr.start_line
		  FROM symbol_references sr INDEXED BY idx_refs_run_name
		  JOIN files f ON f.id = sr.file_id
		 WHERE sr.index_run_id = ? AND sr.name = ? AND `+hierarchyRefKinds+`
		 LIMIT ?`, runID, name, refLineCap+1)
	if err != nil {
		return nil, false, fmt.Errorf("registry: supertype reference lines of %q: %w", name, err)
	}
	defer rows.Close()
	recorded := map[Place]bool{}
	for rows.Next() {
		var p Place
		if err := rows.Scan(&p.Path, &p.Line); err != nil {
			return nil, false, fmt.Errorf("registry: supertype reference lines of %q: scan: %w", name, err)
		}
		recorded[p] = true
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return recorded, len(recorded) <= refLineCap, nil
}

// ---------------------------------------------------------------- families

type declFamily int

const (
	famNone declFamily = iota
	famJava
	famKotlin
	famScala
	famCSharp
	famC
	famRust
	famPython
	famRuby
	famPHP
	famECMA
	famSwift
)

func declFamilyOf(lang string) declFamily {
	switch lang {
	case "java", "groovy":
		return famJava
	case "kotlin":
		return famKotlin
	case "scala":
		return famScala
	case "c_sharp":
		return famCSharp
	case "c", "cpp", "objc", "cuda", "arduino":
		return famC
	case "rust":
		return famRust
	case "python":
		return famPython
	case "ruby":
		return famRuby
	case "php", "hack":
		return famPHP
	case "javascript", "typescript", "tsx", "jsx", "vue", "svelte", "astro":
		return famECMA
	case "swift":
		return famSwift
	}
	return famNone
}

type declKind int

const (
	declNone declKind = iota
	declNamed
	declAnonymous
)

// declKeywords find where a named type is declared, by family. The last match
// before the mention is the declaration the mention belongs to. Group 1 is the
// declared name.
var declKeywords = map[declFamily]*regexp.Regexp{
	famJava:   regexp.MustCompile(`\b(?:class|interface|enum|record|@interface)\s+(\w+)`),
	famKotlin: regexp.MustCompile(`\b(?:class|interface|object)\s+(\w+)`),
	famScala:  regexp.MustCompile(`\b(?:class|trait|object|enum)\s+(\w+)`),
	famCSharp: regexp.MustCompile(`\b(?:record\s+(?:class|struct)|class|struct|interface|record|enum)\s+(\w+)`),
	famC:      regexp.MustCompile(`(?:\b(?:class|struct|union)|@interface)\s+(?:\w+\s+)*?(\w+)\s*(?:final\b|sealed\b)?\s*(?:[:{;]|$)`),
	famPHP:    regexp.MustCompile(`\b(?:class|interface|trait|enum)\s+(\w+)`),
	famECMA:   regexp.MustCompile(`\b(?:class|interface)\s+(\w+)`),
	famSwift:  regexp.MustCompile(`\b(?:class|struct|enum|protocol|extension|actor)\s+(\w+)`),
}

// superIntro says, per family, which depth-0 token starts (or ends) a run of
// supertypes. A token in `start` makes the names after it supertypes; a token
// in `end` stops that.
type superIntro struct {
	start map[string]bool
	end   map[string]bool
}

func wordSet(ws ...string) map[string]bool {
	m := map[string]bool{}
	for _, w := range ws {
		m[w] = true
	}
	return m
}

var superIntros = map[declFamily]superIntro{
	famJava:   {start: wordSet("extends", "implements"), end: wordSet("permits", "throws")},
	famKotlin: {start: wordSet(":")},
	famScala:  {start: wordSet("extends", "with")},
	famCSharp: {start: wordSet(":")},
	famC:      {start: wordSet(":")},
	famPHP:    {start: wordSet("extends", "implements")},
	famECMA:   {start: wordSet("extends", "implements")},
	famSwift:  {start: wordSet(":")},
}

// ---------------------------------------------------------------- header tokens

type headerTok struct {
	text  string
	off   int
	depth int
	ident bool
}

// headerTokens reads the start of a declaration after the declared name: its
// identifiers (dotted, `::` and `\` paths joined) and punctuation, each with
// the bracket depth it sits at. It stops at the first depth-0 `{`, `;`, a
// lone `=` or `where`, which end a supertype list.
func headerTokens(s string, base int) []headerTok {
	var out []headerTok
	depth := 0
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case identByte(c) || c == '\\' && i+1 < len(s) && identByte(s[i+1]):
			j := i
			for j < len(s) {
				switch {
				case identByte(s[j]):
					j++
				case (s[j] == '.' || s[j] == '\\') && j+1 < len(s) && identByte(s[j+1]):
					j++
				case s[j] == ':' && j+2 < len(s) && s[j+1] == ':' && identByte(s[j+2]):
					j += 2
				default:
					goto word
				}
			}
		word:
			w := s[i:j]
			if depth == 0 && w == "where" {
				return out
			}
			out = append(out, headerTok{text: w, off: base + i, depth: depth, ident: true})
			i = j
		default:
			switch c {
			case '{':
				if depth == 0 {
					return out
				}
				depth++
			case ';':
				if depth == 0 {
					return out
				}
			case '=':
				prevEq := i > 0 && (s[i-1] == '=' || s[i-1] == '!' || s[i-1] == '<' || s[i-1] == '>')
				nextGt := i+1 < len(s) && (s[i+1] == '>' || s[i+1] == '=')
				if depth == 0 && !prevEq && !nextGt {
					return out
				}
			case '(', '[':
				depth++
			case '<':
				// A generic list opens right after a name or another list.
				if i > 0 && (identByte(s[i-1]) || s[i-1] == '>') {
					depth++
				}
			case ')', ']', '}':
				if depth > 0 {
					depth--
				}
			case '>':
				if depth > 0 && (i == 0 || (s[i-1] != '-' && s[i-1] != '=')) {
					depth--
				}
			}
			out = append(out, headerTok{text: string(c), off: base + i, depth: depth})
			i++
		}
	}
	return out
}

func lastSegment(path string) string {
	if i := strings.LastIndexAny(path, ".\\"); i >= 0 {
		path = path[i+1:]
	}
	if i := strings.LastIndex(path, "::"); i >= 0 {
		path = path[i+2:]
	}
	return path
}

// namesInSupertypeList reports whether tokens, read as the rest of a
// declaration header, name target at depth 0 inside a supertype run, and that
// token starts on or after minOff.
func namesInSupertypeList(toks []headerTok, intro superIntro, target string, minOff int) bool {
	in := false
	for _, t := range toks {
		if t.depth != 0 {
			continue
		}
		switch {
		case intro.start[t.text]:
			in = true
		case intro.end[t.text]:
			in = false
		case t.ident && in && t.off >= minOff && lastSegment(t.text) == target:
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- classification

// declarationWindow joins the lines a declaration header may span, ending at
// line idx: the lines before it that are not the end of a statement or a block.
// It returns the text and the offset at which line idx starts in it.
func declarationWindow(lines []string, idx int) (string, int) {
	start := idx
	for k := 0; k < 3 && start > 0; k++ {
		prev := strings.TrimSpace(lines[start-1])
		if prev == "" || commentOnly(prev) || strings.HasSuffix(prev, ";") ||
			strings.HasSuffix(prev, "{") || strings.HasSuffix(prev, "}") {
			break
		}
		start--
	}
	var b strings.Builder
	for k := start; k < idx; k++ {
		b.WriteString(lines[k])
		b.WriteByte(' ')
	}
	base := b.Len()
	b.WriteString(lines[idx])
	return b.String(), base
}

var (
	rustImpl      = regexp.MustCompile(`\bimpl\b`)
	rustTrait     = regexp.MustCompile(`\btrait\s+(\w+)`)
	rubySuper     = regexp.MustCompile(`^\s*(?:class|module)\s+[\w:]+\s*<\s*([\w:]+)`)
	rubyMixin     = regexp.MustCompile(`^\s*(?:include|extend|prepend)\s+([\w:]+)`)
	rubyAnon      = regexp.MustCompile(`\bClass\.new\(\s*([\w:]+)`)
	javaAnon      = regexp.MustCompile(`\bnew\s+(?:[\w.]+\.)?(\w+)\s*(?:<[^()]*>)?\s*\([^;{]*\)\s*\{`)
	scalaAnon     = regexp.MustCompile(`\bnew\s+(?:[\w.]+\.)?(\w+)\b[^;{()]*\{`)
	kotlinAnon    = regexp.MustCompile(`\bobject\s*:\s*`)
	phpAnonClass  = regexp.MustCompile(`\bnew\s+class\b`)
	ecmaAnonClass = regexp.MustCompile(`\bclass\s+(?:extends|implements)\b`)
	pyClass       = regexp.MustCompile(`^\s*class\s+(\w+)\s*\(`)
)

// classifySubtypeLine decides what the line at index idx of a file of language
// lang declares with respect to name.
func classifySubtypeLine(lines []string, idx int, name, lang string) declKind {
	fam := declFamilyOf(lang)
	cur := lines[idx]
	switch fam {
	case famRust:
		return rustLine(lines, idx, name)
	case famPython:
		return pythonLine(lines, idx, name)
	case famRuby:
		return rubyLine(cur, name)
	}
	window, base := declarationWindow(lines, idx)
	offs := identifierOffsets(cur, name)
	if len(offs) == 0 {
		return declNone
	}

	// Anonymous forms first: they are a line's own, never the window's.
	if anonymousSubtype(cur, name, fam) {
		return declAnonymous
	}

	re, ok := declKeywords[fam]
	if !ok {
		return declNone
	}
	pos := base + offs[0]
	before := window[:pos]
	ms := re.FindAllStringSubmatchIndex(before, -1)
	if len(ms) == 0 {
		return declNone
	}
	m := ms[len(ms)-1]
	declared := before[m[2]:m[3]]
	if declared == name {
		return declNone
	}
	// From the start of the declared name, so a generic list that follows it
	// directly (`Foo<T extends X>`) is read as one.
	rest := window[m[2]:]
	toks := headerTokens(rest, m[2])
	if namesInSupertypeList(toks, superIntros[fam], name, base) {
		return declNamed
	}
	return declNone
}

// anonymousSubtype reports whether the line builds an anonymous class or
// object that extends or implements name.
func anonymousSubtype(cur, name string, fam declFamily) bool {
	switch fam {
	case famJava:
		if m := javaAnon.FindAllStringSubmatch(cur, -1); m != nil {
			for _, g := range m {
				if g[1] == name {
					return true
				}
			}
		}
	case famScala:
		for _, g := range scalaAnon.FindAllStringSubmatch(cur, -1) {
			if g[1] == name {
				return true
			}
		}
	case famKotlin:
		if loc := kotlinAnon.FindStringIndex(cur); loc != nil {
			toks := headerTokens(cur[loc[1]:], loc[1])
			for _, t := range toks {
				if t.depth == 0 && t.ident && lastSegment(t.text) == name {
					return true
				}
			}
		}
	case famPHP:
		if loc := phpAnonClass.FindStringIndex(cur); loc != nil {
			toks := headerTokens(cur[loc[0]:], loc[0])
			return namesInSupertypeList(toks, superIntros[famPHP], name, 0)
		}
	case famECMA:
		if loc := ecmaAnonClass.FindStringIndex(cur); loc != nil {
			toks := headerTokens(cur[loc[0]:], loc[0])
			return namesInSupertypeList(toks, superIntros[famECMA], name, 0)
		}
	}
	return false
}

// rustLine reads `impl<T> Name for Type` and `trait Sub: A + Name`.
func rustLine(lines []string, idx int, name string) declKind {
	window, base := declarationWindow(lines, idx)
	offs := identifierOffsets(lines[idx], name)
	if len(offs) == 0 {
		return declNone
	}
	pos := base + offs[0]
	before := window[:pos]

	if locs := rustImpl.FindAllStringIndex(before, -1); len(locs) > 0 {
		start := locs[len(locs)-1][0]
		toks := headerTokens(window[start:], start)
		// impl [<generics>] Path [<args>] for Type: the trait path is the first
		// identifier after `impl` at the top level.
		seenImpl := false
		for i := 0; i < len(toks); i++ {
			t := toks[i]
			if t.depth != 0 || !t.ident {
				continue
			}
			if !seenImpl {
				seenImpl = t.text == "impl"
				continue
			}
			if lastSegment(t.text) == name && t.off >= base {
				for j := i + 1; j < len(toks); j++ {
					n := toks[j]
					if n.depth != 0 {
						continue
					}
					if n.ident && n.text == "for" {
						return declNamed
					}
					break
				}
			}
			break
		}
	}
	if ms := rustTrait.FindAllStringSubmatchIndex(before, -1); len(ms) > 0 {
		m := ms[len(ms)-1]
		if before[m[2]:m[3]] != name {
			toks := headerTokens(window[m[2]:], m[2])
			if namesInSupertypeList(toks, superIntro{start: wordSet(":", "+")}, name, base) {
				// `trait Sub<T: A>` is inside the generics, depth > 0, and
				// `trait Sub: A + Name` is a depth-0 colon and plus.
				return declNamed
			}
		}
	}
	return declNone
}

// pythonLine reads `class Name(Base, Name, metaclass=M):` across lines.
func pythonLine(lines []string, idx int, name string) declKind {
	cur := lines[idx]
	if len(identifierOffsets(cur, name)) == 0 {
		return declNone
	}
	// Find the class statement whose parentheses are still open on this line.
	start := -1
	for k := idx; k >= 0 && idx-k <= 4; k-- {
		if pyClass.MatchString(lines[k]) {
			start = k
			break
		}
	}
	if start < 0 {
		return declNone
	}
	var b strings.Builder
	for k := start; k <= idx; k++ {
		b.WriteString(lines[k])
		b.WriteByte(' ')
	}
	text := b.String()
	m := pyClass.FindStringSubmatchIndex(text)
	declared := text[m[2]:m[3]]
	if declared == name {
		return declNone
	}
	open := m[1] // just past the `(`
	base := len(text) - len(cur) - 1
	// Split the argument list on top-level commas, up to its closing `)`. An
	// argument counts only if it reaches this line: the class statement may
	// have closed its parentheses on an earlier one.
	depth := 1
	argStart := open
	type arg struct {
		text     string
		from, to int
	}
	var args []arg
	for i := open; i < len(text) && depth > 0; i++ {
		switch text[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				args = append(args, arg{text[argStart:i], argStart, i})
			}
		case ',':
			if depth == 1 {
				args = append(args, arg{text[argStart:i], argStart, i})
				argStart = i + 1
			}
		}
	}
	if depth > 0 {
		args = append(args, arg{text[argStart:], argStart, len(text)})
	}
	for _, a := range args {
		if a.to < base {
			continue
		}
		t := strings.TrimSpace(a.text)
		if t == "" || strings.Contains(t, "=") {
			continue // keyword arguments: metaclass=..., or a default
		}
		if i := strings.IndexAny(t, "[("); i >= 0 {
			t = t[:i]
		}
		if lastSegment(strings.TrimSpace(t)) == name {
			return declNamed
		}
	}
	return declNone
}

// rubyLine reads `class Sub < Name` and the mixin lines `include Name`.
func rubyLine(cur, name string) declKind {
	if m := rubySuper.FindStringSubmatch(cur); m != nil && lastSegment(m[1]) == name {
		declared := strings.Fields(strings.TrimSpace(cur))
		if len(declared) >= 2 && lastSegment(declared[1]) != name {
			return declNamed
		}
	}
	if m := rubyMixin.FindStringSubmatch(cur); m != nil && lastSegment(m[1]) == name {
		return declNamed
	}
	if m := rubyAnon.FindStringSubmatch(cur); m != nil && lastSegment(m[1]) == name {
		return declAnonymous
	}
	return declNone
}
