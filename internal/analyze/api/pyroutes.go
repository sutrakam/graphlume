package api

import (
	"regexp"
	"strings"
)

// Python route paths that are not a single string literal.
//
// A FastAPI module often spells its paths through the module's own names
// rather than inline literals:
//
//	_BASE = "/widgets"
//
//	@router.get(f"{_BASE}/{{widget_id}}")     # an f-string over a constant
//	@router.get(WIDGETS_PATH)                 # a constant
//	router.add_api_route("/ping", ping)       # the imperative registration
//
// and a family of near-identical routes is commonly registered by a helper the
// module calls once per resource:
//
//	def _register(kind: str) -> None:
//	    router.add_api_route(f"/{kind}/summary", summary, methods=["GET"])
//
//	_register("orders")
//	for _k in ("invoices", "refunds"):
//	    _register(_k)
//
// Everything here is read from the one file, and only from string literals the
// author wrote: a name bound at run time, or passed in from another module,
// leaves the route as written rather than guessed.

var (
	// NAME = "literal" or NAME: str = "literal", at module level.
	pyStringConst = regexp.MustCompile(`^([A-Za-z_]\w*)\s*(?::\s*[\w\[\]., ]+)?=\s*([rRuU]?(?:"[^"\\]*"|'[^'\\]*'))\s*$`)
	// router.add_api_route(...), app.add_api_route(...)
	pyAddAPIRoute = regexp.MustCompile(`^([\w.]+)\.\s*add_api_route\s*\(`)
	pyModuleDef   = regexp.MustCompile(`^(?:async\s+)?def\s+(\w+)\s*\(`)
	pyHelperCall  = regexp.MustCompile(`^(\w+)\s*\(`)
	pyIdent       = regexp.MustCompile(`^[A-Za-z_]\w*$`)
	pyFStringRef  = regexp.MustCompile(`^[A-Za-z_]\w*$`)
)

// pyCodeLines returns the file's lines with each trailing # comment removed,
// so a comment inside a multi-line decorator neither supplies its first
// quoted string nor unbalances its parentheses. Line numbers are kept.
func pyCodeLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = stripPyLineComment(l)
	}
	return out
}

func stripPyLineComment(l string) string {
	if !strings.Contains(l, "#") {
		return l
	}
	var quote byte
	for i := 0; i < len(l); i++ {
		c := l[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '#':
			return strings.TrimRight(l[:i], " \t")
		}
	}
	return l
}

// pyStringConsts maps each module-level name bound to a plain string literal.
// A name bound twice to different values is dropped: which one a route sees
// depends on order of execution, and guessing would invent a path.
func pyStringConsts(code []string) map[string]string {
	out := map[string]string{}
	bad := map[string]bool{}
	for _, l := range code {
		if l == "" || l[0] == ' ' || l[0] == '\t' {
			continue
		}
		m := pyStringConst.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		v, ok := pyStringLiteral(m[2])
		if !ok {
			continue
		}
		if old, seen := out[m[1]]; seen && old != v {
			bad[m[1]] = true
		}
		out[m[1]] = v
	}
	for k := range bad {
		delete(out, k)
	}
	return out
}

// pyStringLiteral reads a whole plain (or raw) string literal.
func pyStringLiteral(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s != "" && strings.ContainsRune("rRuU", rune(s[0])) {
		s = s[1:]
	}
	if len(s) < 2 || (s[0] != '"' && s[0] != '\'') || s[len(s)-1] != s[0] {
		return "", false
	}
	body := s[1 : len(s)-1]
	if strings.ContainsRune(body, rune(s[0])) {
		return "", false
	}
	return body, true
}

// pyStringExpr evaluates a path argument built from literals: a string, an
// f-string, a name bound to a string, or a + of those. ok is false for
// anything else. dynamic is true when an f-string interpolates a name that is
// not known; the placeholder is kept as written.
func pyStringExpr(expr string, names map[string]string) (val string, dynamic, ok bool) {
	parts := splitPyConcat(expr)
	if len(parts) == 0 {
		return "", false, false
	}
	var b strings.Builder
	for _, p := range parts {
		v, dyn, ok := pyStringAtom(p, names)
		if !ok {
			return "", false, false
		}
		dynamic = dynamic || dyn
		b.WriteString(v)
	}
	return b.String(), dynamic, true
}

func pyStringAtom(s string, names map[string]string) (string, bool, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false, false
	}
	if pyIdent.MatchString(s) {
		v, ok := names[s]
		return v, false, ok
	}
	prefix := ""
	for len(s) > 0 && strings.ContainsRune("rRfFuUbB", rune(s[0])) && len(prefix) < 2 {
		prefix += strings.ToLower(s[:1])
		s = s[1:]
	}
	if strings.Contains(prefix, "b") {
		return "", false, false
	}
	body, ok := pyStringLiteral(s)
	if !ok {
		return "", false, false
	}
	if !strings.Contains(prefix, "f") {
		return body, false, true
	}
	v, dyn := pyFormat(body, names)
	return v, dyn, true
}

// pyFormat renders an f-string's body: {{ and }} are literal braces, {name}
// is the name's value when it is known, and any other field is kept as
// written and marks the result dynamic.
func pyFormat(body string, names map[string]string) (string, bool) {
	var b strings.Builder
	dynamic := false
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case c == '{' && i+1 < len(body) && body[i+1] == '{':
			b.WriteByte('{')
			i++
		case c == '}' && i+1 < len(body) && body[i+1] == '}':
			b.WriteByte('}')
			i++
		case c == '{':
			end := strings.IndexByte(body[i:], '}')
			if end < 0 {
				b.WriteString(body[i:])
				return b.String(), true
			}
			field := strings.TrimSpace(body[i+1 : i+end])
			if v, ok := names[field]; ok && pyFStringRef.MatchString(field) {
				b.WriteString(v)
			} else {
				b.WriteString(body[i : i+end+1])
				dynamic = true
			}
			i += end
		default:
			b.WriteByte(c)
		}
	}
	return b.String(), dynamic
}

// splitPyConcat splits an expression at top-level + signs.
func splitPyConcat(s string) []string {
	var parts []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case '+':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, s[start:])
}

// pyCallArgs returns the top-level arguments of the call that opens at the
// first "(" of call.
func pyCallArgs(call string) []string {
	_, args, ok := strings.Cut(call, "(")
	if !ok {
		return nil
	}
	return splitTopLevelArgs(args)
}

// pyPathArg resolves a route call's path argument: the first positional one,
// or path=. ok is false when there is none or it is not built from literals.
func pyPathArg(args []string, names map[string]string) (path string, dynamic, ok bool) {
	for i, a := range args {
		if k, v, isKw := pyKeywordArg(a); isKw {
			if k == "path" || k == "rule" {
				return pyStringExpr(v, names)
			}
			continue
		}
		if i == 0 {
			return pyStringExpr(a, names)
		}
	}
	return "", false, false
}

// pyKeywordArg splits `name=value`; isKw is false for a positional argument
// (including one that merely contains == or an = inside brackets or quotes).
func pyKeywordArg(a string) (key, val string, isKw bool) {
	a = strings.TrimSpace(a)
	i := strings.IndexByte(a, '=')
	if i <= 0 || (i+1 < len(a) && a[i+1] == '=') {
		return "", "", false
	}
	k := strings.TrimSpace(a[:i])
	if !pyIdent.MatchString(k) {
		return "", "", false
	}
	return k, strings.TrimSpace(a[i+1:]), true
}

// pyRouteHelper is a module-level function that declares routes, and the
// literal arguments it is called with at module level.
type pyRouteHelper struct {
	name       string
	params     []string
	defaults   map[string]string
	start, end int // body lines, [start, end)
	calls      []map[string]string
}

// pyRouteHelpers finds the module-level functions whose bodies declare
// routes and are called from module level with literal arguments, directly
// or from a for loop over a literal tuple or list of strings. Only those are
// returned: a helper never called that way keeps its routes as written.
func pyRouteHelpers(code []string, consts map[string]string, tables map[string]pyTable) []pyRouteHelper {
	var helpers []pyRouteHelper
	byName := map[string]int{}
	for i := 0; i < len(code); i++ {
		l := code[i]
		if l == "" || l[0] == ' ' || l[0] == '\t' {
			continue
		}
		m := pyModuleDef.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		sig, sigEnd := readBalancedParen(code, i, 20)
		end := pyBlockEnd(code, sigEnd+1)
		if !pyDeclaresRoutes(code[sigEnd+1 : end]) {
			i = end - 1
			continue
		}
		params, defaults := pyParams(sig)
		byName[m[1]] = len(helpers)
		helpers = append(helpers, pyRouteHelper{
			name: m[1], params: params, defaults: defaults,
			start: sigEnd + 1, end: end,
		})
		i = end - 1
	}
	if len(helpers) == 0 {
		return nil
	}

	bind := func(h *pyRouteHelper, call string, names map[string]string) {
		args := pyCallArgs(call)
		b := map[string]string{}
		for k, v := range h.defaults {
			b[k] = v
		}
		pos := 0
		for _, a := range args {
			if k, v, isKw := pyKeywordArg(a); isKw {
				if s, dyn, ok := pyStringExpr(v, names); ok && !dyn {
					b[k] = s
				}
				continue
			}
			if pos < len(h.params) {
				if s, dyn, ok := pyStringExpr(a, names); ok && !dyn {
					b[h.params[pos]] = s
				} else if id, ok := names[pyIdentKey(strings.TrimSpace(a))]; ok {
					b[pyIdentKey(h.params[pos])] = id
				}
			}
			pos++
		}
		if len(b) > 0 {
			h.calls = append(h.calls, b)
		}
	}

	loops := map[int]pyFor{}
	for _, f := range pyModuleFors(code) {
		loops[f.line] = f
	}
	for i := 0; i < len(code); i++ {
		l := code[i]
		if l == "" || l[0] == ' ' || l[0] == '\t' {
			continue
		}
		t := strings.TrimSpace(l)
		if m := pyHelperCall.FindStringSubmatch(t); m != nil {
			if k, ok := byName[m[1]]; ok {
				call, _ := readBalancedParen(code, i, 20)
				bind(&helpers[k], call, consts)
			}
			continue
		}
		f, isLoop := loops[i]
		if !isLoop {
			continue
		}
		// for _k in ("a", "b"): / for _k in KINDS: with KINDS a literal table.
		rows, ok := pyLoopBindings(f, tables)
		for j := f.start; j < f.end && ok; j++ {
			cm := pyHelperCall.FindStringSubmatch(strings.TrimSpace(code[j]))
			if cm == nil {
				continue
			}
			k, ok := byName[cm[1]]
			if !ok {
				continue
			}
			call, _ := readBalancedParen(code, j, 20)
			for _, row := range rows {
				names := make(map[string]string, len(consts)+len(row))
				for n, v := range consts {
					names[n] = v
				}
				for n, v := range row {
					names[n] = v
				}
				bind(&helpers[k], call, names)
			}
		}
		i = f.end - 1
	}

	kept := helpers[:0]
	for _, h := range helpers {
		if len(h.calls) > 0 {
			kept = append(kept, h)
		}
	}
	return kept
}

// pyBlockEnd is the index of the first line at or after from that is
// unindented code, ending the block the lines before it belong to.
func pyBlockEnd(code []string, from int) int {
	for j := from; j < len(code); j++ {
		l := code[j]
		if strings.TrimSpace(l) == "" {
			continue
		}
		if l[0] != ' ' && l[0] != '\t' && l[0] != ')' {
			return j
		}
	}
	return len(code)
}

func pyDeclaresRoutes(body []string) bool {
	for _, l := range body {
		t := strings.TrimSpace(l)
		if pyDecoratorStart.MatchString(t) || pyAddAPIRoute.MatchString(t) {
			return true
		}
	}
	return false
}

// pyParams reads a def's parameter names in order, and the string defaults
// of those that have one.
func pyParams(sig string) ([]string, map[string]string) {
	var names []string
	defaults := map[string]string{}
	for _, a := range pyCallArgs(sig) {
		a = strings.TrimSpace(a)
		if a == "" || a == "*" || a == "/" || strings.HasPrefix(a, "*") {
			continue
		}
		name, def, hasDef := strings.Cut(a, "=")
		name, _, _ = strings.Cut(name, ":")
		name = strings.TrimSpace(name)
		if !pyIdent.MatchString(name) {
			continue
		}
		names = append(names, name)
		if hasDef {
			if v, ok := pyStringLiteral(def); ok {
				defaults[name] = v
			}
		}
	}
	return names, defaults
}
