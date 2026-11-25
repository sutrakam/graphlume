package pipeline

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/internal/analyze/api"
	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// Python routes served more than once from one declaration.
//
// A router factory's route is served once per call of the factory, at the
// prefix that call and its mount give it; a route registered in a loop over a
// table another module defines is served once per row. Extract reads one file
// and sees the declaration once. This pass follows each call (through the
// calling file's imports) and each table (through the looping file's
// imports), and gives every served path an endpoint: the declared one is
// named after the first, and one endpoint is derived for each other.
//
// Confidence is graded on what joined the files. Following an import to the
// one file that defines the factory or table, with literal arguments and an
// unambiguous mount chain, is high: the same evidence a decorator gives. A
// path that still holds a value the source does not state is medium. Exact is
// a compiler's claim and is never made here.

// pySources reads and parses Python files once per pass.
type pySources struct {
	ctx     context.Context
	reg     *registry.Registry
	runID   int64
	text    map[string]string
	parsed  map[string]api.PyRouters
	missing map[string]bool
}

func newPySources(ctx context.Context, reg *registry.Registry, runID int64) *pySources {
	return &pySources{ctx: ctx, reg: reg, runID: runID,
		text: map[string]string{}, parsed: map[string]api.PyRouters{}, missing: map[string]bool{}}
}

func (s *pySources) put(file, content string) { s.text[file] = content }

func (s *pySources) content(file string) (string, bool) {
	if t, ok := s.text[file]; ok {
		return t, true
	}
	if s.missing[file] {
		return "", false
	}
	t, err := s.reg.FileContent(s.ctx, s.runID, file)
	if err != nil {
		s.missing[file] = true
		return "", false
	}
	s.text[file] = t
	return t, true
}

func (s *pySources) routers(file string) api.PyRouters {
	if r, ok := s.parsed[file]; ok {
		return r
	}
	t, _ := s.content(file)
	r := api.PythonRouters([]byte(t))
	s.parsed[file] = r
	return r
}

// routeExpansion is one path a declared route is served at.
type routeExpansion struct {
	sig        string // "GET /full/path"
	file       string // where a derived endpoint is placed: the call site
	line       int
	confidence model.Confidence
	runtime    bool // the path still holds a value the source does not state
}

type expansionStats struct{ Ambiguous int }

// templateKey finds a declared endpoint from a route re-read with values
// bound: same file, same line, same method.
type templateKey struct {
	file   string
	line   int
	method string
}

// pythonRouteExpansions returns, for each declared endpoint served at paths
// other than its own, every path it is served at, first one first.
func pythonRouteExpansions(ctx context.Context, reg *registry.Registry, runID int64,
	endpoints []registry.EndpointRow, pyFiles []string, src *pySources,
	mounted map[mountNode]string, bad map[mountNode]bool,
) (map[int64][]routeExpansion, expansionStats, error) {
	var st expansionStats
	templates := map[templateKey][]int64{}
	endpointFiles := map[string]bool{}
	for _, e := range endpoints {
		if isDerivedFQN(e.FQN) || capability.IsTestPath(e.Path) {
			continue
		}
		method, _, _ := strings.Cut(e.Name, " ")
		templates[templateKey{e.Path, e.Line, method}] = append(templates[templateKey{e.Path, e.Line, method}], e.ID)
		endpointFiles[e.Path] = true
	}
	files := make([]string, 0, len(endpointFiles))
	for f := range endpointFiles {
		files = append(files, f)
	}
	sort.Strings(files)

	// chain is the prefix a router of a file is served under from other files.
	chain := func(n mountNode) (string, bool) {
		if bad[n] {
			return "", false
		}
		return mounted[n], true
	}

	out := map[int64][]routeExpansion{}
	add := func(file string, r api.Route, x routeExpansion) {
		ids := templates[templateKey{file, r.Line, r.Method}]
		if len(ids) != 1 {
			return // no declaration, or several on one line: nothing to name
		}
		out[ids[0]] = append(out[ids[0]], x)
	}

	// 1. Router factories, followed from each call.
	factories := map[string]map[string]api.PyFactory{} // file -> name -> factory
	var names []string
	for _, f := range files {
		for _, fac := range src.routers(f).Factories {
			if factories[f] == nil {
				factories[f] = map[string]api.PyFactory{}
			}
			factories[f][fac.Name] = fac
			names = append(names, fac.Name)
		}
	}
	if len(names) > 0 {
		callers, err := pyFilesNaming(ctx, reg, runID, names)
		if err != nil {
			return nil, st, err
		}
		for f := range factories {
			callers[f] = true // a factory called from its own file
		}
		callerFiles := make([]string, 0, len(callers))
		for f := range callers {
			callerFiles = append(callerFiles, f)
		}
		sort.Strings(callerFiles)
		for _, c := range callerFiles {
			if capability.IsTestPath(c) {
				continue // a test builds its own app around the factory
			}
			info := src.routers(c)
			for _, call := range info.FactoryCalls {
				file, name, ok := resolvePyCallee(c, call.Callee, info, pyFiles, src, factories)
				if !ok {
					continue
				}
				if call.Wrapper != "" {
					if _, _, isFactory := resolvePyCallee(c, call.Wrapper, info, pyFiles, src, factories); isFactory {
						continue // the wrapper is the factory: its own call is expanded
					}
				}
				fsrc, _ := src.content(file)
				routes, routerArg, ok := api.ExpandPyFactory([]byte(fsrc), name, call.Args, call.Names)
				if !ok {
					if call.Parent != "" || call.Bound != "" || routerArg != "" {
						st.Ambiguous++ // mounted, but at a prefix the call does not state
					}
					continue
				}
				var node mountNode
				var callPrefix string
				switch {
				case call.Parent != "":
					node, callPrefix = mountNode{c, call.Parent}, call.Prefix
				case call.Bound != "":
					node = mountNode{c, call.Bound}
				case routerArg != "" && !strings.Contains(routerArg, "."):
					node = mountNode{c, routerArg}
				default:
					continue // a router built and not mounted here, or filled through an attribute
				}
				above, ok := chain(node)
				if !ok {
					st.Ambiguous++
					continue
				}
				base := joinPrefix(joinPrefix(above, info.Prefixes[node.variable]), callPrefix)
				for _, r := range routes {
					conf := model.ConfidenceHigh
					if r.Dynamic || r.Confidence != model.ConfidenceHigh || call.Wrapper != "" {
						conf = model.ConfidenceMedium
					}
					add(file, r, routeExpansion{
						sig:  r.Method + " " + api.JoinRoutePath(base, r.Path),
						file: c, line: call.Line, confidence: conf, runtime: r.Runtime,
					})
				}
			}
		}
	}

	// 2. Loops over a table another module defines.
	for _, f := range files {
		info := src.routers(f)
		if len(info.TableLoops) == 0 {
			continue
		}
		extra := map[string]string{}
		for _, l := range info.TableLoops {
			if expr, ok := resolvePyTable(f, l.Table, info, pyFiles, src); ok {
				extra[l.Table] = expr
			}
		}
		if len(extra) == 0 {
			continue
		}
		fsrc, _ := src.content(f)
		for _, r := range api.ExtractPythonWithTables([]byte(fsrc), extra) {
			if r.Runtime {
				continue
			}
			ids := templates[templateKey{f, r.Line, r.Method}]
			if len(ids) != 1 || !isRuntimeTemplate(endpoints, ids[0]) {
				continue
			}
			prefix := ""
			if recv, ok := info.Receivers[r.Line]; ok {
				p, ok := chain(mountNode{f, recv})
				if !ok {
					st.Ambiguous++
					continue
				}
				prefix = p
			}
			add(f, r, routeExpansion{
				sig:  r.Method + " " + api.JoinRoutePath(prefix, r.Path),
				file: f, line: r.Line, confidence: r.Confidence,
			})
		}
	}

	// Each path once, in a stable order: by call site, then path.
	for id, list := range out {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].file != list[j].file {
				return list[i].file < list[j].file
			}
			if list[i].line != list[j].line {
				return list[i].line < list[j].line
			}
			return list[i].sig < list[j].sig
		})
		seen := map[string]bool{}
		kept := list[:0]
		for _, x := range list {
			if seen[x.sig] {
				continue
			}
			seen[x.sig] = true
			kept = append(kept, x)
		}
		out[id] = kept
	}
	return out, st, nil
}

func isDerivedFQN(fqn string) bool { return strings.Contains(fqn, " [via ") }

func isRuntimeTemplate(endpoints []registry.EndpointRow, id int64) bool {
	for _, e := range endpoints {
		if e.ID == id {
			return e.Doc == api.RuntimeRouteDoc || e.Doc == api.RuntimeRouteResolvedDoc
		}
	}
	return false
}

// pyFilesNaming returns the Python files whose text names any of the
// functions. The full-text tokenizer splits on '_', so a name is searched as
// the phrase of its parts.
func pyFilesNaming(ctx context.Context, reg *registry.Registry, runID int64, names []string) (map[string]bool, error) {
	sort.Strings(names)
	uniq := names[:0]
	for i, n := range names {
		if i == 0 || n != names[i-1] {
			uniq = append(uniq, n)
		}
	}
	out := map[string]bool{}
	const chunk = 40
	for start := 0; start < len(uniq); start += chunk {
		end := min(start+chunk, len(uniq))
		var terms []string
		for _, n := range uniq[start:end] {
			words := strings.Fields(strings.ToLower(nonWord.ReplaceAllString(n, " ")))
			if len(words) == 0 {
				continue
			}
			terms = append(terms, `content : "`+strings.Join(words, " ")+`"`)
		}
		if len(terms) == 0 {
			continue
		}
		files, err := reg.PythonFilesMatching(ctx, runID, strings.Join(terms, " OR "))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			out[f.Path] = true
		}
	}
	return out, nil
}

var nonWord = regexp.MustCompile(`[^A-Za-z0-9]+`)

// resolvePyCallee finds the file and name of the factory a call names: a
// factory of the calling file, or one its imports name, following re-exports
// (`from .approvals import build_router` in a package's __init__.py).
func resolvePyCallee(caller, callee string, info api.PyRouters, pyFiles []string,
	src *pySources, factories map[string]map[string]api.PyFactory,
) (string, string, bool) {
	if !strings.Contains(callee, ".") {
		if _, ok := factories[caller][callee]; ok {
			if _, imported := info.Imports[callee]; !imported {
				return caller, callee, true
			}
		}
	}
	mod, name, ok := info.ResolveMountTarget(callee)
	if !ok {
		return "", "", false
	}
	from := caller
	for hop := 0; hop < 4; hop++ {
		file, _ := resolvePyModule(from, mod, pyFiles)
		if file == "" {
			return "", "", false
		}
		if _, ok := factories[file][name]; ok {
			return file, name, true
		}
		next := src.routers(file)
		imp, ok := next.Imports[name]
		if !ok || imp.Name == "" {
			return "", "", false
		}
		from, mod, name = file, imp.Module, imp.Name
	}
	return "", "", false
}

// resolvePyTable finds the literal a looping file's table name stands for in
// the module that defines it.
func resolvePyTable(file, table string, info api.PyRouters, pyFiles []string, src *pySources) (string, bool) {
	mod, name, ok := info.ResolveMountTarget(table)
	if !ok {
		return "", false
	}
	from := file
	for hop := 0; hop < 4; hop++ {
		target, _ := resolvePyModule(from, mod, pyFiles)
		if target == "" || target == file {
			return "", false
		}
		text, ok := src.content(target)
		if !ok {
			return "", false
		}
		if expr, ok := api.PyLiteralTable([]byte(text), name); ok {
			return expr, true
		}
		imp, ok := src.routers(target).Imports[name]
		if !ok || imp.Name == "" {
			return "", false
		}
		from, mod, name = target, imp.Module, imp.Name
	}
	return "", false
}
