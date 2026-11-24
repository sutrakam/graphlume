package api

import (
	"regexp"
	"strings"
)

// Python routers mounted from another file.
//
// FastAPI and Flask applications rarely declare their routes where the prefix
// is chosen. A module declares `router = APIRouter()` (or a Blueprint) and its
// routes; the application module imports it and mounts it:
//
//	from app.api import users
//	app.include_router(users.router, prefix="/users")
//
// Extract sees one file at a time, so the route in users.py is "GET /{id}",
// not "GET /users/{id}", and a client calling /users/42 binds to nothing.
// What a file says about its routers is read here; the files are joined by a
// pass that sees the whole run.

// PyMount is one include_router or register_blueprint call.
type PyMount struct {
	// Parent is the router or app the child is mounted on, a variable of the
	// mounting file.
	Parent string
	// Child is the mounted router as written: `users.router`, `users_router`.
	Child string
	// Prefix is the call's prefix= or url_prefix=, "" when there is none.
	Prefix string
	Line   int
}

// PyRouters is what one Python file says about its routers.
type PyRouters struct {
	// Prefixes is the prefix each router variable carries within the file:
	// its own declaration's, and that of any mount in the same file.
	Prefixes map[string]string
	// Mounts are the file's include_router and register_blueprint calls.
	Mounts []PyMount
	// Receivers maps a route decorator's or add_api_route call's line
	// (1-based, as Route.Line) to the router variable it is called on.
	Receivers map[int]string
	// Imports maps each name the file's imports bind to what it names.
	Imports map[string]PyImport
	// Consts maps each module-level name bound to a string literal.
	Consts map[string]string
	// Factories are the file's router factories; FactoryCalls the calls that
	// may invoke one, here or in another file.
	Factories    []PyFactory
	FactoryCalls []PyFactoryCall
	// TableLoops are the loops that register routes over a table this file
	// does not define.
	TableLoops []PyTableLoop
}

// PyImport is one name bound by an import statement: `from Module import
// Name`, or `import Module` when Name is empty. Module keeps the leading dots
// of a relative import.
type PyImport struct {
	Module string
	Name   string
}

var (
	pyMountStart  = regexp.MustCompile(`\b(\w+)\.(?:include_router|register_blueprint)\s*\(`)
	pyMountChild  = regexp.MustCompile(`(?:include_router|register_blueprint)\s*\(\s*(?:router\s*=\s*|blueprint\s*=\s*)?([A-Za-z_][\w.]*)`)
	pyMountPrefix = regexp.MustCompile(`\b(?:url_)?prefix\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	pyFromImport  = regexp.MustCompile(`^from\s+(\.*[\w.]*)\s+import\s+(.+)$`)
	pyPlainImport = regexp.MustCompile(`^import\s+(.+)$`)
	pyImportItem  = regexp.MustCompile(`^([\w.]+)(?:\s+as\s+(\w+))?$`)
)

// PythonRouters reads one Python file's routers, mounts and imports.
func PythonRouters(src []byte) PyRouters {
	return pythonRoutersOf(pyCodeLines(strings.Split(string(src), "\n")))
}

func pythonRoutersOf(lines []string) (out PyRouters) {
	out = PyRouters{
		Prefixes:  pyRouterPrefixes(lines),
		Receivers: map[int]string{},
		Imports:   pythonImports(lines),
		Consts:    pyStringConsts(lines),
	}
	defer func() {
		defs := pyModuleDefs(lines)
		tables := pyLiteralTables(lines)
		out.Factories = pyFactories(lines, defs, out.Receivers)
		pyScopedReceivers(lines, defs, out.Receivers)
		out.FactoryCalls = pyFactoryCalls(lines, out.Factories, out.Consts, tables)
		out.TableLoops = pyTableLoops(lines, tables)
	}()
	loops := pyRouterLoops(lines)
	for i := 0; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if m := pyDecoratorStart.FindStringSubmatch(t); m != nil {
			out.Receivers[i+1] = m[1]
			continue
		}
		if m := pyAddAPIRoute.FindStringSubmatch(t); m != nil {
			out.Receivers[i+1] = m[1]
			continue
		}
		if strings.HasPrefix(t, "#") {
			continue
		}
		m := pyMountStart.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		call, _ := readBalancedParen(lines, i, 10)
		c := pyMountChild.FindStringSubmatch(call)
		if c == nil {
			continue
		}
		// include_router(build_router(...)): a factory's router, mounted per
		// call. FactoryCalls carries it; the callee is not a router variable.
		if rest := call[strings.Index(call, c[0])+len(c[0]):]; strings.HasPrefix(strings.TrimSpace(rest), "(") {
			continue
		}
		mount := PyMount{Parent: m[1], Child: c[1], Line: i + 1}
		if p := pyMountPrefix.FindStringSubmatch(call); p != nil {
			mount.Prefix = p[1] + p[2]
		}
		// for r in (users.router, orders.router): app.include_router(r, ...)
		// mounts each router the loop names.
		if items, ok := loops.itemsAt(c[1], i); ok {
			for _, item := range items {
				each := mount
				each.Child = item
				out.Mounts = append(out.Mounts, each)
			}
			continue
		}
		out.Mounts = append(out.Mounts, mount)
	}
	return out
}

var pyForStart = regexp.MustCompile(`^for\s+(\w+)\s+in\s*([\(\[])`)

// pyLoop is a for loop over a literal tuple or list of names.
type pyLoop struct {
	variable   string
	items      []string
	start, end int // body lines, [start, end)
}

type pyLoops []pyLoop

// itemsAt returns the names a loop variable ranges over at line i, when i
// is inside the body of a loop over a literal tuple or list of names.
func (ls pyLoops) itemsAt(variable string, i int) ([]string, bool) {
	for _, l := range ls {
		if l.variable == variable && i >= l.start && i < l.end {
			return l.items, true
		}
	}
	return nil, false
}

// pyRouterLoops finds the file's for loops over a literal tuple or list
// whose every item is a (dotted) name.
func pyRouterLoops(lines []string) pyLoops {
	var out pyLoops
	for i := 0; i < len(lines); i++ {
		raw := lines[i]
		t := strings.TrimSpace(raw)
		m := pyForStart.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		open := m[2][0]
		closer := byte(')')
		if open == '[' {
			closer = ']'
		}
		// Read the iterable to its closing bracket, across lines.
		var b strings.Builder
		depth, j, done := 0, i, false
		for ; j < len(lines) && j < i+60 && !done; j++ {
			seg := lines[j]
			if j == i {
				seg = t[strings.IndexByte(t, open):]
			}
			for k := 0; k < len(seg); k++ {
				c := seg[k]
				switch c {
				case '(', '[', '{':
					depth++
				case ')', ']', '}':
					depth--
				}
				if depth == 0 && c == closer {
					b.WriteString(seg[1:k])
					if j != i {
						b.Reset()
						b.WriteString(pyJoinedIterable(lines, i, j, k, open))
					}
					done = true
					break
				}
			}
			if done {
				break
			}
		}
		if !done {
			continue
		}
		var items []string
		for _, it := range splitTopLevelArgs(b.String() + ")") {
			it = strings.TrimSpace(it)
			if !pyDottedName.MatchString(it) {
				items = nil
				break
			}
			items = append(items, it)
		}
		if len(items) == 0 {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " \t"))
		end := j + 1
		for ; end < len(lines); end++ {
			l := lines[end]
			if strings.TrimSpace(l) == "" {
				continue
			}
			if len(l)-len(strings.TrimLeft(l, " \t")) <= indent {
				break
			}
		}
		out = append(out, pyLoop{variable: m[1], items: items, start: j + 1, end: end})
	}
	return out
}

// pyJoinedIterable is the text between a loop's opening bracket on line i
// and its closing one at column k of line j.
func pyJoinedIterable(lines []string, i, j, k int, open byte) string {
	first := strings.TrimSpace(lines[i])
	var b strings.Builder
	b.WriteString(first[strings.IndexByte(first, open)+1:])
	for n := i + 1; n < j; n++ {
		b.WriteString(" ")
		b.WriteString(lines[n])
	}
	b.WriteString(" ")
	b.WriteString(lines[j][:k])
	return b.String()
}

var pyDottedName = regexp.MustCompile(`^[A-Za-z_][\w]*(?:\.[A-Za-z_]\w*)*$`)

// pythonImports maps every name a file's top-level imports bind.
func pythonImports(lines []string) map[string]PyImport {
	out := map[string]PyImport{}
	for i := 0; i < len(lines); i++ {
		t := stripPyComment(strings.TrimSpace(lines[i]))
		if m := pyFromImport.FindStringSubmatch(t); m != nil {
			names := m[2]
			// from x import (\n a,\n b as c,\n)
			if strings.HasPrefix(names, "(") && !strings.Contains(names, ")") {
				for j := i + 1; j < len(lines) && j < i+200; j++ {
					next := stripPyComment(strings.TrimSpace(lines[j]))
					names += " " + next
					i = j
					if strings.Contains(next, ")") {
						break
					}
				}
			}
			names = strings.Trim(strings.TrimSpace(names), "()")
			for _, item := range strings.Split(names, ",") {
				im := pyImportItem.FindStringSubmatch(strings.TrimSpace(strings.Trim(item, "() \\")))
				if im == nil || strings.Contains(im[1], ".") {
					continue
				}
				alias := im[1]
				if im[2] != "" {
					alias = im[2]
				}
				out[alias] = PyImport{Module: m[1], Name: im[1]}
			}
			continue
		}
		if m := pyPlainImport.FindStringSubmatch(t); m != nil {
			for _, item := range strings.Split(m[1], ",") {
				im := pyImportItem.FindStringSubmatch(strings.TrimSpace(item))
				if im == nil {
					continue
				}
				// `import a.b.c` binds the dotted name as written; an alias
				// binds just the alias.
				alias := im[1]
				if im[2] != "" {
					alias = im[2]
				}
				out[alias] = PyImport{Module: im[1]}
			}
		}
	}
	return out
}

func stripPyComment(s string) string {
	if i := strings.IndexByte(s, '#'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// ResolveMountTarget turns a mount's child expression into the module that
// declares the router and the router's variable name there, using the
// mounting file's imports. ok is false for a name the imports do not explain,
// which includes a router declared in the mounting file itself (Extract has
// already applied that prefix).
func (r PyRouters) ResolveMountTarget(child string) (module, variable string, ok bool) {
	parts := strings.Split(child, ".")
	if len(parts) == 1 {
		imp, found := r.Imports[child]
		if !found || imp.Name == "" {
			return "", "", false
		}
		// from app.api.users import router as users_router
		return imp.Module, imp.Name, true
	}
	// users.router, api.v1.users.router: the longest imported head names the
	// module, the rest is modules beneath it and then the variable.
	for k := len(parts) - 1; k >= 1; k-- {
		head := strings.Join(parts[:k], ".")
		imp, found := r.Imports[head]
		if !found {
			continue
		}
		mod := imp.Module
		if imp.Name != "" {
			mod = joinModule(mod, imp.Name)
		}
		for _, p := range parts[k : len(parts)-1] {
			mod = joinModule(mod, p)
		}
		return mod, parts[len(parts)-1], true
	}
	return "", "", false
}

// joinModule appends a name to a module path, minding a bare relative one:
// "." + "users" is ".users", not "..users".
func joinModule(mod, name string) string {
	if mod == "" || strings.Trim(mod, ".") == "" {
		return mod + name
	}
	return mod + "." + name
}

// JoinRoutePath composes a mount prefix with a route path.
func JoinRoutePath(prefix, path string) string { return joinRoutePath(prefix, path) }
