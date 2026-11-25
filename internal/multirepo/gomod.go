package multirepo

import (
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// goModule is one module a member defines: its path and the directory, inside
// that member's repository, holding its go.mod.
type goModule struct {
	// Path is the module path from the `module` directive.
	Path string
	// Dir is the module root relative to the repository root, "" for the root.
	Dir string
	// File is the go.mod's own path, for messages.
	File string
	// Requires maps a required module path to its version.
	Requires map[string]string
	// Replaces are the module's replace directives, in file order.
	Replaces []goReplace
}

// goReplace is one `replace old [v] => new [v]` directive.
type goReplace struct {
	Old, OldVersion string
	New, NewVersion string
}

// parseGoModText reads the directives stitching needs from go.mod text.
//
// A small reader of its own rather than a dependency on the toolchain's
// modfile package (which this module does not require) or on
// internal/resolve/gotypes (which would drag the type checker into every
// federated query). It reads module, require and replace in both the single
// line and the parenthesised forms; anything it cannot read is skipped, which
// degrades to "no version known", never to a wrong module path.
func parseGoModText(file, text string) goModule {
	m := goModule{File: file, Dir: goModDir(file), Requires: map[string]string{}}
	block := ""
	for _, raw := range strings.Split(text, "\n") {
		line := raw
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if block != "" {
			if line == ")" {
				block = ""
				continue
			}
			m.directive(block, goModFields(line))
			continue
		}
		f := goModFields(line)
		if len(f) >= 2 && f[1] == "(" {
			block = f[0]
			continue
		}
		if len(f) > 0 {
			m.directive(f[0], f[1:])
		}
	}
	return m
}

func (m *goModule) directive(verb string, args []string) {
	switch verb {
	case "module":
		if len(args) > 0 {
			m.Path = args[0]
		}
	case "require":
		if len(args) >= 2 {
			m.Requires[args[0]] = args[1]
		}
	case "replace":
		arrow := -1
		for i, a := range args {
			if a == "=>" {
				arrow = i
				break
			}
		}
		if arrow < 1 || arrow >= len(args)-1 {
			return
		}
		r := goReplace{Old: args[0], New: args[arrow+1]}
		if arrow >= 2 {
			r.OldVersion = args[1]
		}
		if len(args) > arrow+2 {
			r.NewVersion = args[arrow+2]
		}
		m.Replaces = append(m.Replaces, r)
	}
}

// replaceFor returns the replace directive that applies to module mod at
// version v: one naming that exact version wins over one naming none, which
// is the go command's own precedence.
func (m goModule) replaceFor(mod, v string) (goReplace, bool) {
	var any goReplace
	found := false
	for _, r := range m.Replaces {
		if r.Old != mod {
			continue
		}
		if r.OldVersion != "" && r.OldVersion == v {
			return r, true
		}
		if r.OldVersion == "" {
			any, found = r, true
		}
	}
	return any, found
}

// goModFields splits a go.mod line into words, unquoting quoted ones.
func goModFields(line string) []string {
	var out []string
	for _, w := range strings.Fields(line) {
		if len(w) >= 2 && (w[0] == '"' || w[0] == '`') {
			if u, err := strconv.Unquote(w); err == nil {
				w = u
			}
		}
		out = append(out, w)
	}
	return out
}

// goModDir is the module root a go.mod path names, "" for the repository root.
func goModDir(file string) string {
	d := path.Dir(file)
	if d == "." || d == "/" {
		return ""
	}
	return d
}

// goModIgnored reports whether a go.mod sits where the go command would never
// treat it as a module: under testdata, vendor, or a directory whose name
// starts with "." or "_". Fixture modules inside a provider's tests would
// otherwise claim module paths the provider does not publish.
func goModIgnored(file string) bool {
	for _, seg := range strings.Split(path.Dir(file), "/") {
		if seg == "testdata" || seg == "vendor" ||
			(seg != "." && (strings.HasPrefix(seg, ".") || strings.HasPrefix(seg, "_"))) {
			return true
		}
	}
	return false
}

// modulesFromFiles turns a member's go.mod files into its modules, sorted by
// path. Files with no module directive, and ignored locations, are dropped.
func modulesFromFiles(files map[string]string) []goModule {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var out []goModule
	for _, p := range paths {
		if goModIgnored(p) {
			continue
		}
		m := parseGoModText(p, files[p])
		if m.Path == "" {
			continue
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Dir < out[j].Dir
	})
	return out
}

// governingModule returns the module, among mods, whose root directory is the
// nearest ancestor of file: the go.mod the go command would use for it.
func governingModule(mods []goModule, file string) (goModule, bool) {
	best, found := goModule{}, false
	for _, m := range mods {
		if m.Dir != "" && file != m.Dir && !strings.HasPrefix(file, m.Dir+"/") {
			continue
		}
		if !found || len(m.Dir) > len(best.Dir) {
			best, found = m, true
		}
	}
	return best, found
}

// importInModule reports whether import path p is module m itself or one of
// its packages. The "/" is the whole rule: example.com/library is not a
// package of example.com/lib.
func importInModule(p, m string) bool {
	return p == m || strings.HasPrefix(p, m+"/")
}

var majorSuffix = regexp.MustCompile(`^v[0-9]+$`)
var gopkgSuffix = regexp.MustCompile(`\.v[0-9]+$`)

// defaultPackageName is the name an unaliased import of p is assumed to bind
// when the package clause is not known: the last path element, skipping a
// major-version element (/v2) and dropping a gopkg.in-style .vN suffix.
func defaultPackageName(p string) string {
	parts := strings.Split(p, "/")
	last := parts[len(parts)-1]
	if majorSuffix.MatchString(last) && len(parts) > 1 {
		last = parts[len(parts)-2]
	}
	return gopkgSuffix.ReplaceAllString(last, "")
}

// pseudoVersionRev is the commit-hash part of a Go pseudo-version
// (v0.0.0-20231010120000-abcdef123456), or "" when v is not one.
var pseudoVersion = regexp.MustCompile(`-(?:0\.)?[0-9]{14}-([0-9a-f]{12})(?:\+incompatible)?$`)

func pseudoVersionRev(v string) string {
	if m := pseudoVersion.FindStringSubmatch(v); m != nil {
		return m[1]
	}
	return ""
}

// isLocalReplace reports whether a replace target is a filesystem path rather
// than a module path, by the go command's own rule.
func isLocalReplace(target string) bool {
	return strings.HasPrefix(target, "./") || strings.HasPrefix(target, "../") ||
		strings.HasPrefix(target, "/") || target == "." || target == ".." ||
		(len(target) > 1 && target[1] == ':') // a Windows drive letter
}
