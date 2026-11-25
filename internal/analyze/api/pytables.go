package api

import (
	"regexp"
	"strings"
)

// Python routes registered from a literal table.
//
// A service that serves one family of routes per data set commonly writes the
// family once and registers it from a table:
//
//	DATA_SETS = ("orders", "refunds")
//	for _ds in DATA_SETS:
//	    router.add_api_route(f"/{_ds}/values", values, methods=["POST"])
//
//	ROUTES = [("GET", "/ping", ping), ("POST", "/echo", echo)]
//	for method, path, handler in ROUTES:
//	    router.add_api_route(path, handler, methods=[method])
//
// When the table is a literal the author wrote (a tuple, list, set or dict of
// strings, names and tuples of those) the routes are as static as decorators
// and are read once per row. A table built at run time (a comprehension, a
// call, a registry filled by imports) is not guessed at: its routes keep the
// path as written and are marked as registered at run time.

// pyValue is one literal value: a string, a (dotted) name, or a tuple or list
// of values.
type pyValue struct {
	str   string
	ident string
	isStr bool
	items []pyValue
	isSeq bool
}

// pyTable is a literal collection: the items of a tuple, list or set, or the
// keys and values of a dict.
type pyTable struct {
	keys   []pyValue
	values []pyValue
	dict   bool
}

// parsePyValue reads one literal value. ok is false for anything that is not
// a string, a name, or a tuple or list of those.
func parsePyValue(expr string) (pyValue, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return pyValue{}, false
	}
	if s, ok := pyStringLiteral(expr); ok {
		return pyValue{str: s, isStr: true}, true
	}
	if pyDottedName.MatchString(expr) {
		return pyValue{ident: expr}, true
	}
	if open := expr[0]; open == '(' || open == '[' {
		closer := byte(')')
		if open == '[' {
			closer = ']'
		}
		if expr[len(expr)-1] != closer {
			return pyValue{}, false
		}
		var items []pyValue
		for _, part := range splitTopLevelArgs(expr[1:len(expr)-1] + ")") {
			v, ok := parsePyValue(part)
			if !ok {
				return pyValue{}, false
			}
			items = append(items, v)
		}
		return pyValue{items: items, isSeq: true}, true
	}
	return pyValue{}, false
}

// parsePyTable reads a literal tuple, list, set or dict.
func parsePyTable(expr string) (pyTable, bool) {
	expr = strings.TrimSpace(expr)
	if len(expr) < 2 {
		return pyTable{}, false
	}
	open, last := expr[0], expr[len(expr)-1]
	if (open != '(' || last != ')') && (open != '[' || last != ']') && (open != '{' || last != '}') {
		return pyTable{}, false
	}
	parts := splitTopLevelArgs(expr[1:len(expr)-1] + ")")
	var t pyTable
	for i, part := range parts {
		if k, v, isPair := pyDictPair(part); isPair {
			if open != '{' || (i > 0 && !t.dict) {
				return pyTable{}, false
			}
			t.dict = true
			kv, ok1 := parsePyValue(k)
			vv, ok2 := parsePyValue(v)
			if !ok1 || !ok2 {
				return pyTable{}, false
			}
			t.keys = append(t.keys, kv)
			t.values = append(t.values, vv)
			continue
		}
		if t.dict {
			return pyTable{}, false
		}
		v, ok := parsePyValue(part)
		if !ok {
			return pyTable{}, false
		}
		t.keys = append(t.keys, v)
	}
	// "(x)" is a parenthesised value, not a tuple; a table of one is "(x,)".
	if open == '(' && len(parts) == 1 && !strings.HasSuffix(strings.TrimSpace(expr[1:len(expr)-1]), ",") {
		return pyTable{}, false
	}
	return t, len(t.keys) > 0
}

// pyDictPair splits `key: value` at its top-level colon.
func pyDictPair(s string) (key, val string, ok bool) {
	depth := 0
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
		case ':':
			if depth == 0 {
				return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:]), true
			}
		}
	}
	return "", "", false
}

// NAME = <literal> or NAME: ann = <literal>, at module level.
var pyTableAssign = regexp.MustCompile(`^([A-Za-z_]\w*)\s*(?::\s*[^=]+)?=\s*([\(\[\{].*)$`)

// pyLiteralTables maps each module-level name bound to a literal table. A
// name bound twice is dropped: which binding a loop sees depends on order.
func pyLiteralTables(code []string) map[string]pyTable {
	out := map[string]pyTable{}
	seen := map[string]int{}
	for i := 0; i < len(code); i++ {
		l := code[i]
		if l == "" || l[0] == ' ' || l[0] == '\t' {
			continue
		}
		m := pyTableAssign.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		expr, end := readBracketed(code, i, strings.Index(l, m[2]), 400)
		seen[m[1]]++
		if t, ok := parsePyTable(expr); ok {
			out[m[1]] = t
		}
		i = end
	}
	for k, n := range seen {
		if n > 1 {
			delete(out, k)
		}
	}
	return out
}

// PyLiteralTable returns the literal text a module binds name to at module
// level, when that is a literal table (read by another file's loop).
func PyLiteralTable(src []byte, name string) (string, bool) {
	code := pyCodeLines(strings.Split(string(src), "\n"))
	if _, ok := pyLiteralTables(code)[name]; !ok {
		return "", false
	}
	for i := 0; i < len(code); i++ {
		l := code[i]
		if l == "" || l[0] == ' ' || l[0] == '\t' {
			continue
		}
		m := pyTableAssign.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil || m[1] != name {
			continue
		}
		expr, _ := readBracketed(code, i, strings.Index(l, m[2]), 400)
		return expr, true
	}
	return "", false
}

// readBracketed reads from column col of line i to the bracket that closes
// the one there, across lines. It returns the text, joined with spaces, and
// the index of the line it ends on.
func readBracketed(code []string, i, col, maxLines int) (string, int) {
	var b strings.Builder
	depth := 0
	var quote byte
	for j := i; j < len(code) && j < i+maxLines; j++ {
		seg := code[j]
		if j == i {
			seg = seg[col:]
		} else {
			b.WriteByte(' ')
		}
		for k := 0; k < len(seg); k++ {
			c := seg[k]
			b.WriteByte(c)
			if quote != 0 {
				if c == '\\' && k+1 < len(seg) {
					k++
					b.WriteByte(seg[k])
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
				if depth == 0 {
					return b.String(), j
				}
			}
		}
		quote = 0 // a string does not continue past its line
	}
	return "", i
}

// pyFor is a module-level for loop: its targets, its iterable as written, and
// its body's lines [start, end).
type pyFor struct {
	targets    []string
	iter       string
	line       int // header, 0-based
	start, end int
}

var pyForHeader = regexp.MustCompile(`^for\s+(.+?)\s+in\s+(.*)$`)

// pyModuleFors finds the file's module-level for loops.
func pyModuleFors(code []string) []pyFor {
	var out []pyFor
	for i := 0; i < len(code); i++ {
		l := code[i]
		if l == "" || l[0] == ' ' || l[0] == '\t' {
			continue
		}
		t := strings.TrimSpace(l)
		m := pyForHeader.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		iter := m[2]
		hdrEnd := i
		if !strings.HasSuffix(iter, ":") {
			// The iterable runs over several lines.
			col := strings.LastIndex(l, iter)
			if col < 0 || !strings.ContainsAny(iter[:1], "([{") {
				continue
			}
			expr, end := readBracketed(code, i, col, 400)
			if expr == "" {
				continue
			}
			rest := strings.TrimSpace(code[end][strings.LastIndexAny(code[end], ")]}")+1:])
			if rest != ":" {
				continue
			}
			iter, hdrEnd = expr, end
		} else {
			iter = strings.TrimSpace(strings.TrimSuffix(iter, ":"))
		}
		targets := pyForTargets(m[1])
		if targets == nil {
			continue
		}
		end := pyBlockEnd(code, hdrEnd+1)
		out = append(out, pyFor{targets: targets, iter: iter, line: i, start: hdrEnd + 1, end: end})
		i = end - 1
	}
	return out
}

// pyForTargets reads `a`, `a, b` or `(a, b)`; nil for nested unpacking.
func pyForTargets(s string) []string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		s = s[1 : len(s)-1]
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !pyIdent.MatchString(p) {
			return nil
		}
		out = append(out, p)
	}
	return out
}

// pyIterHead splits an iterable into the table it names and how it is walked:
// `X`, `X.keys()`, `X.values()`, `X.items()`.
func pyIterHead(iter string) (head, walk string) {
	iter = strings.TrimSpace(iter)
	for _, w := range []string{"keys", "values", "items"} {
		if h, ok := strings.CutSuffix(iter, "."+w+"()"); ok {
			return strings.TrimSpace(h), w
		}
	}
	return iter, ""
}

// pyLoopBindings returns the names each iteration of a loop binds, when its
// iterable is a literal table (inline, or a name in tables). Strings bind as
// values; names bind under pyIdentKey. ok is false when the iterable is not a
// literal table or a row does not unpack into the targets.
func pyLoopBindings(f pyFor, tables map[string]pyTable) ([]map[string]string, bool) {
	head, walk := pyIterHead(f.iter)
	t, ok := tables[head]
	if !ok {
		if t, ok = parsePyTable(head); !ok {
			return nil, false
		}
	}
	var rows []pyValue
	switch {
	case walk == "items" && t.dict:
		for i := range t.keys {
			rows = append(rows, pyValue{items: []pyValue{t.keys[i], t.values[i]}, isSeq: true})
		}
	case walk == "values" && t.dict:
		rows = t.values
	case walk == "" || (walk == "keys" && t.dict):
		rows = t.keys
	default:
		return nil, false
	}
	var out []map[string]string
	for _, row := range rows {
		b := map[string]string{}
		if len(f.targets) == 1 {
			if !bindPyValue(b, f.targets[0], row) {
				return nil, false
			}
		} else {
			if !row.isSeq || len(row.items) != len(f.targets) {
				return nil, false
			}
			for k, target := range f.targets {
				if !bindPyValue(b, target, row.items[k]) {
					return nil, false
				}
			}
		}
		out = append(out, b)
	}
	return out, len(out) > 0
}

// pyIdentKey is the key a name binding is kept under in a names map, apart
// from string bindings: `for h in (ping, echo)` binds h to the function ping,
// not to the string "ping".
func pyIdentKey(name string) string { return "\x00" + name }

func bindPyValue(b map[string]string, target string, v pyValue) bool {
	switch {
	case v.isStr:
		b[target] = v.str
	case v.ident != "":
		b[pyIdentKey(target)] = v.ident
	case v.isSeq:
		// A row bound whole: only its parts can be used, and they are not.
	default:
		return false
	}
	return true
}

// PyTableLoop is a module-level loop that registers routes over a table this
// file does not define: one it imports, or one it names through a module.
type PyTableLoop struct {
	// Table is the name the iterable reads, as written: `DATA_SETS`,
	// `registry.DATA_SETS`.
	Table string
	// Line is the loop's header line, 1-based.
	Line int
}

// pyTableLoops finds the module-level loops whose body registers routes,
// directly or through a module-level function of the same file, over a table
// that is not a literal of this file.
func pyTableLoops(code []string, tables map[string]pyTable) []PyTableLoop {
	declares := pyRouteDeclaringDefs(code)
	var out []PyTableLoop
	for _, f := range pyModuleFors(code) {
		if _, ok := pyLoopBindings(f, tables); ok {
			continue
		}
		head, _ := pyIterHead(f.iter)
		if !pyDottedName.MatchString(head) {
			continue
		}
		if !pyBodyRegisters(code[f.start:f.end], declares) {
			continue
		}
		out = append(out, PyTableLoop{Table: head, Line: f.line + 1})
	}
	return out
}

// pyRouteDeclaringDefs names the module-level functions that declare routes.
func pyRouteDeclaringDefs(code []string) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < len(code); i++ {
		l := code[i]
		if l == "" || l[0] == ' ' || l[0] == '\t' {
			continue
		}
		m := pyModuleDef.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		_, sigEnd := readBalancedParen(code, i, 20)
		end := pyBlockEnd(code, sigEnd+1)
		if pyDeclaresRoutes(code[sigEnd+1 : end]) {
			out[m[1]] = true
		}
		i = end - 1
	}
	return out
}

func pyBodyRegisters(body []string, declares map[string]bool) bool {
	if pyDeclaresRoutes(body) {
		return true
	}
	for _, l := range body {
		if m := pyHelperCall.FindStringSubmatch(strings.TrimSpace(l)); m != nil && declares[m[1]] {
			return true
		}
	}
	return false
}

// pyInDefOrFor reports whether line i is inside the body of a def or a for
// loop: where a route registered with a path that is not a literal takes its
// path from a value only known when the code runs.
func pyInDefOrFor(code []string, i int) bool {
	indent := pyIndent(code[i])
	for j := i - 1; j >= 0 && indent > 0; j-- {
		l := code[j]
		if strings.TrimSpace(l) == "" {
			continue
		}
		ind := pyIndent(l)
		if ind >= indent {
			continue
		}
		indent = ind
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "def ") || strings.HasPrefix(t, "async def ") ||
			strings.HasPrefix(t, "for ") || strings.HasPrefix(t, "async for ") {
			return true
		}
	}
	return false
}

func pyIndent(l string) int { return len(l) - len(strings.TrimLeft(l, " \t")) }

// RuntimeRouteDoc marks an endpoint whose path is built from a value known
// only when the code runs: a loop over a table that is not a literal, or a
// function argument no call states. The path is reported as written.
const RuntimeRouteDoc = "route registered at run time: its path is built from values the source does not state"

// RuntimeRouteResolvedDoc marks such an endpoint once a pass that sees the
// whole run has read the values (an imported literal table, a factory's call
// arguments) and named it after the path it is served at.
const RuntimeRouteResolvedDoc = "route registered at run time: path read from the values its registration is given"
