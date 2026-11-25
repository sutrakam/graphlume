package api

import (
	"regexp"
	"strings"
)

// Python router factories.
//
// A shared module commonly builds a router per caller instead of declaring one
// at module level:
//
//	# shared/approvals.py
//	def build_queue_router(*, prefix: str = "") -> APIRouter:
//	    router = APIRouter(prefix=prefix)
//	    @router.get("/queue")
//	    async def list_queue(): ...
//	    return router
//
//	# orders.py
//	router.include_router(build_queue_router(prefix="/orders"))
//
// or declares its routes on a router it is handed:
//
//	def register_health(router: APIRouter) -> None:
//	    @router.get("/health")
//	    async def health(): ...
//
// The route is served once per call, at the prefix that call chooses, under
// whatever the calling router is mounted at. Extract reads the factory's file
// alone and sees one route at "/queue"; what each call adds is read here, and
// joined across files by a pass that sees the whole run.

// PyFactory is a module-level function that declares routes on a router it
// builds and returns, or on a router it is passed.
type PyFactory struct {
	Name string
	// Line is the def's line, 1-based.
	Line int
	// Router is the variable its routes are declared on.
	Router string
	// Returns is true for a factory that builds Router itself and returns it,
	// false for one that declares routes on a Router parameter.
	Returns bool
	// Prefix is the prefix= its own APIRouter(...) is built with, as written;
	// "" when there is none.
	Prefix string

	params     []string
	defaults   map[string]string
	start, end int
}

// PyFactoryCall is one call of a function that may be a router factory.
type PyFactoryCall struct {
	// Callee is the function as written: `build_router`, `approvals.build_router`.
	Callee string
	Args   []string
	// Line is the call's first line, 1-based.
	Line int
	// Parent is the router or app an inline call is mounted on:
	// `parent.include_router(build_router(...), prefix=...)`.
	Parent string
	// Prefix is that include_router's own prefix=, "" when there is none.
	Prefix string
	// Bound is the variable a call's result is assigned to: `x = build_router()`.
	Bound string
	// Wrapper is set for a call found as the first argument of another call
	// that is mounted: `p.include_router(with_deps(build_router(...), ...))`.
	// The wrapper is assumed to return the router it is given, which is not
	// proven, so the routes are graded medium.
	Wrapper string
	// Names are the string constants (and, in a loop over a literal table,
	// the row's values) in scope at the call, to evaluate its arguments.
	Names map[string]string
}

var (
	pyCallAssign  = regexp.MustCompile(`^([A-Za-z_]\w*)\s*(?::\s*[^=]+)?=\s*([A-Za-z_][\w.]*)\s*\(`)
	pyCallStmt    = regexp.MustCompile(`^([A-Za-z_][\w.]*)\s*\(`)
	pyLocalAssign = regexp.MustCompile(`^([A-Za-z_]\w*)\s*(?::\s*[^=]+)?=[^=]`)
	pyReturnName  = regexp.MustCompile(`^return\s+([A-Za-z_]\w*)\s*$`)
	pyRouterBuild = regexp.MustCompile(`^([A-Za-z_]\w*)\s*(?::\s*[^=]+)?=\s*(?:fastapi\.|flask\.)?(APIRouter|Blueprint)\s*\(`)
	pyInlineCall  = regexp.MustCompile(`^([A-Za-z_][\w.]*)\s*\(`)
)

// pyDef is a module-level function: its name, parameters, and body lines.
type pyDef struct {
	name       string
	line       int // 0-based
	params     []string
	defaults   map[string]string
	start, end int
	locals     map[string]bool // parameters and names assigned in the body
}

func pyModuleDefs(code []string) []pyDef {
	var out []pyDef
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
		params, defaults := pyParams(sig)
		d := pyDef{name: m[1], line: i, params: params, defaults: defaults, start: sigEnd + 1, end: end, locals: map[string]bool{}}
		for _, p := range params {
			d.locals[p] = true
		}
		for j := d.start; j < end; j++ {
			t := strings.TrimSpace(code[j])
			if am := pyLocalAssign.FindStringSubmatch(t); am != nil {
				d.locals[am[1]] = true
			}
			if fm := pyForHeader.FindStringSubmatch(t); fm != nil {
				for _, v := range pyForTargets(fm[1]) {
					d.locals[v] = true
				}
			}
		}
		out = append(out, d)
		i = end - 1
	}
	return out
}

// pyScopedReceivers rewrites the receiver of every route declared inside a
// module-level function on a router local to it (built there, or passed in)
// to "fn().router": a factory's `router` is not the module's `router`, and
// must not take the module router's mount prefix.
func pyScopedReceivers(code []string, defs []pyDef, receivers map[int]string) {
	for _, d := range defs {
		for line, recv := range receivers {
			i := line - 1
			if i < d.start || i >= d.end || strings.Contains(recv, ".") {
				continue
			}
			if d.locals[recv] {
				receivers[line] = d.name + "()." + recv
			}
		}
	}
}

// pyFactories finds the module-level functions that declare routes on one
// router they build and return, or on one router parameter.
func pyFactories(code []string, defs []pyDef, receivers map[int]string) []PyFactory {
	var out []PyFactory
	for _, d := range defs {
		recvs := map[string]bool{}
		for line, recv := range receivers {
			if i := line - 1; i >= d.start && i < d.end {
				recvs[strings.TrimPrefix(recv, d.name+"().")] = true
			}
		}
		if len(recvs) == 0 {
			continue
		}
		built := map[string]string{} // router variable -> its prefix= as written
		returned := map[string]bool{}
		for j := d.start; j < d.end; j++ {
			t := strings.TrimSpace(code[j])
			if bm := pyRouterBuild.FindStringSubmatch(t); bm != nil {
				call, _ := readBalancedParen(code, j, 25)
				prefix := ""
				for _, a := range pyCallArgs(call) {
					if k, v, ok := pyKeywordArg(a); ok && (k == "prefix" || k == "url_prefix") {
						prefix = v
					}
				}
				built[bm[1]] = prefix
			}
			if rm := pyReturnName.FindStringSubmatch(t); rm != nil {
				returned[rm[1]] = true
			}
		}
		f := PyFactory{Name: d.name, Line: d.line + 1, params: d.params, defaults: d.defaults, start: d.start, end: d.end}
		for r := range recvs {
			if pfx, ok := built[r]; ok && returned[r] {
				f.Router, f.Returns, f.Prefix = r, true, pfx
			}
		}
		if f.Router == "" && len(recvs) == 1 {
			for r := range recvs {
				for _, p := range d.params {
					if p == r {
						f.Router = r
					}
				}
			}
		}
		if f.Router != "" {
			out = append(out, f)
		}
	}
	return out
}

// pyFactoryCalls finds every call that may build or fill a router: one
// mounted inline (`p.include_router(f(...))`), one assigned (`x = f(...)`), and
// a bare call statement (`f(router, ...)`). Calls inside the bodies of the
// file's own factories are skipped: they are expanded with their factory, not
// on their own. A call inside a module-level loop over a literal table is read
// once per row, with the row's values in scope.
func pyFactoryCalls(code []string, factories []PyFactory, consts map[string]string, tables map[string]pyTable) []PyFactoryCall {
	inFactory := func(i int) bool {
		for _, f := range factories {
			if i >= f.start && i < f.end {
				return true
			}
		}
		return false
	}
	rowsAt := map[int][]map[string]string{}
	for _, f := range pyModuleFors(code) {
		rows, ok := pyLoopBindings(f, tables)
		if !ok {
			continue
		}
		for j := f.start; j < f.end; j++ {
			rowsAt[j] = rows
		}
	}

	var out []PyFactoryCall
	add := func(c PyFactoryCall, i int) {
		rows, inLoop := rowsAt[i]
		if !inLoop {
			c.Names = consts
			out = append(out, c)
			return
		}
		for _, row := range rows {
			each := c
			each.Names = pyMergeNames(consts, row)
			if id, ok := row[pyIdentKey(c.Parent)]; ok {
				each.Parent = id
			}
			out = append(out, each)
		}
	}
	for i := 0; i < len(code); i++ {
		t := strings.TrimSpace(code[i])
		if t == "" || t[0] == '@' || inFactory(i) {
			continue
		}
		if m := pyMountStart.FindStringSubmatch(t); m != nil {
			call, _ := readBalancedParen(code, i, 40)
			c := PyFactoryCall{Parent: m[1], Line: i + 1}
			for k, a := range pyCallArgs(call) {
				if key, v, isKw := pyKeywordArg(a); isKw {
					switch key {
					case "prefix", "url_prefix":
						c.Prefix = v
					case "router", "blueprint":
						if c.Callee == "" {
							c.Callee, c.Args = pyInlineCallParts(v)
						}
					}
					continue
				}
				if k == 0 {
					c.Callee, c.Args = pyInlineCallParts(a)
				}
			}
			if c.Callee != "" {
				if p, dyn, ok := pyStringExpr(c.Prefix, consts); c.Prefix != "" {
					if !ok || dyn {
						continue // a prefix read at run time: the mount is not known
					}
					c.Prefix = p
				}
				add(c, i)
				// with_deps(build_router(...), ...): the wrapped call too.
				if len(c.Args) > 0 {
					if _, _, isKw := pyKeywordArg(c.Args[0]); !isKw {
						if inner, args := pyInlineCallParts(c.Args[0]); inner != "" {
							w := c
							w.Wrapper, w.Callee, w.Args = c.Callee, inner, args
							add(w, i)
						}
					}
				}
			}
			continue
		}
		if m := pyCallAssign.FindStringSubmatch(t); m != nil {
			call, _ := readBalancedParen(code, i, 40)
			_, args := pyInlineCallParts(call[strings.Index(call, m[2]):])
			add(PyFactoryCall{Callee: m[2], Args: args, Bound: m[1], Line: i + 1}, i)
			continue
		}
		if m := pyCallStmt.FindStringSubmatch(t); m != nil && !strings.HasPrefix(t, "return") {
			call, _ := readBalancedParen(code, i, 40)
			_, args := pyInlineCallParts(call)
			if len(args) > 0 {
				add(PyFactoryCall{Callee: m[1], Args: args, Line: i + 1}, i)
			}
		}
	}
	return out
}

// pyInlineCallParts splits `f(a, b=c)` into its callee and arguments; callee
// is "" when expr is not a call.
func pyInlineCallParts(expr string) (string, []string) {
	expr = strings.TrimSpace(expr)
	m := pyInlineCall.FindStringSubmatch(expr)
	if m == nil {
		return "", nil
	}
	return m[1], pyCallArgs(expr)
}

// ExpandPyFactory returns the routes one call of a factory declares, with the
// call's arguments bound: paths carry the factory router's own prefix, and
// any path built from a parameter takes the argument the call passes.
// routerArg is, for a factory that declares routes on a parameter, the
// caller's expression for that parameter. ok is false when the call does not
// say which router it fills, or passes a prefix not built from literals.
func ExpandPyFactory(src []byte, name string, args []string, callerNames map[string]string) (routes []Route, routerArg string, ok bool) {
	code := pyCodeLines(strings.Split(string(src), "\n"))
	info := pythonRoutersOf(code)
	var f *PyFactory
	for k := range info.Factories {
		if info.Factories[k].Name == name {
			f = &info.Factories[k]
		}
	}
	if f == nil {
		return nil, "", false
	}

	bound := map[string]string{}
	for k, v := range f.defaults {
		bound[k] = v
	}
	pos := 0
	for _, a := range args {
		key, val := "", a
		if k, v, isKw := pyKeywordArg(a); isKw {
			key, val = k, v
		} else {
			if strings.HasPrefix(strings.TrimSpace(a), "*") {
				break // *args: positions after it are not known
			}
			if pos < len(f.params) {
				key = f.params[pos]
			}
			pos++
		}
		if key == "" {
			continue
		}
		if key == f.Router && !f.Returns {
			routerArg = strings.TrimSpace(val)
			if id, isIdent := callerNames[pyIdentKey(routerArg)]; isIdent {
				routerArg = id
			}
			continue
		}
		if s, dyn, ok := pyStringExpr(val, callerNames); ok && !dyn {
			bound[key] = s
		} else {
			delete(bound, key) // passed, but not a literal: the default does not apply
		}
	}
	if !f.Returns && !pyDottedName.MatchString(routerArg) {
		return nil, "", false
	}

	names := pyMergeNames(info.Consts, bound)
	prefix := ""
	if f.Returns && f.Prefix != "" {
		p, dyn, ok := pyStringExpr(f.Prefix, names)
		if !ok || dyn {
			return nil, routerArg, false
		}
		prefix = p
	}
	for _, r := range pyScanRoutes(code, f.start, f.end, names, map[string]string{f.Router: prefix}, nil) {
		if r.Router != f.Router {
			continue
		}
		r.Runtime = r.Dynamic
		routes = append(routes, r)
	}
	return withPath(routes), routerArg, true
}
