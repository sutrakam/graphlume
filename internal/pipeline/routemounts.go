package pipeline

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/internal/analyze"
	"github.com/sutrakam/graphlume/internal/analyze/api"
	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// RouteMountStats is what the Python router-mount pass did.
type RouteMountStats struct {
	// Mounts is how many routers in one file are mounted from another.
	Mounts int
	// Renamed is how many endpoints changed name in this pass.
	Renamed int
	// Ambiguous counts routers mounted under more than one distinct prefix,
	// or whose module the imports name more than one file for. Their routes
	// keep the path their own file gives them rather than one of several.
	Ambiguous int
	// Derived is how many endpoints were added for routes served more than
	// once: a router factory's routes at each call site past the first, a
	// table loop's routes for each row of an imported table past the first.
	Derived int
}

// mountNode is a router variable in a file.
type mountNode struct{ file, variable string }

type mountEdge struct {
	parent mountNode
	// prefix is what the mount adds below the parent's own prefix chain: the
	// parent's in-file prefix, then the call's prefix=.
	prefix string
}

// MountPythonRouters gives each Python route the prefix of the router mount
// that serves it from another file.
//
// Extract reads one file at a time, so a route in users.py mounted by
// main.py's `app.include_router(users.router, prefix="/users")` was indexed as
// "GET /{id}". This pass sees the whole run, joins mounts to the routers they
// name through each mounting file's imports, and renames the endpoint
// "GET /users/{id}" so reports, client binding and runtime traces see the URL
// the service actually answers.
//
// The name is recomputed from the route's own path, kept in its FQN, every
// time: a mount removed or a prefix changed in an incremental run moves the
// name back, and running the pass twice changes nothing. Only the display
// name changes; the FQN, which is the symbol's identity, does not.
//
// A router mounted under two different prefixes is served at both. One symbol
// cannot hold two paths, so it is left as its file declares it and counted.
func MountPythonRouters(ctx context.Context, reg *registry.Registry, runID int64) (RouteMountStats, error) {
	var st RouteMountStats
	endpoints, err := reg.PythonEndpoints(ctx, runID)
	if err != nil || len(endpoints) == 0 {
		return st, err
	}
	mountFiles, err := reg.PythonMountFiles(ctx, runID)
	if err != nil {
		return st, err
	}
	paths, err := reg.FilePaths(ctx, runID)
	if err != nil {
		return st, err
	}
	pyFiles := make([]string, 0, len(paths))
	for _, p := range paths {
		if strings.HasSuffix(p, ".py") {
			pyFiles = append(pyFiles, p)
		}
	}
	sort.Strings(pyFiles)

	mounted := map[mountNode]string{}
	bad := map[mountNode]bool{}
	if len(mountFiles) > 0 {
		mounted, bad, st = pythonMountChains(mountFiles, pyFiles)
	}

	src := newPySources(ctx, reg, runID)
	for _, f := range mountFiles {
		src.put(f.Path, f.Content)
	}

	// Which router each file's routes are declared on, read only for files
	// that have a mounted router.
	receivers := map[string]map[int]string{}
	for n := range mounted {
		if _, done := receivers[n.file]; !done {
			receivers[n.file] = src.routers(n.file).Receivers
		}
	}
	mountedName := func(file string, line int, raw string) string {
		if recv, ok := receivers[file][line]; ok {
			if prefix, ok := mounted[mountNode{file, recv}]; ok {
				if method, p, ok := strings.Cut(raw, " "); ok {
					return method + " " + api.JoinRoutePath(prefix, p)
				}
			}
		}
		return raw
	}

	// Routes served once per factory call or per table row.
	exp, expSt, err := pythonRouteExpansions(ctx, reg, runID, endpoints, pyFiles, src, mounted, bad)
	if err != nil {
		return st, err
	}
	st.Ambiguous += expSt.Ambiguous

	renames := map[int64]string{}
	docs := map[int64]string{}
	confs := map[int64]model.Confidence{}
	var derived []registry.DerivedEndpoint
	for _, e := range endpoints {
		if analyze.IsDerivedEndpointFQN(e.FQN) {
			continue
		}
		raw, ok := analyze.EndpointSignature(e.FQN, e.Path)
		if !ok {
			continue
		}
		want := mountedName(e.Path, e.Line, raw)
		resolvedDoc := e.Doc
		if list := exp[e.ID]; len(list) > 0 {
			want = list[0].sig
			if list[0].confidence != e.Confidence {
				confs[e.ID] = list[0].confidence
			}
			for _, x := range list[1:] {
				doc := ""
				if x.runtime {
					doc = api.RuntimeRouteDoc
				}
				derived = append(derived, registry.DerivedEndpoint{
					Path: x.file, Line: x.line, Name: x.sig,
					FQN:        analyze.DerivedEndpointFQN(x.file, x.sig, fmt.Sprintf("%s:%d", e.Path, e.Line)),
					Handler:    e.Handler,
					Doc:        doc,
					Confidence: x.confidence,
					Template:   e.ID,
				})
			}
			if e.Doc == api.RuntimeRouteDoc || e.Doc == api.RuntimeRouteResolvedDoc {
				resolvedDoc = api.RuntimeRouteResolvedDoc
				if list[0].runtime {
					resolvedDoc = api.RuntimeRouteDoc
				}
			}
		} else if e.Doc == api.RuntimeRouteResolvedDoc {
			resolvedDoc = api.RuntimeRouteDoc // its table or call is gone
		}
		if resolvedDoc != e.Doc {
			docs[e.ID] = resolvedDoc
		}
		if want != e.Name {
			renames[e.ID] = want
		}
	}
	st.Renamed = len(renames)
	if err := reg.RenameEndpoints(ctx, renames); err != nil {
		return st, err
	}
	if err := reg.SetEndpointDocs(ctx, docs); err != nil {
		return st, err
	}
	if err := reg.SetEndpointConfidence(ctx, confs); err != nil {
		return st, err
	}
	n, err := reg.ReplaceDerivedEndpoints(ctx, runID, analyze.DerivedEndpointMarker, derived)
	st.Derived = n
	return st, err
}

// pythonMountPrefixes resolves every cross-file mount and returns, for each
// mounted router, the prefix its routes gain.
func pythonMountPrefixes(mountFiles []registry.FileText, pyFiles []string) (map[mountNode]string, RouteMountStats) {
	out, _, st := pythonMountChains(mountFiles, pyFiles)
	return out, st
}

// pythonMountChains is pythonMountPrefixes with the routers whose prefix is
// ambiguous (mounted under two, or through a cycle) named as well.
func pythonMountChains(mountFiles []registry.FileText, pyFiles []string) (map[mountNode]string, map[mountNode]bool, RouteMountStats) {
	var st RouteMountStats
	incoming := map[mountNode][]mountEdge{}
	for _, f := range mountFiles {
		// A test builds its own app and mounts the router under it, often at
		// another prefix. That is not how the service serves it, and counting
		// it would make every tested router look mounted twice.
		if capability.IsTestPath(f.Path) {
			continue
		}
		info := api.PythonRouters([]byte(f.Content))
		for _, m := range info.Mounts {
			mod, variable, ok := info.ResolveMountTarget(m.Child)
			if !ok {
				continue
			}
			target, tie := resolvePyModule(f.Path, mod, pyFiles)
			if target == "" {
				if tie {
					st.Ambiguous++
				}
				continue // a module outside the repository: nothing to rename
			}
			if target == f.Path {
				continue // a router of the same file: Extract applied it
			}
			child := mountNode{target, variable}
			incoming[child] = append(incoming[child], mountEdge{
				parent: mountNode{f.Path, m.Parent},
				prefix: joinPrefix(info.Prefixes[m.Parent], m.Prefix),
			})
		}
	}

	return resolveMountChainsFull(incoming, st)
}

// anyRouter stands for every router of a file, when a mount names a module
// whose exported router the file does not say.
const anyRouter = "*"

// resolveMountChains composes each mounted router's prefix from the chain of
// mounts above it. A router reached under two different full prefixes, or
// through a cycle, is counted as ambiguous and left out.
func resolveMountChains(incoming map[mountNode][]mountEdge, st RouteMountStats) (map[mountNode]string, RouteMountStats) {
	out, _, st := resolveMountChainsFull(incoming, st)
	return out, st
}

func resolveMountChainsFull(incoming map[mountNode][]mountEdge, st RouteMountStats) (map[mountNode]string, map[mountNode]bool, RouteMountStats) {
	edgesOf := func(n mountNode) []mountEdge {
		edges := incoming[n]
		if n.variable != anyRouter {
			edges = append(edges[:len(edges):len(edges)], incoming[mountNode{n.file, anyRouter}]...)
		}
		return edges
	}
	memo := map[mountNode]string{}
	bad := map[mountNode]bool{}
	visiting := map[mountNode]bool{}
	var chain func(n mountNode) (string, bool)
	chain = func(n mountNode) (string, bool) {
		if p, ok := memo[n]; ok {
			return p, true
		}
		if bad[n] || visiting[n] {
			return "", false // ambiguous, or a cycle
		}
		edges := edgesOf(n)
		if len(edges) == 0 {
			return "", true
		}
		visiting[n] = true
		defer delete(visiting, n)
		got := ""
		for i, e := range edges {
			above, ok := chain(e.parent)
			if !ok {
				bad[n] = true
				return "", false
			}
			full := joinPrefix(above, e.prefix)
			if i > 0 && full != got {
				bad[n] = true
				return "", false
			}
			got = full
		}
		memo[n] = got
		return got, true
	}

	out := map[mountNode]string{}
	children := make([]mountNode, 0, len(incoming))
	for n := range incoming {
		children = append(children, n)
	}
	sort.Slice(children, func(i, j int) bool {
		if children[i].file != children[j].file {
			return children[i].file < children[j].file
		}
		return children[i].variable < children[j].variable
	})
	for _, n := range children {
		st.Mounts++
		p, ok := chain(n)
		if !ok {
			st.Ambiguous++
			continue
		}
		if p != "" {
			out[n] = p
		}
	}
	return out, bad, st
}

func joinPrefix(a, b string) string {
	switch {
	case a == "":
		return api.JoinRoutePath("", b)
	case b == "":
		return api.JoinRoutePath(a, "")
	default:
		return api.JoinRoutePath(a, b)
	}
}

// resolvePyModule finds the file a module path names, from the file that
// imports it. A relative module is resolved against the importer's package;
// an absolute one is matched by path suffix, because the source root
// (src/, backend/, the repository root) is not known, and when several files
// match, the one sharing the most directories with the importer wins. A tie
// is refused, and reported as a tie.
func resolvePyModule(importer, module string, pyFiles []string) (file string, tie bool) {
	dots := len(module) - len(strings.TrimLeft(module, "."))
	rest := strings.ReplaceAll(module[dots:], ".", "/")
	if dots > 0 {
		base := path.Dir(importer)
		for i := 1; i < dots; i++ {
			base = path.Dir(base)
		}
		for _, cand := range []string{path.Join(base, rest) + ".py", path.Join(base, rest, "__init__.py")} {
			cand = strings.TrimPrefix(cand, "./")
			if containsSorted(pyFiles, cand) {
				return cand, false
			}
		}
		return "", false
	}
	if rest == "" {
		return "", false
	}
	var matches []string
	for _, suffix := range []string{rest + ".py", rest + "/__init__.py"} {
		for _, f := range pyFiles {
			if f == suffix || strings.HasSuffix(f, "/"+suffix) {
				matches = append(matches, f)
			}
		}
	}
	switch len(matches) {
	case 0:
		return "", false
	case 1:
		return matches[0], false
	}
	best, bestScore, tie := "", -1, false
	for _, m := range matches {
		s := sharedDirs(importer, m)
		switch {
		case s > bestScore:
			best, bestScore, tie = m, s, false
		case s == bestScore:
			tie = true
		}
	}
	if tie {
		return "", true
	}
	return best, false
}

func containsSorted(sorted []string, s string) bool {
	i := sort.SearchStrings(sorted, s)
	return i < len(sorted) && sorted[i] == s
}

// sharedDirs counts the leading directories two paths have in common.
func sharedDirs(a, b string) int {
	as := strings.Split(path.Dir(a), "/")
	bs := strings.Split(path.Dir(b), "/")
	n := 0
	for n < len(as) && n < len(bs) && as[n] == bs[n] && as[n] != "." {
		n++
	}
	return n
}

// MountExpressRouters gives each Express route the prefix of the router mount
// that serves it from another file: `app.use('/api/users', auth, users)` with
// users required from ./routes/users. It works as MountPythonRouters does:
// mounts compose through routers mounted on mounted routers, a router served
// under two prefixes keeps its own path and is counted, mounts made by tests
// are ignored, and every name is recomputed from the route's own path so a
// removed mount moves the name back.
func MountExpressRouters(ctx context.Context, reg *registry.Registry, runID int64) (RouteMountStats, error) {
	var st RouteMountStats
	endpoints, err := reg.ExpressEndpoints(ctx, runID)
	if err != nil || len(endpoints) == 0 {
		return st, err
	}
	mountFiles, err := reg.ExpressMountFiles(ctx, runID)
	if err != nil {
		return st, err
	}

	parsed := map[string]api.JSRouters{}
	read := func(file string) api.JSRouters {
		if r, ok := parsed[file]; ok {
			return r
		}
		content, err := reg.FileContent(ctx, runID, file)
		r := api.JSRouters{}
		if err == nil {
			r = api.ExpressRouters([]byte(content))
		}
		parsed[file] = r
		return r
	}
	for _, mf := range mountFiles {
		parsed[mf.Path] = api.ExpressRouters([]byte(mf.Content))
	}

	mounted := map[mountNode]string{}
	if len(mountFiles) > 0 {
		paths, err := reg.FilePaths(ctx, runID)
		if err != nil {
			return st, err
		}
		jsFiles := make([]string, 0, len(paths))
		for _, p := range paths {
			if registry.IsJSPath(p) {
				jsFiles = append(jsFiles, p)
			}
		}
		sort.Strings(jsFiles)

		incoming := map[mountNode][]mountEdge{}
		for _, mf := range mountFiles {
			if capability.IsTestPath(mf.Path) {
				continue // a test's own app, not where the service serves the router
			}
			info := parsed[mf.Path]
			for _, m := range info.Mounts {
				mod, export, ok := info.ResolveMountTarget(m)
				if !ok {
					continue
				}
				target := api.ResolveJSModulePath(mf.Path, mod, jsFiles)
				if target == "" || target == mf.Path {
					continue
				}
				variable := read(target).RouterFor(export)
				if variable == "" {
					variable = anyRouter
				}
				child := mountNode{target, variable}
				incoming[child] = append(incoming[child], mountEdge{
					parent: mountNode{mf.Path, m.Parent},
					prefix: joinPrefix(info.Prefixes[m.Parent], m.Prefix),
				})
			}
		}
		mounted, st = resolveMountChains(incoming, st)
	}

	nest, nestAmbiguous, err := nestGlobalPrefixes(ctx, reg, runID, endpoints)
	if err != nil {
		return st, err
	}
	st.Ambiguous += nestAmbiguous

	renames := map[int64]string{}
	for _, e := range endpoints {
		raw, ok := strings.CutPrefix(e.FQN, e.Path+"#")
		if !ok {
			continue
		}
		want := raw
		if prefix, ok := expressPrefixFor(mounted, read, e); ok {
			if method, p, ok := strings.Cut(raw, " "); ok {
				want = method + " " + api.JoinRoutePath(prefix, p)
			}
		}
		if prefix, ok := nest[e.Path]; ok {
			if method, p, ok := strings.Cut(want, " "); ok {
				want = method + " " + api.JoinRoutePath(prefix, p)
			}
		}
		if want != e.Name {
			renames[e.ID] = want
		}
	}
	st.Renamed = len(renames)
	return st, reg.RenameEndpoints(ctx, renames)
}

// expressPrefixFor is the mount prefix of the router an endpoint's route is
// declared on, falling back to a mount of the whole file.
func expressPrefixFor(mounted map[mountNode]string, read func(string) api.JSRouters, e registry.EndpointRow) (string, bool) {
	if !mountedFile(mounted, e.Path) {
		return "", false
	}
	if recv := read(e.Path).Receivers[e.Line]; recv != "" {
		if p, ok := mounted[mountNode{e.Path, recv}]; ok {
			return p, true
		}
	}
	p, ok := mounted[mountNode{e.Path, anyRouter}]
	return p, ok
}

func mountedFile(mounted map[mountNode]string, file string) bool {
	for n := range mounted {
		if n.file == file {
			return true
		}
	}
	return false
}

// nestSetGlobalPrefix is app.setGlobalPrefix('api') or ('api', { ... }).
var nestSetGlobalPrefix = regexp.MustCompile("\\.setGlobalPrefix\\(\\s*['\"`]([^'\"`]*)['\"`]\\s*([,)])")

// nestGlobalPrefixes finds, for each NestJS controller file that declares
// endpoints, the global prefix its application sets with app.setGlobalPrefix.
//
// The call is in the application's bootstrap (main.ts) and applies to every
// controller of that application: the files under the directory of the
// nearest package.json above it, or above the bootstrap's own src directory
// when there is none. Composed in this pass and not in a second one, because
// this pass recomputes every JS/TS endpoint's name from its route: a pass of
// its own would undo this one's work and redo its own on every resolve.
//
// A call with options (`{ exclude: [...] }`) serves some routes without the
// prefix, which the options decide and this does not read; it is counted as
// ambiguous and applied to none. So is an application that sets two.
func nestGlobalPrefixes(ctx context.Context, reg *registry.Registry, runID int64, endpoints []registry.EndpointRow) (map[string]string, int, error) {
	boots, err := reg.JSFilesMatching(ctx, runID, `content : "setglobalprefix"`)
	if err != nil || len(boots) == 0 {
		return nil, 0, err
	}
	paths, err := reg.FilePaths(ctx, runID)
	if err != nil {
		return nil, 0, err
	}
	manifests := map[string]bool{}
	for _, p := range paths {
		if path.Base(p) == "package.json" {
			manifests[path.Dir(p)] = true
		}
	}
	appRoot := func(bootFile string) string {
		for d := path.Dir(bootFile); ; d = path.Dir(d) {
			if manifests[d] {
				return d
			}
			if d == "." || d == "/" || d == "" {
				break
			}
		}
		d := path.Dir(bootFile)
		if path.Base(d) == "src" {
			d = path.Dir(d)
		}
		return d
	}

	ambiguous := 0
	byRoot := map[string]string{}
	bad := map[string]bool{}
	for _, b := range boots {
		if capability.IsTestPath(b.Path) {
			continue
		}
		for _, m := range nestSetGlobalPrefix.FindAllStringSubmatch(b.Content, -1) {
			root := appRoot(b.Path)
			if m[2] == "," || (byRoot[root] != "" && byRoot[root] != m[1]) {
				bad[root] = true
				continue
			}
			byRoot[root] = m[1]
		}
	}
	for root := range bad {
		delete(byRoot, root)
		ambiguous++
	}
	if len(byRoot) == 0 {
		return nil, ambiguous, nil
	}

	out := map[string]string{}
	checked := map[string]bool{}
	for _, e := range endpoints {
		if checked[e.Path] {
			continue
		}
		checked[e.Path] = true
		root, prefix := "", ""
		for r, p := range byRoot {
			if (r == "." || strings.HasPrefix(e.Path, r+"/")) && len(r) >= len(root) {
				root, prefix = r, p
			}
		}
		if prefix == "" {
			continue
		}
		// Only controllers: an Express or Next route in the same package is
		// not under Nest's prefix.
		content, err := reg.FileContent(ctx, runID, e.Path)
		if err != nil || !strings.Contains(content, "@Controller") {
			continue
		}
		out[e.Path] = prefix
	}
	return out, ambiguous, nil
}
