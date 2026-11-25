package indexer

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// This file reads the layout of a JavaScript or TypeScript workspace
// (pnpm, yarn or npm) so that the stand-in tsconfig can resolve imports
// between sibling packages without an install.
//
// An installed workspace links every member into node_modules, which is how
// `import { x } from "@acme/ui"` finds packages/ui. A checkout that was never
// installed has no links, so the compiler cannot resolve the import and the
// reference stays a guess. The map built here gives the compiler the same
// answer through compilerOptions.paths instead: each member's name points at
// its source entry and its folder.
//
// Nothing here runs or downloads anything. The files read are the workspace
// declaration (pnpm-workspace.yaml, package.json "workspaces"), each member's
// package.json and, to merge the project's own aliases, its tsconfig chain.
// What they say can only ADD path entries to a tsconfig GraphLume writes in
// its own temp directory: no value reaches an argument, an environment
// variable or a command, and every target must exist inside the repository.

// maxWorkspaceFile bounds a manifest read.
const maxWorkspaceFile = 4 << 20

// maxWorkspaceDirs bounds the directories one workspace expansion may visit,
// so a pathological pattern such as "**" over a huge tree stays cheap.
const maxWorkspaceDirs = 20000

// maxWorkspaceDepth bounds how far a "**" segment descends.
const maxWorkspaceDepth = 6

// workspacePackage is one member of a workspace.
type workspacePackage struct {
	// Name is package.json's "name".
	Name string
	// Dir is the member's absolute directory.
	Dir string
	// Entry is the absolute source file "import \"name\"" resolves to, or ""
	// when none could be found.
	Entry string
	// Subpaths maps an import specifier suffix ("/button", "/*") to the
	// absolute source file or pattern it resolves to, from "exports".
	Subpaths map[string]string
}

// readLimited reads a regular file of at most max bytes; a symbolic link or a
// larger file reads as absent.
func readLimited(p string, max int64) ([]byte, bool) {
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > max {
		return nil, false
	}
	f, err := os.Open(p) //nolint:gosec // G304: a manifest inside the repository being indexed, named by a fixed file name
	if err != nil {
		return nil, false
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, max))
	if err != nil {
		return nil, false
	}
	return b, true
}

// workspacePatterns returns the member patterns a directory declares: the
// "packages" of pnpm-workspace.yaml and package.json's "workspaces" (an array,
// or an object with a "packages" array). ok is false when the directory
// declares no workspace.
func workspacePatterns(dir string) (patterns []string, ok bool) {
	if b, found := readLimited(filepath.Join(dir, "pnpm-workspace.yaml"), maxWorkspaceFile); found {
		var y struct {
			Packages []string `yaml:"packages"`
		}
		if yamlUnmarshal(b, &y) == nil {
			patterns = append(patterns, y.Packages...)
			ok = true
		}
	}
	if b, found := readLimited(filepath.Join(dir, "package.json"), maxWorkspaceFile); found {
		var j struct {
			Workspaces json.RawMessage `json:"workspaces"`
		}
		if json.Unmarshal(b, &j) == nil && len(j.Workspaces) > 0 {
			var arr []string
			var obj struct {
				Packages []string `json:"packages"`
			}
			if json.Unmarshal(j.Workspaces, &arr) == nil {
				patterns = append(patterns, arr...)
				ok = ok || len(arr) > 0
			} else if json.Unmarshal(j.Workspaces, &obj) == nil {
				patterns = append(patterns, obj.Packages...)
				ok = ok || len(obj.Packages) > 0
			}
		}
	}
	return patterns, ok
}

// yamlUnmarshal decodes YAML without letting a decoder panic (yaml.v3 has
// panicked on hostile input) take the indexer down.
func yamlUnmarshal(b []byte, v any) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = io.ErrUnexpectedEOF
		}
	}()
	return yaml.Unmarshal(b, v)
}

// findWorkspaceRoot returns the nearest directory from projectDir up to
// repoRoot (both included) that declares a workspace, with its patterns.
func findWorkspaceRoot(repoRoot, projectDir string) (root string, patterns []string, ok bool) {
	dir := filepath.Clean(projectDir)
	top := filepath.Clean(repoRoot)
	for {
		if p, found := workspacePatterns(dir); found {
			return dir, p, true
		}
		if dir == top {
			return "", nil, false
		}
		parent := filepath.Dir(dir)
		if parent == dir || !isWithin(top, parent) {
			return "", nil, false
		}
		dir = parent
	}
}

// isWithin reports whether p is base or lies beneath it, lexically.
func isWithin(base, p string) bool {
	rel, err := filepath.Rel(base, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// expandWorkspaceDirs lists the directories, relative to root and slash
// separated, that the patterns select. A pattern beginning with "!" removes
// what it matches. Symbolic links, dot-directories and node_modules are never
// entered.
func expandWorkspaceDirs(root string, patterns []string) []string {
	visited := 0
	selected := map[string]bool{}
	var include, exclude []string
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		switch {
		case p == "":
		case strings.HasPrefix(p, "!"):
			exclude = append(exclude, strings.TrimPrefix(p, "!"))
		default:
			include = append(include, p)
		}
	}
	expand := func(pattern string) []string {
		pattern = strings.TrimPrefix(path.Clean(strings.TrimPrefix(strings.ReplaceAll(pattern, "\\", "/"), "./")), "/")
		if pattern == "." || pattern == "" || strings.HasPrefix(pattern, "../") {
			return nil
		}
		var out []string
		var rec func(rel string, segs []string, depth int)
		rec = func(rel string, segs []string, depth int) {
			if visited > maxWorkspaceDirs {
				return
			}
			if len(segs) == 0 {
				out = append(out, rel)
				return
			}
			seg := segs[0]
			if seg == "**" {
				rec(rel, segs[1:], depth)
				if depth >= maxWorkspaceDepth {
					return
				}
				for _, name := range subdirs(root, rel, &visited) {
					rec(path.Join(rel, name), segs, depth+1)
				}
				return
			}
			if !strings.ContainsAny(seg, "*?[") {
				child := path.Join(rel, seg)
				if isRealDir(filepath.Join(root, filepath.FromSlash(child))) && seg != "node_modules" {
					rec(child, segs[1:], depth)
				}
				return
			}
			for _, name := range subdirs(root, rel, &visited) {
				if m, err := path.Match(seg, name); err == nil && m {
					rec(path.Join(rel, name), segs[1:], depth)
				}
			}
		}
		rec("", strings.Split(pattern, "/"), 0)
		return out
	}
	for _, p := range include {
		for _, d := range expand(p) {
			selected[d] = true
		}
	}
	for _, p := range exclude {
		for _, d := range expand(p) {
			delete(selected, d)
		}
	}
	out := make([]string, 0, len(selected))
	for d := range selected {
		if d != "" {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// subdirs lists the real subdirectories of root/rel, skipping dot-directories
// and node_modules.
func subdirs(root, rel string, visited *int) []string {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 || name == "node_modules" || strings.HasPrefix(name, ".") {
			continue
		}
		*visited++
		out = append(out, name)
	}
	return out
}

func isRealDir(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.IsDir()
}

func isRegularFile(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode().IsRegular()
}

// tsSourceSuffixes are the extensions a workspace entry may be written in.
var tsSourceSuffixes = []string{".ts", ".tsx", ".mts", ".cts"}

// builtDirs are directory names a package publishes build output into. A
// checkout has no build, so an entry under one of them is looked for in the
// source tree instead.
var builtDirs = map[string]bool{"dist": true, "build": true, "lib": true, "out": true, "esm": true, "cjs": true, "types": true, "es": true, "umd": true}

// sourceForEntry maps what a package.json says a consumer imports (usually
// built output) to a source file that exists in the checkout, preferring
// TypeScript over built JavaScript. It returns "" when nothing exists. rel is
// slash separated and relative to pkgDir.
func sourceForEntry(pkgDir, rel string) string {
	rel = strings.TrimPrefix(path.Clean(strings.ReplaceAll(rel, "\\", "/")), "./")
	if rel == "." || rel == "" || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
		return ""
	}
	// A directory entry resolves to its index file.
	if isRealDir(filepath.Join(pkgDir, filepath.FromSlash(rel))) {
		for _, name := range []string{"index.ts", "index.tsx", "index.js", "index.jsx"} {
			if c := path.Join(rel, name); isRegularFile(filepath.Join(pkgDir, filepath.FromSlash(c))) {
				return filepath.Join(pkgDir, filepath.FromSlash(c))
			}
		}
		return ""
	}
	stem := rel
	for _, suf := range []string{".d.mts", ".d.cts", ".d.ts", ".mjs", ".cjs", ".jsx", ".js", ".mts", ".cts", ".tsx", ".ts"} {
		if strings.HasSuffix(stem, suf) {
			stem = strings.TrimSuffix(stem, suf)
			break
		}
	}
	var stems []string
	stems = append(stems, stem)
	if first, rest, ok := strings.Cut(stem, "/"); ok && builtDirs[first] {
		stems = append(stems, "src/"+rest, rest)
	}
	var jsCandidates []string
	for _, s := range stems {
		for _, suf := range tsSourceSuffixes {
			if c := s + suf; isRegularFile(filepath.Join(pkgDir, filepath.FromSlash(c))) {
				return filepath.Join(pkgDir, filepath.FromSlash(c))
			}
		}
		jsCandidates = append(jsCandidates, s+".js", s+".jsx")
		// An index file in a directory named like the entry.
		for _, suf := range []string{"/index.ts", "/index.tsx"} {
			if c := s + suf; isRegularFile(filepath.Join(pkgDir, filepath.FromSlash(c))) {
				return filepath.Join(pkgDir, filepath.FromSlash(c))
			}
		}
	}
	// Plain JavaScript: the entry itself when it exists, then its source
	// counterparts. The type checker reads it with allowJs.
	for _, c := range append([]string{rel}, jsCandidates...) {
		if isRegularFile(filepath.Join(pkgDir, filepath.FromSlash(c))) && !strings.HasSuffix(c, ".d.ts") {
			return filepath.Join(pkgDir, filepath.FromSlash(c))
		}
	}
	return ""
}

// conditionOrder is the preference among the conditions of an "exports"
// object: type declarations first, then the ESM entry, then the default.
var conditionOrder = []string{"types", "source", "import", "module", "default", "require", "node", "browser"}

// exportTargets flattens one "exports" value to the strings it can resolve to,
// best first.
func exportTargets(v any, depth int) []string {
	if depth > 6 {
		return nil
	}
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, e := range t {
			out = append(out, exportTargets(e, depth+1)...)
		}
		return out
	case map[string]any:
		var out []string
		for _, c := range conditionOrder {
			if e, ok := t[c]; ok {
				out = append(out, exportTargets(e, depth+1)...)
			}
		}
		return out
	}
	return nil
}

// readWorkspacePackage reads one member's package.json. ok is false when it
// has no name.
func readWorkspacePackage(repoRoot, dir string) (workspacePackage, bool) {
	b, found := readLimited(filepath.Join(dir, "package.json"), maxWorkspaceFile)
	if !found {
		return workspacePackage{}, false
	}
	var j struct {
		Name    string          `json:"name"`
		Main    string          `json:"main"`
		Module  string          `json:"module"`
		Types   string          `json:"types"`
		Typings string          `json:"typings"`
		Exports json.RawMessage `json:"exports"`
	}
	if json.Unmarshal(b, &j) != nil || !validPackageName(j.Name) {
		return workspacePackage{}, false
	}
	pkg := workspacePackage{Name: j.Name, Dir: dir, Subpaths: map[string]string{}}
	inside := func(p string) string {
		if p != "" && isWithin(repoRoot, p) {
			return p
		}
		return ""
	}

	var entryCandidates []string
	if len(j.Exports) > 0 {
		var ex any
		if json.Unmarshal(j.Exports, &ex) == nil {
			subpaths := map[string]any{}
			if m, isMap := ex.(map[string]any); isMap {
				hasDot := false
				for k := range m {
					if strings.HasPrefix(k, ".") {
						hasDot = true
					}
				}
				if hasDot {
					for k, v := range m {
						if k == "." {
							entryCandidates = append(entryCandidates, exportTargets(v, 0)...)
						} else if strings.HasPrefix(k, "./") {
							subpaths[k] = v
						}
					}
				} else {
					// Conditions at the top level: they describe ".".
					entryCandidates = append(entryCandidates, exportTargets(m, 0)...)
				}
			} else {
				entryCandidates = append(entryCandidates, exportTargets(ex, 0)...)
			}
			keys := make([]string, 0, len(subpaths))
			for k := range subpaths {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				src := resolveSubpath(dir, k, exportTargets(subpaths[k], 0))
				if src != "" && inside(strings.Split(src, "*")[0]) != "" {
					pkg.Subpaths[strings.TrimPrefix(k, ".")] = src
				}
			}
		}
	}
	entryCandidates = append(entryCandidates, j.Types, j.Typings, j.Module, j.Main)
	for _, c := range entryCandidates {
		if c == "" || strings.Contains(c, "*") {
			continue
		}
		if src := sourceForEntry(dir, c); src != "" {
			pkg.Entry = inside(src)
			if pkg.Entry != "" {
				break
			}
		}
	}
	if pkg.Entry == "" {
		for _, c := range []string{"src/index.ts", "src/index.tsx", "index.ts", "index.tsx", "src/index.js", "src/index.jsx", "index.js"} {
			if isRegularFile(filepath.Join(dir, filepath.FromSlash(c))) {
				pkg.Entry = filepath.Join(dir, filepath.FromSlash(c))
				break
			}
		}
	}
	return pkg, true
}

// resolveSubpath turns an exports subpath key and its targets into a source
// file, or into a pattern when the key contains "*".
func resolveSubpath(pkgDir, key string, targets []string) string {
	if strings.Contains(key, "*") {
		for _, t := range targets {
			if strings.Count(t, "*") != 1 {
				continue
			}
			// "./src/*.tsx" stays a pattern; a built target is looked for
			// in the source tree by its leading directory.
			rel := strings.TrimPrefix(path.Clean(strings.ReplaceAll(t, "\\", "/")), "./")
			if strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
				continue
			}
			if first, rest, ok := strings.Cut(rel, "/"); ok && builtDirs[first] && isRealDir(filepath.Join(pkgDir, "src")) {
				rel = "src/" + rest
			}
			rel = strings.NewReplacer(".d.ts", ".ts", ".d.mts", ".ts", ".mjs", ".ts", ".cjs", ".ts", ".js", ".ts").Replace(rel)
			dirPart := path.Dir(strings.Split(rel, "*")[0] + "x")
			if dirPart == "." || isRealDir(filepath.Join(pkgDir, filepath.FromSlash(dirPart))) {
				return filepath.Join(pkgDir, filepath.FromSlash(rel))
			}
		}
		return ""
	}
	for _, t := range targets {
		if strings.Contains(t, "*") {
			continue
		}
		if src := sourceForEntry(pkgDir, t); src != "" {
			return src
		}
	}
	return ""
}

// validPackageName accepts the names npm accepts, in the plain forms only: it
// keeps a quote, a space or a "*" out of a path-mapping key.
func validPackageName(n string) bool {
	if n == "" || len(n) > 214 {
		return false
	}
	for _, r := range n {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '@', r == '/', r == '~':
		default:
			return false
		}
	}
	if strings.Count(n, "/") > 1 || (strings.Contains(n, "/") && !strings.HasPrefix(n, "@")) {
		return false
	}
	return !strings.HasPrefix(n, ".") && !strings.HasSuffix(n, "/")
}

// workspaceMembers lists the members of the workspace rooted at root.
func workspaceMembers(repoRoot, root string, patterns []string) []workspacePackage {
	var out []workspacePackage
	seen := map[string]bool{}
	for _, rel := range expandWorkspaceDirs(root, patterns) {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		if !isWithin(repoRoot, dir) {
			continue
		}
		pkg, ok := readWorkspacePackage(repoRoot, dir)
		if !ok || seen[pkg.Name] {
			continue
		}
		seen[pkg.Name] = true
		out = append(out, pkg)
	}
	return out
}

// workspacePaths builds compilerOptions.paths entries for the members: the
// package name to its source entry, each exported subpath to its source file,
// and name/* to the package folder (and its src folder).
func workspacePaths(members []workspacePackage) map[string][]string {
	paths := map[string][]string{}
	for _, m := range members {
		if m.Entry != "" {
			paths[m.Name] = []string{m.Entry}
		}
		keys := make([]string, 0, len(m.Subpaths))
		for k := range m.Subpaths {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			paths[m.Name+k] = []string{m.Subpaths[k]}
		}
		if _, taken := paths[m.Name+"/*"]; !taken {
			paths[m.Name+"/*"] = []string{
				filepath.Join(m.Dir, "*"), filepath.Join(m.Dir, "src", "*"),
			}
		}
	}
	return paths
}

// stripJSONC removes the comments and trailing commas a tsconfig may carry,
// leaving strict JSON. Text inside strings is untouched.
func stripJSONC(b []byte) []byte {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case c == '"':
			out = append(out, c)
			for i++; i < len(b); i++ {
				out = append(out, b[i])
				if b[i] == '\\' && i+1 < len(b) {
					i++
					out = append(out, b[i])
				} else if b[i] == '"' {
					break
				}
			}
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			out = append(out, '\n')
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && (b[i] != '*' || b[i+1] != '/') {
				i++
			}
			i++
		default:
			out = append(out, c)
		}
	}
	// Trailing commas: a comma whose next non-space byte closes a container.
	res := make([]byte, 0, len(out))
	inStr := false
	for i := 0; i < len(out); i++ {
		c := out[i]
		if inStr {
			res = append(res, c)
			if c == '\\' && i+1 < len(out) {
				i++
				res = append(res, out[i])
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			res = append(res, c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(out) && (out[j] == ' ' || out[j] == '\t' || out[j] == '\n' || out[j] == '\r') {
				j++
			}
			if j < len(out) && (out[j] == '}' || out[j] == ']') {
				continue
			}
		}
		res = append(res, c)
	}
	return res
}

// tsconfigPaths is what a project's tsconfig chain says about path mapping.
type tsconfigPaths struct {
	// BaseURL is the absolute baseUrl in effect, or "".
	BaseURL string
	// Paths maps each alias to its absolute targets.
	Paths map[string][]string
}

// maxExtendsDepth bounds an "extends" chain.
const maxExtendsDepth = 8

// readTsconfigPaths reads compilerOptions.baseUrl and paths from the
// project's tsconfig.json (or jsconfig.json), following "extends" through
// relative files and through workspace members (so "@acme/tsconfig/base.json"
// is read from the member that provides it). A file that is missing or does
// not parse contributes nothing. Targets come back absolute.
func readTsconfigPaths(repoRoot, projectDir string, members []workspacePackage) tsconfigPaths {
	res := tsconfigPaths{Paths: map[string][]string{}}
	var first string
	for _, name := range []string{"tsconfig.json", "jsconfig.json"} {
		if f := filepath.Join(projectDir, name); isRegularFile(f) {
			first = f
			break
		}
	}
	if first == "" {
		return res
	}
	visited := map[string]bool{}
	type layer struct {
		dir     string
		baseURL string
		paths   map[string][]string
	}
	// Parent first, so a child's values override.
	var chain []layer
	var load func(file string, depth int)
	load = func(file string, depth int) {
		if depth > maxExtendsDepth || visited[file] || !isWithin(repoRoot, file) {
			return
		}
		visited[file] = true
		raw, ok := readLimited(file, maxWorkspaceFile)
		if !ok {
			return
		}
		var cfg struct {
			Extends         json.RawMessage `json:"extends"`
			CompilerOptions struct {
				BaseURL *string             `json:"baseUrl"`
				Paths   map[string][]string `json:"paths"`
			} `json:"compilerOptions"`
		}
		if json.Unmarshal(stripJSONC(raw), &cfg) != nil {
			return
		}
		dir := filepath.Dir(file)
		var extends []string
		var one string
		var many []string
		if json.Unmarshal(cfg.Extends, &one) == nil && one != "" {
			extends = []string{one}
		} else if json.Unmarshal(cfg.Extends, &many) == nil {
			extends = many
		}
		for _, e := range extends {
			if target := resolveExtends(dir, e, members); target != "" {
				load(target, depth+1)
			}
		}
		l := layer{dir: dir, paths: cfg.CompilerOptions.Paths}
		if cfg.CompilerOptions.BaseURL != nil {
			b := filepath.Join(dir, filepath.FromSlash(*cfg.CompilerOptions.BaseURL))
			if isWithin(repoRoot, b) {
				l.baseURL = b
			}
		}
		chain = append(chain, l)
	}
	load(first, 0)
	for _, l := range chain {
		if l.baseURL != "" {
			res.BaseURL = l.baseURL
		}
	}
	for _, l := range chain {
		base := res.BaseURL
		if base == "" {
			base = l.dir
		}
		for alias, targets := range l.paths {
			var abs []string
			for _, t := range targets {
				p := filepath.Join(base, filepath.FromSlash(t))
				if isWithin(repoRoot, p) {
					abs = append(abs, p)
				}
			}
			if len(abs) > 0 {
				res.Paths[alias] = abs
			}
		}
	}
	return res
}

// resolveExtends finds the file an "extends" value names: a relative path, or
// a path inside a workspace member. A package that is not a member is not
// resolved (it would come from node_modules, which an uninstalled checkout
// does not have).
func resolveExtends(dir, spec string, members []workspacePackage) string {
	spec = strings.ReplaceAll(spec, "\\", "/")
	withJSON := func(p string) string {
		if isRegularFile(p) {
			return p
		}
		if !strings.HasSuffix(p, ".json") && isRegularFile(p+".json") {
			return p + ".json"
		}
		return ""
	}
	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") {
		return withJSON(filepath.Join(dir, filepath.FromSlash(spec)))
	}
	for _, m := range members {
		if spec == m.Name {
			return withJSON(filepath.Join(m.Dir, "tsconfig.json"))
		}
		if rest, ok := strings.CutPrefix(spec, m.Name+"/"); ok {
			return withJSON(filepath.Join(m.Dir, filepath.FromSlash(rest)))
		}
	}
	return ""
}
