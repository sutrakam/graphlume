package supplychain

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/internal/analyze/deps"
)

// ReachState is what the index shows about the use of a package.
type ReachState string

const (
	// ReachReferenced: a file that imports the package also references one of
	// the symbols the advisory names, by a qualified name the index recorded.
	ReachReferenced ReachState = "referenced"
	// ReachImported: the package is imported, and either the advisory names no
	// symbols or the index could not bind a reference to one. This is NOT
	// "the vulnerable code is unused" — it is "the index cannot tell".
	ReachImported ReachState = "imported_symbol_use_unknown"
	// ReachNotImported: no indexed file imports the package. Claimed only
	// where the package-to-import mapping is exact.
	ReachNotImported ReachState = "not_imported"
	// ReachUnknown: no import matched, in an ecosystem where the package name
	// does not determine the import path — so the absence of a match shows
	// nothing. Never presented as "not imported".
	ReachUnknown ReachState = "import_use_unknown"
)

// rank orders the states for gating: higher is closer to proven use. An
// undeterminable import ranks with an import, because a gate must not pass on
// what could not be checked.
func (s ReachState) rank() int {
	switch s {
	case ReachReferenced:
		return 3
	case ReachImported, ReachUnknown:
		return 2
	}
	return 1
}

// Index is what reachability reads from the code graph. It is declared here,
// by its consumer; internal/cli adapts the registry to it.
type Index interface {
	// ImportSites returns every import the index recorded.
	ImportSites(ctx context.Context) ([]ImportSite, error)
	// References returns recorded references whose name is one of names.
	References(ctx context.Context, names []string) ([]RefSite, error)
	// LineText returns the indexed text of one line of a file; ok is false
	// when the index holds no text for it.
	LineText(ctx context.Context, file string, line int) (text string, ok bool)
	// Files returns every indexed file with its language and parse status.
	Files(ctx context.Context) ([]FileInfo, error)
}

// ImportSite is one import statement.
type ImportSite struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Path     string `json:"import"`
	Alias    string `json:"alias,omitempty"`
	Language string `json:"-"`
	// Via is set when the site was not an import statement: "require" for a
	// CommonJS require call read from the indexed line text.
	Via string `json:"via,omitempty"`
}

// RefSite is one recorded reference.
type RefSite struct {
	File     string
	Line     int
	Name     string
	Receiver string
	Kind     string
}

// FileInfo is one indexed file.
type FileInfo struct {
	Path        string
	Language    string
	ParseStatus string
}

// Reachability is the per-finding answer.
type Reachability struct {
	State  ReachState `json:"state"`
	Detail string     `json:"detail"`
	// ImportMapping is "exact" when the package name determines the import
	// path, "heuristic" when a match was guessed from the name.
	ImportMapping      string       `json:"import_mapping"`
	ImportingFiles     []ImportSite `json:"importing_files"`
	ImportingFileCount int          `json:"importing_file_count"`
	// AdvisorySymbols are the symbols the advisory names, as it spells them.
	AdvisorySymbols []string `json:"advisory_symbols,omitempty"`
	// SymbolReferences are the references that justify ReachReferenced.
	SymbolReferences []SymbolReference `json:"symbol_references,omitempty"`
}

// SymbolReference is one reference to an advisory-named symbol.
type SymbolReference struct {
	Symbol   string `json:"symbol"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Evidence string `json:"evidence"`
}

// ---------------------------------------------------------------- languages

// ecosystemLanguages maps an ecosystem to the source languages whose imports
// can name its packages. An import in any other language is not evidence: a
// Python file importing "requests" says nothing about an npm package of the
// same name.
var ecosystemLanguages = map[deps.Ecosystem][]string{
	deps.EcosystemGo:     {"go"},
	deps.EcosystemNPM:    {"javascript", "typescript", "tsx", "vue", "svelte"},
	deps.EcosystemPyPI:   {"python"},
	deps.EcosystemCargo:  {"rust"},
	deps.EcosystemMaven:  {"java", "kotlin", "scala", "groovy"},
	deps.EcosystemGradle: {"java", "kotlin", "scala", "groovy"},
}

func languageIn(eco deps.Ecosystem, language string) bool {
	for _, l := range ecosystemLanguages[eco] {
		if l == language {
			return true
		}
	}
	return false
}

// importMapping reports how a component's name maps to import paths.
//
// exact means a file that uses the package MUST contain an import this tool
// can match, so no match means not imported. Everything else is a guess from
// the name, and no match means nothing.
func importMapping(c Component) (mapping string, why string) {
	switch c.Ecosystem {
	case deps.EcosystemGo:
		return "exact", ""
	case deps.EcosystemNPM:
		return "exact", ""
	case deps.EcosystemPyPI:
		if _, known := pypiModules[pypiNormalize(c.Name)]; known {
			return "exact", ""
		}
		return "heuristic", "a PyPI distribution is not named after the modules it installs " +
			"(PyYAML is imported as yaml); only the distribution name and common variants were tried"
	case deps.EcosystemCargo:
		return "heuristic", "Rust code can use a crate by path with no `use` declaration " +
			"(serde_json::to_string), and those uses are not recorded as imports"
	case deps.EcosystemMaven, deps.EcosystemGradle:
		return "heuristic", "a Maven coordinate does not name the Java packages inside the artifact " +
			"(com.google.guava:guava is imported as com.google.common); imports were matched on the groupId prefix"
	}
	return "heuristic", "no import rule exists for this ecosystem"
}

// pypiModules maps well-known distributions to the top-level modules they
// install, for the common cases where the two differ or where knowing them for
// certain lets "not imported" be said. Keys are PEP 503 normalised.
//
// The table only ever ADDS evidence. A distribution missing from it falls back
// to name heuristics and, on no match, to ReachUnknown.
var pypiModules = map[string][]string{
	"aiohttp": {"aiohttp"}, "ansible": {"ansible"}, "attrs": {"attr", "attrs"},
	"beautifulsoup4": {"bs4"}, "bleach": {"bleach"}, "celery": {"celery"},
	"certifi": {"certifi"}, "cryptography": {"cryptography"}, "django": {"django"},
	"djangorestframework": {"rest_framework"}, "fastapi": {"fastapi"}, "flask": {"flask"},
	"gunicorn": {"gunicorn"}, "httpx": {"httpx"}, "idna": {"idna"}, "jinja2": {"jinja2"},
	"lxml": {"lxml"}, "markdown": {"markdown"}, "markupsafe": {"markupsafe"},
	"numpy": {"numpy"}, "opencv-python": {"cv2"}, "pandas": {"pandas"}, "paramiko": {"paramiko"},
	"pillow": {"PIL"}, "protobuf": {"google.protobuf"}, "psycopg2": {"psycopg2"},
	"psycopg2-binary": {"psycopg2"}, "pycryptodome": {"Crypto"}, "pydantic": {"pydantic"},
	"pygments": {"pygments"}, "pyjwt": {"jwt"}, "pymysql": {"pymysql"}, "pyopenssl": {"OpenSSL"},
	"python-dateutil": {"dateutil"}, "python-dotenv": {"dotenv"}, "python-jose": {"jose"},
	"python-multipart": {"multipart"}, "pyyaml": {"yaml"}, "redis": {"redis"},
	"requests": {"requests"}, "scikit-learn": {"sklearn"}, "scipy": {"scipy"},
	"setuptools": {"setuptools", "pkg_resources"}, "sqlalchemy": {"sqlalchemy"},
	"starlette": {"starlette"}, "tornado": {"tornado"}, "twisted": {"twisted"},
	"urllib3": {"urllib3"}, "uvicorn": {"uvicorn"}, "werkzeug": {"werkzeug"},
}

// mavenPackages maps a few artifacts whose Java package is not under their
// groupId. Additive only, like pypiModules.
var mavenPackages = map[string][]string{
	"com.fasterxml.jackson.core:jackson-databind":    {"com.fasterxml.jackson.databind"},
	"com.fasterxml.jackson.core:jackson-annotations": {"com.fasterxml.jackson.annotation"},
	"com.google.guava:guava":                         {"com.google.common"},
	"com.google.code.gson:gson":                      {"com.google.gson"},
	"commons-io:commons-io":                          {"org.apache.commons.io"},
	"commons-collections:commons-collections":        {"org.apache.commons.collections"},
	"commons-fileupload:commons-fileupload":          {"org.apache.commons.fileupload"},
	"commons-beanutils:commons-beanutils":            {"org.apache.commons.beanutils"},
	"commons-codec:commons-codec":                    {"org.apache.commons.codec"},
	"org.apache.commons:commons-text":                {"org.apache.commons.text"},
	"org.apache.commons:commons-compress":            {"org.apache.commons.compress"},
	"org.yaml:snakeyaml":                             {"org.yaml.snakeyaml"},
	"com.thoughtworks.xstream:xstream":               {"com.thoughtworks.xstream"},
	"org.springframework:spring-web":                 {"org.springframework.web", "org.springframework.http"},
	"org.springframework:spring-core":                {"org.springframework.core", "org.springframework.util"},
}

// importName is the name source code uses for a component: the DECLARED name
// where a Go replace or a Cargo rename changed the package behind it, because
// neither changes what an import statement says.
func importName(c Component) string {
	if c.DeclaredName != "" {
		return c.DeclaredName
	}
	return c.Name
}

// ---------------------------------------------------------------- matching

// importIndex answers "which import sites use this component".
type importIndex struct {
	sites []ImportSite
	// goModules are the declared Go module paths, longest first, so an import
	// is attributed to the most specific module that contains it:
	// example.com/lib/v2/x belongs to example.com/lib/v2, not example.com/lib.
	goModules []string

	// The lookups behind undeclaredSites. An OSV dump is hundreds of
	// thousands of advisories, nearly all of which name nothing here, so
	// "does anything import this name" must not scan every import each time.
	goPaths      []string // distinct Go import paths, sorted
	goByPath     map[string][]ImportSite
	npmByPackage map[string][]ImportSite // keyed by lower-case package name
}

// npmPackageOf returns the package an npm import path belongs to — "lodash"
// for "lodash/fp", "@scope/pkg" for "@scope/pkg/sub" — or "" for a relative
// or absolute path, which names a file.
func npmPackageOf(importPath string) string {
	if importPath == "" || importPath[0] == '.' || importPath[0] == '/' {
		return ""
	}
	parts := strings.SplitN(importPath, "/", 3)
	if importPath[0] == '@' {
		if len(parts) < 2 {
			return ""
		}
		return strings.ToLower(parts[0] + "/" + parts[1])
	}
	return strings.ToLower(parts[0])
}

func newImportIndex(sites []ImportSite, components []Component) *importIndex {
	ix := &importIndex{sites: sites, goByPath: map[string][]ImportSite{}, npmByPackage: map[string][]ImportSite{}}
	for _, s := range sites {
		switch {
		case languageIn(deps.EcosystemGo, s.Language):
			if _, seen := ix.goByPath[s.Path]; !seen {
				ix.goPaths = append(ix.goPaths, s.Path)
			}
			ix.goByPath[s.Path] = append(ix.goByPath[s.Path], s)
		case languageIn(deps.EcosystemNPM, s.Language):
			if pkg := npmPackageOf(s.Path); pkg != "" {
				ix.npmByPackage[pkg] = append(ix.npmByPackage[pkg], s)
			}
		}
	}
	sort.Strings(ix.goPaths)
	seen := map[string]bool{}
	for _, c := range components {
		if c.Ecosystem == deps.EcosystemGo && !seen[importName(c)] {
			seen[importName(c)] = true
			ix.goModules = append(ix.goModules, importName(c))
		}
	}
	sort.Slice(ix.goModules, func(i, j int) bool {
		if len(ix.goModules[i]) != len(ix.goModules[j]) {
			return len(ix.goModules[i]) > len(ix.goModules[j])
		}
		return ix.goModules[i] < ix.goModules[j]
	})
	return ix
}

func (ix *importIndex) goModuleOf(importPath string) string {
	for _, m := range ix.goModules {
		if importPath == m || strings.HasPrefix(importPath, m+"/") {
			return m
		}
	}
	return ""
}

// nodeCoreModules are the module names Node.js resolves to itself.
var nodeCoreModules = map[string]bool{
	"assert": true, "async_hooks": true, "buffer": true, "child_process": true, "cluster": true,
	"console": true, "constants": true, "crypto": true, "dgram": true, "diagnostics_channel": true,
	"dns": true, "domain": true, "events": true, "fs": true, "http": true, "http2": true, "https": true,
	"inspector": true, "module": true, "net": true, "os": true, "path": true, "perf_hooks": true,
	"process": true, "punycode": true, "querystring": true, "readline": true, "repl": true,
	"stream": true, "string_decoder": true, "sys": true, "timers": true, "tls": true,
	"trace_events": true, "tty": true, "url": true, "util": true, "v8": true, "vm": true,
	"wasi": true, "worker_threads": true, "zlib": true,
}

// undeclaredSites returns the import sites of a package NO manifest declares.
//
// Only for the two ecosystems where a package name is its import path. For Go,
// an import that a declared module already accounts for is not counted again:
// golang.org/x/net/html belongs to golang.org/x/net when that is declared.
func (ix *importIndex) undeclaredSites(eco deps.Ecosystem, name string) []ImportSite {
	if eco != deps.EcosystemGo && eco != deps.EcosystemNPM {
		return nil
	}
	// `import "url"` with no such dependency declared is Node's own module:
	// Node resolves a core module name before it looks in node_modules. It is
	// not evidence about the npm package that happens to share the name.
	if eco == deps.EcosystemNPM && nodeCoreModules[strings.TrimPrefix(name, "node:")] {
		return nil
	}
	var out []ImportSite
	if eco == deps.EcosystemNPM {
		out = append(out, ix.npmByPackage[strings.ToLower(name)]...)
	} else {
		add := func(path string) {
			if len(ix.goModuleOf(path)) <= len(name) {
				out = append(out, ix.goByPath[path]...)
			}
		}
		if _, ok := ix.goByPath[name]; ok {
			add(name)
		}
		prefix := name + "/"
		for i := sort.SearchStrings(ix.goPaths, prefix); i < len(ix.goPaths) && strings.HasPrefix(ix.goPaths[i], prefix); i++ {
			add(ix.goPaths[i])
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// sitesFor returns the import sites that use a component, in a stable order.
func (ix *importIndex) sitesFor(c Component) []ImportSite {
	name := importName(c)
	var out []ImportSite
	for _, s := range ix.sites {
		if !languageIn(c.Ecosystem, s.Language) {
			continue
		}
		if ix.matches(c, name, s.Path) {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

func (ix *importIndex) matches(c Component, name, importPath string) bool {
	switch c.Ecosystem {
	case deps.EcosystemGo:
		return ix.goModuleOf(importPath) == name
	case deps.EcosystemPyPI:
		for _, module := range pypiCandidates(name) {
			if hasDottedPrefix(importPath, module) {
				return true
			}
		}
		return false
	case deps.EcosystemMaven, deps.EcosystemGradle:
		for _, pkg := range mavenPackages[name] {
			if hasDottedPrefix(importPath, pkg) {
				return true
			}
		}
	}
	return deps.Matches(deps.Dependency{Name: name, Ecosystem: c.Ecosystem}, importPath)
}

func hasDottedPrefix(path, prefix string) bool {
	path, prefix = strings.ToLower(path), strings.ToLower(prefix)
	return path == prefix || strings.HasPrefix(path, prefix+".")
}

// pypiCandidates lists the top-level modules a distribution might install.
func pypiCandidates(dist string) []string {
	norm := pypiNormalize(dist)
	if known, ok := pypiModules[norm]; ok {
		return known
	}
	under := strings.ReplaceAll(norm, "-", "_")
	out := []string{under, strings.ReplaceAll(norm, "-", ".")}
	for _, affix := range []string{"python_", "py_"} {
		if strings.HasPrefix(under, affix) {
			out = append(out, strings.TrimPrefix(under, affix))
		}
	}
	if strings.HasSuffix(under, "_python") {
		out = append(out, strings.TrimSuffix(under, "_python"))
	}
	return out
}

// ---------------------------------------------------------------- require()

var requireCall = regexp.MustCompile("require\\(\\s*['\"`]([^'\"`]+)['\"`]\\s*\\)")

// requireSites turns CommonJS require calls into import sites.
//
// The index records `import x from "y"` as an import and `require("y")` as a
// call named require whose argument it does not keep. Left there, every
// CommonJS dependency would read as "not imported" — a false negative across a
// whole module system. The call's line is in the index, so the argument is
// read back from the indexed text.
//
// unread counts require calls whose argument could not be recovered: a
// computed argument, a call split across lines, or a file with no indexed
// text. Those are reported, because they are imports this report cannot see.
func requireSites(ctx context.Context, idx Index, languages map[string]string) (sites []ImportSite, unread int, err error) {
	refs, err := idx.References(ctx, []string{"require"})
	if err != nil {
		return nil, 0, err
	}
	type lineKey struct {
		file string
		line int
	}
	done := map[lineKey]bool{}
	for _, ref := range refs {
		lang := languages[ref.File]
		if !languageIn(deps.EcosystemNPM, lang) || ref.Receiver != "" {
			continue
		}
		key := lineKey{ref.File, ref.Line}
		if done[key] {
			continue
		}
		done[key] = true
		text, ok := idx.LineText(ctx, ref.File, ref.Line)
		matches := requireCall.FindAllStringSubmatch(text, -1)
		if !ok || len(matches) == 0 {
			unread++
			continue
		}
		for _, m := range matches {
			sites = append(sites, ImportSite{
				File: ref.File, Line: ref.Line, Path: m[1], Language: lang, Via: "require",
			})
		}
	}
	return sites, unread, nil
}

// ---------------------------------------------------------------- symbols

// reacher computes reachability for findings against one index.
type reacher struct {
	ctx     context.Context
	idx     Index
	imports *importIndex
	// refs caches reference lookups by name.
	refs map[string][]RefSite
	// importable counts, per ecosystem, the indexed source files written in a
	// language that can import its packages AND whose imports the index
	// records. Where it is zero, "nothing imports this" is true of an empty
	// set, and is not said.
	importable map[deps.Ecosystem]int
}

// countImportable fills reacher.importable from the indexed files.
func countImportable(files []FileInfo, importLanguages []string) map[deps.Ecosystem]int {
	binds := map[string]bool{}
	for _, l := range importLanguages {
		binds[l] = true
	}
	out := map[deps.Ecosystem]int{}
	for _, f := range files {
		if !binds[f.Language] {
			continue
		}
		for eco := range ecosystemLanguages {
			if languageIn(eco, f.Language) {
				out[eco]++
			}
		}
	}
	return out
}

func (r *reacher) references(names []string) ([]RefSite, error) {
	var missing []string
	for _, n := range names {
		if _, ok := r.refs[n]; !ok {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		found, err := r.idx.References(r.ctx, missing)
		if err != nil {
			return nil, err
		}
		for _, n := range missing {
			r.refs[n] = nil
		}
		for _, ref := range found {
			r.refs[ref.Name] = append(r.refs[ref.Name], ref)
		}
	}
	var out []RefSite
	for _, n := range names {
		out = append(out, r.refs[n]...)
	}
	return out, nil
}

// reach decides the state for one component under one advisory entry.
func (r *reacher) reach(c Component, scope SymbolScope) (Reachability, error) {
	return r.reachSites(c, scope, r.imports.sitesFor(c))
}

// reachSites is reach with the import sites already found.
func (r *reacher) reachSites(c Component, scope SymbolScope, sites []ImportSite) (Reachability, error) {
	mapping, why := importMapping(c)
	out := Reachability{ImportMapping: mapping, ImportingFiles: []ImportSite{}}

	if len(sites) == 0 {
		switch {
		case r.importable[c.Ecosystem] == 0:
			out.State = ReachUnknown
			out.Detail = fmt.Sprintf("no import was found, and that shows nothing: the index holds no source file "+
				"in a language that can import it and whose imports are recorded (%s)",
				strings.Join(ecosystemLanguages[c.Ecosystem], ", "))
		case mapping != "exact":
			out.State = ReachUnknown
			out.Detail = "no import matched this package, and that shows nothing: " + why
		case c.Indirect:
			out.State = ReachNotImported
			out.Detail = "no indexed file imports it, as expected for a dependency the manifest marks indirect: " +
				"it is reached through other dependencies, and whether THEY call the vulnerable code is not visible here"
		default:
			out.State = ReachNotImported
			out.Detail = "no indexed file imports it. That covers first-party indexed code only: " +
				"another dependency may still import it, and a build or test tool is run, not imported"
		}
		return out, nil
	}

	// Go advisories can narrow the vulnerability to packages inside the
	// module. Importing a different package of the module is not importing
	// the vulnerable one.
	packages := scope.Packages
	if c.Ecosystem == deps.EcosystemGo {
		packages = append(packages, goFunctionPackages(scope.Functions)...)
	}
	if c.Ecosystem == deps.EcosystemGo && len(packages) > 0 {
		return r.reachGoPackages(sites, packages, out)
	}

	out.setFiles(sites)
	out.AdvisorySymbols = append(out.AdvisorySymbols, scope.Functions...)
	switch {
	case len(scope.Functions) == 0:
		out.State = ReachImported
		out.Detail = "imported; the advisory names no symbols, so which calls are vulnerable is not known"
		if mapping != "exact" {
			out.Detail += " (the import was matched by a name heuristic, not an exact mapping)"
		}
		return out, nil
	case c.Ecosystem == deps.EcosystemPyPI:
		return r.reachPython(sites, scope.Functions, out)
	case c.Ecosystem == deps.EcosystemCargo:
		return reachRust(sites, scope.Functions, out), nil
	}
	out.State = ReachImported
	out.Detail = fmt.Sprintf("imported; the advisory names %d symbol(s), but binding a reference to a "+
		"dependency's symbol is implemented for Go, Python and Rust `use` declarations only", len(scope.Functions))
	return out, nil
}

func (o *Reachability) setFiles(sites []ImportSite) {
	files := map[string]bool{}
	for _, s := range sites {
		files[s.File] = true
	}
	o.ImportingFileCount = len(files)
	// One site per file is enough to open the file at the import.
	seen := map[string]bool{}
	for _, s := range sites {
		if seen[s.File] {
			continue
		}
		seen[s.File] = true
		o.ImportingFiles = append(o.ImportingFiles, s)
	}
}

// goFunctionPackages reads GitHub-style affected functions for Go
// ("golang.org/x/net/html.Parse") as package-and-symbol pairs.
func goFunctionPackages(functions []string) []PackageSymbols {
	var out []PackageSymbols
	for _, fn := range functions {
		// The symbol starts at the first dot, after the last slash, that is
		// followed by an upper-case letter: an exported Go name. A dot inside
		// the path's last element ("gopkg.in/yaml.v3") is followed by a
		// lower-case one.
		dot := -1
		for i := strings.LastIndex(fn, "/") + 1; i+1 < len(fn); i++ {
			if fn[i] == '.' && fn[i+1] >= 'A' && fn[i+1] <= 'Z' {
				dot = i
				break
			}
		}
		if dot <= 0 {
			continue
		}
		out = append(out, PackageSymbols{Path: fn[:dot], Symbols: []string{fn[dot+1:]}})
	}
	return out
}

// goQualifiers lists the identifiers a Go file may use for an imported
// package. With an alias it is the alias. Without one it is the package's own
// name, which the import path only suggests: gopkg.in/yaml.v3 is package yaml,
// github.com/x/go-foo is usually package foo, and .../v2 is the element before.
// Every plausible spelling is tried; a miss never counts as evidence of absence.
var goMajorSuffix = regexp.MustCompile(`^v[0-9]+$`)

func goQualifiers(importPath, alias string) (names []string, dotImport bool) {
	switch alias {
	case ".":
		return nil, true
	case "_":
		return nil, false
	case "":
	default:
		return []string{alias}, false
	}
	segments := strings.Split(importPath, "/")
	last := segments[len(segments)-1]
	if len(segments) > 1 && goMajorSuffix.MatchString(last) {
		last = segments[len(segments)-2]
	}
	add := func(s string) {
		if s == "" {
			return
		}
		for _, have := range names {
			if have == s {
				return
			}
		}
		names = append(names, s)
	}
	add(last)
	if i := strings.Index(last, "."); i > 0 {
		add(last[:i])
	}
	trimmed := strings.TrimSuffix(strings.TrimPrefix(last, "go-"), "-go")
	add(trimmed)
	add(strings.ReplaceAll(trimmed, "-", "_"))
	add(strings.ReplaceAll(trimmed, "-", ""))
	return names, false
}

func (r *reacher) reachGoPackages(moduleSites []ImportSite, packages []PackageSymbols, out Reachability) (Reachability, error) {
	var (
		pkgSites   []ImportSite
		symbolsOf  = map[string][]string{} // import path -> function symbols
		methods    []string
		wholePkg   []string
		lookup     []string
		pkgListing []string
	)
	for _, p := range packages {
		pkgListing = append(pkgListing, p.Path)
		if len(p.Symbols) == 0 {
			wholePkg = append(wholePkg, p.Path)
		}
		for _, s := range p.Symbols {
			out.AdvisorySymbols = append(out.AdvisorySymbols, p.Path+"."+s)
			if strings.Contains(s, ".") {
				methods = append(methods, s)
				continue
			}
			symbolsOf[p.Path] = append(symbolsOf[p.Path], s)
			lookup = append(lookup, s)
		}
		for _, site := range moduleSites {
			if site.Path == p.Path {
				pkgSites = append(pkgSites, site)
			}
		}
	}

	if len(pkgSites) == 0 {
		moduleFiles := map[string]bool{}
		for _, s := range moduleSites {
			moduleFiles[s.File] = true
		}
		out.State = ReachNotImported
		out.Detail = fmt.Sprintf("the advisory limits the vulnerability to %s, which no indexed file imports "+
			"(%d file(s) import other packages of the module). First-party indexed code only: another "+
			"dependency may still import it", strings.Join(pkgListing, ", "), len(moduleFiles))
		return out, nil
	}
	out.setFiles(pkgSites)

	if len(lookup) > 0 {
		refs, err := r.references(lookup)
		if err != nil {
			return out, err
		}
		for _, site := range pkgSites {
			qualifiers, dot := goQualifiers(site.Path, site.Alias)
			for _, ref := range refs {
				if ref.File != site.File || !containsString(symbolsOf[site.Path], ref.Name) {
					continue
				}
				// Bound only by its qualifier: pkg.Func, or a bare Func in a
				// file that dot-imports the package.
				bound := containsString(qualifiers, ref.Receiver) || (dot && ref.Receiver == "")
				if !bound {
					continue
				}
				evidence := fmt.Sprintf("%s.%s in a file that imports %s", ref.Receiver, ref.Name, site.Path)
				if dot {
					evidence = fmt.Sprintf("%s in a file that dot-imports %s", ref.Name, site.Path)
				}
				out.SymbolReferences = append(out.SymbolReferences, SymbolReference{
					Symbol: site.Path + "." + ref.Name, File: ref.File, Line: ref.Line, Evidence: evidence,
				})
			}
		}
	}
	sortSymbolReferences(out.SymbolReferences)

	switch {
	case len(out.SymbolReferences) > 0:
		out.State = ReachReferenced
		out.Detail = fmt.Sprintf("%d qualified reference(s) to a symbol the advisory names", len(out.SymbolReferences))
		if len(methods) > 0 {
			out.Detail += fmt.Sprintf("; %d named method(s) were not checked (%s)", len(methods), strings.Join(methods, ", "))
		}
	case len(wholePkg) > 0:
		out.State = ReachImported
		out.Detail = fmt.Sprintf("imported; the advisory marks every symbol of %s as affected and names none, "+
			"so there is no specific symbol to look for", strings.Join(wholePkg, ", "))
	default:
		out.State = ReachImported
		out.Detail = "the vulnerable package is imported, and no qualified call to a named symbol was found in the " +
			"importing files. That is not proof of absence: only pkg.Func calls are bound"
		if len(methods) > 0 {
			out.Detail += fmt.Sprintf("; %d named symbol(s) are methods (%s), which cannot be bound without type "+
				"information", len(methods), strings.Join(methods, ", "))
		}
		out.Detail += ". govulncheck answers this exactly for Go"
	}
	return out, nil
}

// reachPython binds dotted affected functions ("jinja2.utils.urlize") to
// references in files that import the function's module.
//
// Two shapes are bound, both of which the index records:
//
//	from jinja2.utils import urlize ... urlize(x)     import == module, bare name
//	import yaml ... yaml.load(x)                      import == module, receiver is the module or its alias
//	from jinja2 import utils ... utils.urlize(x)      import == parent, receiver is the module's last name
//
// A method on a class ("pkg.mod.Class.method") is not bound: the receiver is a
// variable whose type the index does not know.
func (r *reacher) reachPython(sites []ImportSite, functions []string, out Reachability) (Reachability, error) {
	type target struct{ module, name, full string }
	var targets []target
	var names []string
	for _, fn := range functions {
		i := strings.LastIndex(fn, ".")
		if i <= 0 || i == len(fn)-1 {
			continue
		}
		targets = append(targets, target{module: fn[:i], name: fn[i+1:], full: fn})
		names = append(names, fn[i+1:])
	}
	refs, err := r.references(names)
	if err != nil {
		return out, err
	}
	for _, site := range sites {
		for _, t := range targets {
			moduleLast := t.module[strings.LastIndex(t.module, ".")+1:]
			parent := ""
			if i := strings.LastIndex(t.module, "."); i > 0 {
				parent = t.module[:i]
			}
			for _, ref := range refs {
				if ref.File != site.File || ref.Name != t.name {
					continue
				}
				var evidence string
				switch {
				case site.Path == t.module && ref.Receiver == "":
					evidence = fmt.Sprintf("%s used in a file that imports %s", t.name, t.module)
				case site.Path == t.module && (ref.Receiver == site.Alias || ref.Receiver == moduleLast) && ref.Receiver != "":
					evidence = fmt.Sprintf("%s.%s in a file that imports %s", ref.Receiver, t.name, t.module)
				case parent != "" && site.Path == parent && ref.Receiver == moduleLast:
					evidence = fmt.Sprintf("%s.%s in a file that imports %s", ref.Receiver, t.name, parent)
				default:
					continue
				}
				out.SymbolReferences = append(out.SymbolReferences, SymbolReference{
					Symbol: t.full, File: ref.File, Line: ref.Line, Evidence: evidence,
				})
			}
		}
	}
	sortSymbolReferences(out.SymbolReferences)
	if len(out.SymbolReferences) > 0 {
		out.State = ReachReferenced
		out.Detail = fmt.Sprintf("%d reference(s) to a function the advisory names", len(out.SymbolReferences))
		return out, nil
	}
	out.State = ReachImported
	out.Detail = "imported, and no reference to a named function was bound in the importing files. " +
		"That is not proof of absence: only module-level functions called by name or as module.function " +
		"are bound, never a method on an object"
	return out, nil
}

// reachRust binds one shape only: a `use` declaration naming the affected
// function itself. A method on a type ("smallvec::SmallVec::insert_many") is
// called on a value whose type the index does not know, so it is not bound.
func reachRust(sites []ImportSite, functions []string, out Reachability) Reachability {
	for _, site := range sites {
		for _, fn := range functions {
			if site.Path == fn {
				out.SymbolReferences = append(out.SymbolReferences, SymbolReference{
					Symbol: fn, File: site.File, Line: site.Line, Evidence: "`use " + fn + "`",
				})
			}
		}
	}
	sortSymbolReferences(out.SymbolReferences)
	if len(out.SymbolReferences) > 0 {
		out.State = ReachReferenced
		out.Detail = fmt.Sprintf("%d `use` declaration(s) of a function the advisory names", len(out.SymbolReferences))
		return out
	}
	out.State = ReachImported
	out.Detail = "imported, and no `use` of a named function was found. That is not proof of absence: " +
		"a method on a type, or a function called by its full path, is not bound"
	return out
}

func sortSymbolReferences(refs []SymbolReference) {
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].File != refs[j].File {
			return refs[i].File < refs[j].File
		}
		if refs[i].Line != refs[j].Line {
			return refs[i].Line < refs[j].Line
		}
		return refs[i].Symbol < refs[j].Symbol
	})
}
