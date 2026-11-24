package parse

import (
	"bytes"
	"sort"
	"strings"
)

// A guarded conditional-compilation mask for C, C++, Objective-C and C#.
//
// WHY. Tree-sitter parses the text as written and nothing runs the
// preprocessor. A `#if ... #else ... #endif` that cuts through a declaration
// leaves text that is not valid in the grammar, in whichever branch is read:
//
//	ILogger ForContext(ILogEventEnricher enricher)
//	#if FEATURE_DEFAULT_INTERFACE
//	    => new LoggerConfiguration().ForContext(enricher)
//	#endif
//	    ;
//
//	Foo::Foo() : a_(1),
//	#if HAVE_B
//	    b_(2),
//	#endif
//	    c_(3) {}
//
//	#ifdef __cplusplus
//	extern "C" {
//	#endif
//
// The grammars have `preproc_if` nodes, which is why a group whose branches
// are each complete declarations already parses. The groups that do not are
// the ones that read as errors, and in a header an error node can swallow
// the whole class that follows it: google/benchmark lost about a quarter of
// its header classes, fmt's core.h was one error node.
//
// WHAT. The source is scanned as text, comments and strings skipped, for the
// directives `#if`, `#ifdef`, `#ifndef`, `#elif`, `#else`, `#endif` (and for C#
// the same without the `def` forms; `#region` and `#endregion` are read as
// directives and ignored), and the groups they form are built with nesting.
// For each group that needs it, exactly ONE branch is kept and the others are
// overwritten with spaces, newlines kept, and the group's own directive lines
// are overwritten too, since the grammar does not accept them in the middle of
// a declaration. The masked text has the SAME BYTE LENGTH as the source, so no
// offset and no line number moves.
//
// WHICH BRANCH. Conditions are not evaluated; the rule is positional:
//
//  1. the first branch whose condition is not the literal `0` (C family) or
//     `false` (C#), in source order, `#else` counting as a branch with no
//     condition;
//  2. so `#if 0 ... #else ... #endif` keeps the `#else`, and `#if 0` with
//     nothing after it keeps nothing;
//  3. everything else keeps the first branch: `#ifdef __cplusplus` around
//     `extern "C" {` keeps it, and keeps the partner `}` group for the same
//     reason (the two are masked together or not at all, see below); a header
//     guard `#ifndef X` / `#define X` has one branch and is simply kept.
//
// WHICH GROUPS. Only the ones that cause trouble, judged on the plain parse:
//
//   - a group with a single branch is masked when an error node overlaps it:
//     nothing is lost, only its directive lines go;
//   - a group with several branches is masked when an error node lies on or
//     next to one of its directive lines (within one line), or contains one;
//     an error somewhere inside a branch does not show that the branches are
//     the problem, and masking would throw the others away for nothing;
//   - groups that open or close a brace on their own (`extern "C" {` and
//     its `}`) are masked all together if any of them is, so the braces that
//     were balanced stay balanced.
//
// WHAT IS LOST. A definition that exists only in a blanked branch is lost, by
// design: the other branch is another build's code. The number of branches
// blanked is recorded on the result and said in the stored parse text.
//
// THE GUARD is in cmacro.go and is shared with the macro mask: the masked
// parse replaces the plain one only if it has no more error nodes and no
// fewer definitions, at least one strictly better, no definition lost that the
// plain parse read reliably, and no definition named after a keyword.

// condLanguages are the grammars the conditional mask applies to.
var condLanguages = map[string]bool{"c": true, "cpp": true, "objc": true, "c_sharp": true}

type condKind uint8

const (
	condIf condKind = iota
	condElif
	condElse
	condEndif
)

// condDirective is one conditional directive line: from its `#` to the end of
// the directive (a backslash continuation and a block comment that spans
// lines are part of it), not including the newline.
type condDirective struct {
	kind       condKind
	start, end int
	// cond is the condition text with comments removed and spaces trimmed.
	cond string
	// depth and pdepth are the brace depth and the parenthesis-and-bracket
	// depth, over code only, where the directive stands.
	depth, pdepth int
	// prev is the last code byte before the directive, prevAt its offset;
	// first is the first code byte after it, 0 when another conditional
	// directive comes before any code.
	prev        byte
	prevAt      int
	first       byte
	hasPrevCode bool
}

type condBranch struct {
	dir condDirective
	// body is the text after the directive up to the next directive of the
	// group: [bodyStart, bodyEnd).
	bodyStart, bodyEnd int
	// delta and pdelta are how many more `{` than `}`, and `(`/`[` than
	// `)`/`]`, the body holds.
	delta, pdelta int
	// first and last are the first and last code byte of the body, 0 when it
	// holds none.
	first, last byte
}

// condGroup is one #if ... #endif with the branches between.
type condGroup struct {
	branches []condBranch
	endif    condDirective
	// start and end span the group, first directive to end of `#endif`.
	start, end int
	parent     int // index in the group list, -1 at the top
	// keep is the index of the branch kept, -1 when none is.
	keep int
	// live is false when the group sits in a branch that was passed over.
	live bool
}

// scanCondDirectives returns every conditional directive of src in order,
// with the brace depth where each stands. Comments, strings and character
// literals are skipped; every other preprocessor line is consumed whole, so a
// `#define X "unterminated` or an apostrophe in `#error` cannot start a
// literal.
func scanCondDirectives(src []byte, csharp bool) []condDirective {
	var out []condDirective
	n := len(src)
	depth, pdepth := 0, 0
	lineStart := true
	var lastCode byte
	lastAt := -1
	pending := -1 // the directive still waiting for its first code byte
	code := func(c byte, at int) {
		lastCode, lastAt = c, at
		if pending >= 0 {
			out[pending].first = c
			pending = -1
		}
	}
	for i := 0; i < n; {
		c := src[i]
		switch {
		case c == '\n':
			lineStart = true
			i++
		case c == ' ' || c == '\t' || c == '\r' || c == '\f' || c == '\v':
			i++
		case c == '/' && i+1 < n && src[i+1] == '/':
			for i < n && src[i] != '\n' {
				if !csharp && src[i] == '\\' && i+1 < n {
					i++
				}
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			i = skipBlockComment(src, i)
		case c == '#' && lineStart:
			end := directiveEnd(src, i, csharp)
			if d, ok := parseCondDirective(src, i, end, csharp); ok {
				d.depth, d.pdepth = depth, pdepth
				d.prev, d.prevAt, d.hasPrevCode = lastCode, lastAt, lastAt >= 0
				out = append(out, d)
				pending = len(out) - 1
			}
			i = end
		case c == '"':
			lineStart = false
			code(c, i)
			i = skipStringAt(src, i, csharp)
		case c == '\'':
			lineStart = false
			code(c, i)
			if !csharp && i > 0 && isIdentByte(src[i-1]) && src[i-1] != 'L' && src[i-1] != 'u' && src[i-1] != 'U' {
				i++ // a digit separator, 1'000
				continue
			}
			i = skipQuoted(src, i)
		default:
			lineStart = false
			code(c, i)
			switch c {
			case '{':
				depth++
			case '}':
				depth--
			case '(', '[':
				pdepth++
			case ')', ']':
				pdepth--
			}
			i++
		}
	}
	return out
}

// skipBlockComment returns the index after the block comment opening at i.
func skipBlockComment(src []byte, i int) int {
	i += 2
	for i < len(src) && (src[i] != '*' || i+1 >= len(src) || src[i+1] != '/') {
		i++
	}
	i += 2
	if i > len(src) {
		i = len(src)
	}
	return i
}

// directiveEnd returns the index of the newline (or the end of src) that ends
// the preprocessor directive starting at i. In C a backslash before the
// newline continues it, and a block comment may span lines inside it.
func directiveEnd(src []byte, i int, csharp bool) int {
	n := len(src)
	for i < n && src[i] != '\n' {
		switch {
		case !csharp && src[i] == '\\' && i+1 < n && (src[i+1] == '\n' || src[i+1] == '\r' && i+2 < n && src[i+2] == '\n'):
			i++
			if src[i] == '\r' {
				i++
			}
			i++ // the newline itself belongs to the directive
			continue
		case src[i] == '/' && i+1 < n && src[i+1] == '*':
			i = skipBlockComment(src, i)
			continue
		case src[i] == '/' && i+1 < n && src[i+1] == '/':
			// A line comment ends at the newline, honouring a continuation in C.
			for i < n && src[i] != '\n' {
				if !csharp && src[i] == '\\' && i+1 < n {
					i++
				}
				i++
			}
			continue
		}
		i++
	}
	if i > n {
		i = n
	}
	return i
}

// skipStringAt returns the index after the string literal whose opening quote
// is at i. A C++ raw string, a C# verbatim string and a C# raw string span
// lines; every other string ends at its closing quote or its line.
func skipStringAt(src []byte, i int, csharp bool) int {
	if csharp {
		// """raw""" with three or more quotes.
		j := i
		for j < len(src) && src[j] == '"' {
			j++
		}
		if q := j - i; q >= 3 {
			closer := strings.Repeat("\"", q)
			if k := bytes.Index(src[j:], []byte(closer)); k >= 0 {
				return j + k + q
			}
			return len(src)
		}
		verbatim := i > 0 && src[i-1] == '@' || i > 1 && src[i-1] == '$' && src[i-2] == '@'
		if verbatim {
			for k := i + 1; k < len(src); k++ {
				if src[k] == '"' {
					if k+1 < len(src) && src[k+1] == '"' {
						k++
						continue
					}
					return k + 1
				}
			}
			return len(src)
		}
		return skipQuoted(src, i)
	}
	if i > 0 && src[i-1] == 'R' && (i < 2 || !isIdentByte(src[i-2]) || src[i-2] == 'L' || src[i-2] == 'u' || src[i-2] == 'U' || src[i-2] == '8') {
		return skipRawString(src, i)
	}
	return skipQuoted(src, i)
}

// parseCondDirective reads the directive occupying src[start:end] when it is
// a conditional one.
func parseCondDirective(src []byte, start, end int, csharp bool) (condDirective, bool) {
	j := start + 1
	for j < end && (src[j] == ' ' || src[j] == '\t') {
		j++
	}
	w := j
	for j < end && src[j] >= 'a' && src[j] <= 'z' {
		j++
	}
	word := string(src[w:j])
	d := condDirective{start: start, end: end}
	switch word {
	case "if":
		d.kind = condIf
	case "ifdef", "ifndef":
		if csharp {
			return d, false
		}
		d.kind = condIf
		d.cond = word // the condition is the macro, but only `0` matters here
	case "elif", "elifdef", "elifndef":
		if csharp && word != "elif" {
			return d, false
		}
		d.kind = condElif
	case "else":
		d.kind = condElse
	case "endif":
		d.kind = condEndif
	default:
		return d, false
	}
	if word == "if" || word == "elif" {
		d.cond = conditionText(src[j:end])
	} else if word != "else" && word != "endif" {
		d.cond = word + " " + conditionText(src[j:end])
	}
	return d, true
}

// conditionText strips comments and continuation backslashes from the text of
// a directive's condition and trims it.
func conditionText(b []byte) string {
	var sb strings.Builder
	for i := 0; i < len(b); i++ {
		switch {
		case b[i] == '/' && i+1 < len(b) && b[i+1] == '*':
			i = skipBlockComment(b, i) - 1
			sb.WriteByte(' ')
		case b[i] == '/' && i+1 < len(b) && b[i+1] == '/':
			return strings.TrimSpace(sb.String())
		case b[i] == '\\' || b[i] == '\r' || b[i] == '\n':
			sb.WriteByte(' ')
		default:
			sb.WriteByte(b[i])
		}
	}
	return strings.TrimSpace(sb.String())
}

// buildCondGroups pairs the directives into groups. A stray `#else` or
// `#endif`, a second `#else`, and a group the file never closes are dropped:
// text that cannot be paired is not touched. The result is in source order of
// each group's opening directive, so a parent comes before its children.
func buildCondGroups(dirs []condDirective) []condGroup {
	type open struct {
		g      condGroup
		bad    bool
		id     int
		parent int // id of the enclosing group, -1 at the top
	}
	type closed struct {
		id, parent int
		g          condGroup
	}
	var stack []*open
	var done []closed
	next := 0
	// parentOf is every opened group's enclosing group, so that a group whose
	// parent was dropped can still find the nearest one that was kept.
	parentOf := map[int]int{}
	closeBranch := func(o *open, at int) {
		if k := len(o.g.branches); k > 0 {
			o.g.branches[k-1].bodyEnd = at
		}
	}
	for _, d := range dirs {
		switch d.kind {
		case condIf:
			o := &open{id: next, parent: -1, g: condGroup{start: d.start, parent: -1, keep: -1}}
			next++
			if len(stack) > 0 {
				o.parent = stack[len(stack)-1].id
			}
			parentOf[o.id] = o.parent
			o.g.branches = append(o.g.branches, condBranch{dir: d, bodyStart: d.end})
			stack = append(stack, o)
		case condElif, condElse:
			if len(stack) == 0 {
				continue
			}
			o := stack[len(stack)-1]
			if k := len(o.g.branches); k > 0 && o.g.branches[k-1].dir.kind == condElse {
				o.bad = true
				continue
			}
			closeBranch(o, d.start)
			o.g.branches = append(o.g.branches, condBranch{dir: d, bodyStart: d.end})
		case condEndif:
			if len(stack) == 0 {
				continue
			}
			o := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			closeBranch(o, d.start)
			o.g.endif = d
			o.g.end = d.end
			if o.bad {
				continue
			}
			for i := range o.g.branches {
				b := &o.g.branches[i]
				nd := d
				if i+1 < len(o.g.branches) {
					nd = o.g.branches[i+1].dir
				}
				b.delta = nd.depth - b.dir.depth
				b.pdelta = nd.pdepth - b.dir.pdepth
				b.first = b.dir.first
				if nd.hasPrevCode && nd.prevAt >= b.dir.end {
					b.last = nd.prev
				}
			}
			done = append(done, closed{id: o.id, parent: o.parent, g: o.g})
		}
	}
	sort.SliceStable(done, func(a, b int) bool { return done[a].g.start < done[b].g.start })
	index := make(map[int]int, len(done))
	for i, c := range done {
		index[c.id] = i
	}
	groups := make([]condGroup, len(done))
	for i, c := range done {
		g := c.g
		// An unclosed or dropped parent leaves the group the nearest kept
		// ancestor, or none.
		for p := c.parent; p >= 0; p = parentOf[p] {
			if idx, ok := index[p]; ok {
				g.parent = idx
				break
			}
		}
		groups[i] = g
	}
	// Groups are in source order and a parent comes before its children, so
	// each choice sees the macros the earlier ones assumed.
	world := map[string]bool{}
	for i := range groups {
		g := &groups[i]
		live := true
		if g.parent >= 0 {
			par := &groups[g.parent]
			live = par.live && par.keep >= 0 && g.start >= par.branches[par.keep].bodyStart && g.start < par.branches[par.keep].bodyEnd
		}
		g.live = live
		g.keep = chooseCondBranch(g, world, live)
	}
	return groups
}

// chooseCondBranch applies the positional rule in the file comment: the first
// branch that is not known to be dead, `#else` counting as a branch with no
// condition. A branch is dead when its condition is the literal zero, or is a
// bare test of one macro (`#ifdef X`, `#ifndef X`, `defined(X)`,
// `!defined(X)`) that contradicts what an earlier group in the file already
// assumed about that macro. world holds those assumptions: choosing a branch
// that tests a macro nothing has been assumed about records the macro as
// defined or undefined to match it, when the group is live (not inside a
// branch that was passed over).
//
// This is the one place the rule is more than positional, and it is what keeps
// two groups that test the same macro from being read in two different worlds:
//
//	#ifdef NO_EXCEPTIONS  ((void)e);  #else  try {  #endif
//	...
//	#ifndef NO_EXCEPTIONS  } catch (...) { }  #endif
//
// Read positionally, the first group keeps `((void)e);` and the second keeps
// `} catch`, a brace with nothing to close.
func chooseCondBranch(g *condGroup, world map[string]bool, live bool) int {
	for i, b := range g.branches {
		if b.dir.kind == condElse {
			return i
		}
		if condIsFalse(b.dir.cond) {
			continue
		}
		name, want, ok := condMacroTest(b.dir.cond)
		if ok {
			if have, known := world[name]; known && have != want {
				continue
			}
			if live {
				if _, known := world[name]; !known {
					world[name] = want
				}
			}
		}
		return i
	}
	return -1
}

// condMacroTest reads a condition that is nothing but a test of whether one
// macro is defined: `ifdef X`, `ifndef X`, `defined(X)`, `defined X`, and the
// `!` forms of the last two. want is true when the branch is taken with the
// macro defined.
func condMacroTest(cond string) (name string, want, ok bool) {
	c := strings.TrimSpace(cond)
	switch {
	case strings.HasPrefix(c, "ifdef "):
		name = identOnly(c[len("ifdef "):])
		return name, true, name != ""
	case strings.HasPrefix(c, "ifndef "):
		name = identOnly(c[len("ifndef "):])
		return name, false, name != ""
	}
	want = true
	if strings.HasPrefix(c, "!") {
		want = false
		c = strings.TrimSpace(c[1:])
	}
	if !strings.HasPrefix(c, "defined") {
		return "", false, false
	}
	c = strings.TrimSpace(c[len("defined"):])
	if strings.HasPrefix(c, "(") && strings.HasSuffix(c, ")") {
		c = strings.TrimSpace(c[1 : len(c)-1])
	}
	name = identOnly(c)
	return name, want, name != ""
}

// identOnly returns s when it is a single identifier, else "".
func identOnly(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || !isIdentStart(s[0]) {
		return ""
	}
	for i := 0; i < len(s); i++ {
		if !isIdentByte(s[i]) {
			return ""
		}
	}
	return s
}

// condIsFalse reports whether a condition is the literal zero: `0`, `(0)`, or
// `false` in C#. It is the one idiom where the first branch is known to be
// dead code.
func condIsFalse(cond string) bool {
	c := strings.TrimSpace(cond)
	for len(c) >= 2 && c[0] == '(' && c[len(c)-1] == ')' {
		c = strings.TrimSpace(c[1 : len(c)-1])
	}
	return c == "0" || c == "false"
}

// condSpan is a half-open byte range.
type condSpan struct{ start, end int }

// condPlan is which groups to mask in one file.
type condPlan struct {
	groups []condGroup
	// selected[i] says whether groups[i] is masked.
	selected []bool
	// blanked are the bodies of the branches not kept, the places a
	// definition from the plain parse may legitimately vanish from.
	blanked []condSpan
	// branches counts the blanked branches that held any text.
	branches int
}

func (p *condPlan) empty() bool { return p == nil || !p.anySelected() }

func (p *condPlan) anySelected() bool {
	for _, s := range p.selected {
		if s {
			return true
		}
	}
	return false
}

// planCondMask decides which groups to mask given the error spans of the
// plain parse.
func planCondMask(src []byte, groups []condGroup, errs []condSpan) *condPlan {
	plan := &condPlan{groups: groups, selected: make([]bool, len(groups))}
	if len(groups) == 0 {
		return plan
	}
	sort.Slice(errs, func(a, b int) bool { return errs[a].start < errs[b].start })
	// overlaps reports whether any error overlaps [a, b), or is a zero-width
	// missing token inside it or at its edge.
	overlaps := func(a, b int) bool {
		for _, e := range errs {
			if e.start > b {
				return false
			}
			if e.start == e.end {
				if e.start >= a {
					return true
				}
				continue
			}
			if e.start < b && e.end > a {
				return true
			}
		}
		return false
	}
	unbalanced := []int{}
	anyUnbalanced := false
	for i := range groups {
		g := &groups[i]
		if g.cuts() {
			plan.selected[i] = true
		}
		if len(g.branches) == 1 {
			if g.keep >= 0 && g.branches[0].delta != 0 {
				unbalanced = append(unbalanced, i)
			}
			if overlaps(g.start, g.end) {
				plan.selected[i] = true
				if g.branches[0].delta != 0 {
					anyUnbalanced = true
				}
			}
			continue
		}
		dirs := make([]condDirective, 0, len(g.branches)+1)
		for _, b := range g.branches {
			dirs = append(dirs, b.dir)
		}
		dirs = append(dirs, g.endif)
		for _, d := range dirs {
			lo, hi := lineBounds(src, d.start, d.end)
			if overlaps(lo, hi) {
				plan.selected[i] = true
				break
			}
		}
	}
	if anyUnbalanced {
		for _, i := range unbalanced {
			plan.selected[i] = true
		}
	}
	plan.computeBlanked(src)
	return plan
}

// cuts reports whether the group's text shows, without the parse tree, that it
// stands in the middle of a declaration or an expression: the code before it
// ends on a byte that asks for more (`=`, `,`, `(`), a branch begins with one
// that continues what came before (`,`, `=>`, `.Where(`, `)`, `: init_(0)`) or
// ends with one that asks for more, or a branch leaves a parenthesis or
// bracket open or closed that it did not open. A group that is complete
// declarations on both sides does not.
//
// It is a second source of evidence beside the error nodes, for the case the
// error nodes hide: once the parser has failed late in a file it can fold
// the whole stack into one error node, and the group that caused it is then
// no closer to an error than any other. The caller only asks when the plain
// tree has errors, and the guard judges the result either way.
func (g *condGroup) cuts() bool {
	if len(g.branches) == 0 {
		return false
	}
	b0 := g.branches[0].dir
	if b0.hasPrevCode && strings.IndexByte("=,([?|", b0.prev) >= 0 {
		return true
	}
	afterParams := b0.hasPrevCode && b0.prev == ')'
	for _, b := range g.branches {
		if b.pdelta != 0 {
			return true
		}
		if b.first != 0 && strings.IndexByte(",=.)]?|&:", b.first) >= 0 {
			return true
		}
		// A body or a `;` or `=>` where the parameter list just ended: the
		// head of the declaration is outside the group, the rest inside.
		if afterParams && b.first != 0 && strings.IndexByte("{;", b.first) >= 0 {
			return true
		}
		// A branch that ends on `]` is an attribute list, which belongs to
		// the declaration after the group.
		if b.last != 0 && strings.IndexByte(",=([?|&]", b.last) >= 0 {
			return true
		}
	}
	return false
}

// lineBounds widens [start, end) to include the line before and the line
// after the directive.
func lineBounds(src []byte, start, end int) (int, int) {
	lo := bytes.LastIndexByte(src[:start], '\n')
	if lo >= 0 {
		lo = bytes.LastIndexByte(src[:lo], '\n')
	}
	if lo < 0 {
		lo = 0
	}
	hi := len(src)
	if k := bytes.IndexByte(src[end:], '\n'); k >= 0 {
		hi = end + k + 1
		if k2 := bytes.IndexByte(src[hi:], '\n'); k2 >= 0 {
			hi += k2
		} else {
			hi = len(src)
		}
	}
	return lo, hi
}

// computeBlanked lists the spans the plan overwrites and counts the branches.
func (p *condPlan) computeBlanked(src []byte) {
	p.blanked = p.blanked[:0]
	p.branches = 0
	inBlank := func(pos int) bool {
		for _, s := range p.blanked {
			if pos >= s.start && pos < s.end {
				return true
			}
		}
		return false
	}
	for i := range p.groups {
		g := &p.groups[i]
		if !p.selected[i] || inBlank(g.start) {
			continue
		}
		for bi, b := range g.branches {
			if bi == g.keep || b.bodyEnd <= b.bodyStart {
				continue
			}
			p.blanked = append(p.blanked, condSpan{b.bodyStart, b.bodyEnd})
			if len(bytes.TrimSpace(src[b.bodyStart:b.bodyEnd])) > 0 {
				p.branches++
			}
		}
	}
}

// apply returns a copy of src with the plan's directive lines and
// non-kept branches overwritten by spaces. Newlines are kept and no byte is
// added or removed.
func (p *condPlan) apply(src []byte) []byte {
	out := make([]byte, len(src))
	copy(out, src)
	blank := func(a, b int) {
		for i := a; i < b && i < len(out); i++ {
			if out[i] != '\n' && out[i] != '\r' {
				out[i] = ' '
			}
		}
	}
	for _, s := range p.blanked {
		blank(s.start, s.end)
	}
	for i := range p.groups {
		if !p.selected[i] {
			continue
		}
		g := &p.groups[i]
		for _, b := range g.branches {
			blank(b.dir.start, b.dir.end)
		}
		blank(g.endif.start, g.endif.end)
	}
	return out
}

// inBlanked reports whether byte offset pos lies in a blanked branch.
func (p *condPlan) inBlanked(pos int) bool {
	if p == nil {
		return false
	}
	for _, s := range p.blanked {
		if pos >= s.start && pos < s.end {
			return true
		}
	}
	return false
}
