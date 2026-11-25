package licenses

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Sources, in order of trust.
const (
	// SourceDeclared is a licence the dependency's own metadata states.
	SourceDeclared = "declared-metadata"
	// SourceFile is a licence identified from a licence file's text.
	SourceFile = "license-file"
	// SourceUnknown is no licence found; Reason says what was looked for.
	SourceUnknown = "unknown"
)

// Dependency is one dependency to find a licence for.
type Dependency struct {
	Name      string
	Version   string
	Ecosystem string
	// Manifest is the repository-relative path of the manifest that
	// declares it. npm and Python look for installed packages next to it.
	Manifest string
}

// Result is the licence found for one dependency.
type Result struct {
	// License is an SPDX expression, or "unknown".
	License string `json:"license"`
	Source  string `json:"source"`
	// Path is the file the licence was read from: repository-relative when
	// it is inside the repository, "~/"-relative under the home directory.
	Path       string   `json:"path,omitempty"`
	Confidence string   `json:"confidence,omitempty"`
	Category   Category `json:"category"`
	// Declared is the metadata's own words when they are not an SPDX
	// expression this package recognises.
	Declared string `json:"declared,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// Env says where the local caches are. Every path is read, never written.
type Env struct {
	Root       string
	Home       string
	GoModCache string
	CargoHome  string
	MavenRepo  string
	GradleHome string
	// Budget bounds the whole resolution. Zero means DefaultBudget.
	Budget time.Duration
}

// DefaultBudget bounds one resolution. What is not reached in time stays
// unknown, and the reason says so.
const DefaultBudget = 20 * time.Second

// EnvFromOS finds the caches the way the tools that fill them do, from
// environment variables and their documented defaults, without running any
// of those tools.
func EnvFromOS(root string) Env {
	home, _ := os.UserHomeDir()
	e := Env{Root: root, Home: home}

	e.GoModCache = os.Getenv("GOMODCACHE")
	gopath := os.Getenv("GOPATH")
	if e.GoModCache == "" || gopath == "" {
		// `go env -w` writes here; read it rather than run go.
		if dir, err := os.UserConfigDir(); err == nil {
			if b, err := readBounded(filepath.Join(dir, "go", "env"), 64<<10); err == nil {
				for _, line := range strings.Split(string(b), "\n") {
					k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
					if !ok {
						continue
					}
					switch k {
					case "GOMODCACHE":
						if e.GoModCache == "" {
							e.GoModCache = v
						}
					case "GOPATH":
						if gopath == "" {
							gopath = v
						}
					}
				}
			}
		}
	}
	if e.GoModCache == "" {
		if gopath != "" {
			e.GoModCache = filepath.Join(filepath.SplitList(gopath)[0], "pkg", "mod")
		} else if home != "" {
			e.GoModCache = filepath.Join(home, "go", "pkg", "mod")
		}
	}

	e.CargoHome = os.Getenv("CARGO_HOME")
	if e.CargoHome == "" && home != "" {
		e.CargoHome = filepath.Join(home, ".cargo")
	}

	if home != "" {
		e.MavenRepo = filepath.Join(home, ".m2", "repository")
		if b, err := readBounded(filepath.Join(home, ".m2", "settings.xml"), 1<<20); err == nil {
			if m := localRepo.FindSubmatch(b); m != nil {
				if p := strings.TrimSpace(string(m[1])); p != "" && !strings.Contains(p, "${") {
					e.MavenRepo = p
				}
			}
		}
	}
	e.GradleHome = os.Getenv("GRADLE_USER_HOME")
	if e.GradleHome == "" && home != "" {
		e.GradleHome = filepath.Join(home, ".gradle")
	}
	return e
}

var localRepo = regexp.MustCompile(`<localRepository>([^<]+)</localRepository>`)

// Stats describes one resolution.
type Stats struct {
	Elapsed time.Duration
	// BudgetSpent says the time bound ended the resolution early.
	BudgetSpent bool
}

// Resolve finds the licence of each dependency, in order. It never fails: a
// licence that cannot be found is a Result with Source "unknown" and a reason.
func Resolve(ctx context.Context, env Env, deps []Dependency) ([]Result, Stats) {
	start := time.Now()
	budget := env.Budget
	if budget <= 0 {
		budget = DefaultBudget
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	r := newResolver(env)
	out := make([]Result, len(deps))
	var st Stats
	memo := map[string]Result{}
	for i, d := range deps {
		if ctx.Err() != nil {
			st.BudgetSpent = true
			out[i] = unknown(fmt.Sprintf("the %s time bound was spent before this dependency was looked up", budget))
			continue
		}
		key := r.key(d)
		res, ok := memo[key]
		if !ok {
			res = r.resolve(d)
			res.Category = CategoryOf(res.License)
			memo[key] = res
		}
		out[i] = res
	}
	st.Elapsed = time.Since(start)
	return out, st
}

func unknown(reason string) Result {
	return Result{License: "unknown", Source: SourceUnknown, Category: Unknown, Reason: reason}
}

// ---------------------------------------------------------------- resolver

type resolver struct {
	env      Env
	realRoot string

	locks  map[string]map[string]string // package-lock path -> "node_modules/x" -> licence
	venvs  []string                     // site-packages directories, found once
	vFound bool
	dists  map[string]string // normalised project name -> .dist-info directory

	crates      map[string][]crateDir // crate name -> cached copies
	cratesFound bool
	cargoLocks  map[string]map[string][]string
}

type crateDir struct{ version, path string }

func newResolver(env Env) *resolver {
	r := &resolver{env: env, locks: map[string]map[string]string{}, cargoLocks: map[string]map[string][]string{}}
	if real, err := filepath.EvalSymlinks(env.Root); err == nil {
		r.realRoot = real
	} else {
		r.realRoot = env.Root
	}
	return r
}

func (r *resolver) key(d Dependency) string {
	k := d.Ecosystem + "\x00" + d.Name + "\x00" + d.Version
	if d.Ecosystem == "npm" || d.Ecosystem == "pypi" || d.Ecosystem == "cargo" || d.Ecosystem == "go" {
		k += "\x00" + filepath.Dir(d.Manifest)
	}
	return k
}

func (r *resolver) resolve(d Dependency) Result {
	switch d.Ecosystem {
	case "go":
		return r.goModule(d)
	case "npm":
		return r.npm(d)
	case "pypi":
		return r.python(d)
	case "cargo":
		return r.cargo(d)
	case "maven", "gradle":
		return r.maven(d)
	}
	return unknown(fmt.Sprintf("no local licence source is known for the %q ecosystem", d.Ecosystem))
}

// display shortens a path for a report: repository-relative inside the
// repository, "~/"-relative under the home directory.
func (r *resolver) display(p string) string {
	for _, base := range []struct{ dir, prefix string }{{r.env.Root, ""}, {r.realRoot, ""}, {r.env.Home, "~/"}} {
		if base.dir == "" {
			continue
		}
		if rel, err := filepath.Rel(base.dir, p); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return base.prefix + filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(p)
}

// inRepo resolves p and reports whether it stays inside the repository. A
// repository is untrusted input: node_modules/x may be a symbolic link to
// anywhere, and is followed only while it stays inside.
func (r *resolver) inRepo(p string) (string, bool) {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(r.realRoot, real)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", false
	}
	return real, true
}

// ancestors lists dir and each parent up to and including the repository root.
func (r *resolver) ancestors(manifest string) []string {
	dir := filepath.Join(r.env.Root, filepath.Dir(filepath.FromSlash(manifest)))
	var out []string
	for i := 0; i < 32; i++ {
		out = append(out, dir)
		rel, err := filepath.Rel(r.env.Root, dir)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			break
		}
		dir = filepath.Dir(dir)
	}
	return out
}

// ---------------------------------------------------------------- files

// maxMetaBytes bounds a metadata file: package.json, METADATA, Cargo.toml, POM.
const maxMetaBytes = 1 << 20

// readBounded reads at most max bytes of a regular file.
func readBounded(path string, max int64) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- paths are cache or repository locations, read only
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	return io.ReadAll(io.LimitReader(f, max))
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

var licenseFileName = regexp.MustCompile(`(?i)^(?:un)?(?:licen[cs]e|copying|copyright)(?:[-_. ].*)?$`)

// maxLicenseFiles bounds the licence files read from one directory.
const maxLicenseFiles = 6

// fromLicenseFiles classifies the licence files at the top of dir.
func (r *resolver) fromLicenseFiles(dir string) (Result, bool) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return Result{}, false
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && licenseFileName.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) > maxLicenseFiles {
		names = names[:maxLicenseFiles]
	}
	return r.classifyFiles(dir, names)
}

func (r *resolver) classifyFiles(dir string, names []string) (Result, bool) {
	if len(names) == 0 {
		return Result{}, false
	}
	var ids []string
	conf, first := Exact, ""
	for _, n := range names {
		p := filepath.Join(dir, n)
		b, err := readBounded(p, maxClassifyBytes)
		if err != nil {
			continue
		}
		m := Classify(b)
		if m.ID == "" {
			continue
		}
		if first == "" {
			first = p
		}
		ids = append(ids, m.ID)
		conf = weaker(conf, m.Confidence)
	}
	if len(ids) == 0 {
		return Result{
			License: "unknown", Source: SourceUnknown, Path: r.display(filepath.Join(dir, names[0])),
			Reason: fmt.Sprintf("licence file %s was found but its text is not one the classifier recognises", names[0]),
		}, true
	}
	ids = dropIncorporated(ids)
	res := Result{License: joinIDs(ids, "AND"), Source: SourceFile, Path: r.display(first), Confidence: conf}
	if len(ids) > 1 {
		res.Reason = "several licence files; all are reported as applying until shown otherwise"
	}
	return res, true
}

// dropIncorporated removes the GPL that an LGPL file builds on: LGPL-3.0 is
// a set of permissions on top of GPL-3.0, so projects ship both texts.
func dropIncorporated(ids []string) []string {
	has := map[string]bool{}
	for _, id := range ids {
		has[id] = true
	}
	if !has["LGPL-3.0-only"] || !has["GPL-3.0-only"] {
		return ids
	}
	out := ids[:0]
	for _, id := range ids {
		if id != "GPL-3.0-only" {
			out = append(out, id)
		}
	}
	return out
}

func weaker(a, b string) string {
	order := map[string]int{Exact: 0, High: 1, Medium: 2, Low: 3}
	if order[b] > order[a] {
		return b
	}
	return a
}

// downgrade lowers a confidence one step, for evidence about a version near
// the one declared rather than the one declared.
func downgrade(c string) string {
	switch c {
	case Exact:
		return High
	case High:
		return Medium
	}
	return Low
}

// declared builds a result from a metadata licence string.
func (r *resolver) declared(value, path string) (Result, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Result{}, false
	}
	if e := NormalizeExpression(value); e != "" {
		return Result{License: e, Source: SourceDeclared, Path: r.display(path), Confidence: Exact}, true
	}
	if e := FromName(value); e != "" {
		return Result{License: e, Source: SourceDeclared, Path: r.display(path), Confidence: High, Declared: value}, true
	}
	return Result{
		License: "unknown", Source: SourceUnknown, Path: r.display(path), Declared: value,
		Reason: fmt.Sprintf("the metadata declares %q, which is not an SPDX expression or a licence name this report recognises", value),
	}, false
}

// ---------------------------------------------------------------- Go

// escapeModule applies the module cache's case encoding: each upper-case
// letter becomes '!' and its lower-case form.
func escapeModule(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c >= 'A' && c <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(c + ('a' - 'A'))
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func (r *resolver) goModule(d Dependency) Result {
	// A module replaced by a directory of this repository is first-party: no
	// cache holds it, and its licence is the repository's.
	if res, ok := r.goFirstParty(d); ok {
		return res
	}
	if !strings.HasPrefix(d.Version, "v") || strings.ContainsAny(d.Name, "\\") || strings.Contains(d.Name, "..") {
		return unknown("no exact module version is declared, so no cached copy can be named")
	}
	// A vendored copy is the exact version the build uses.
	if v, ok := r.inRepo(filepath.Join(r.env.Root, "vendor", filepath.FromSlash(d.Name))); ok {
		if res, ok := r.fromLicenseFiles(v); ok {
			return res
		}
	}
	if r.env.GoModCache == "" {
		return unknown("no Go module cache location is known (GOMODCACHE, GOPATH and ~/go are all unset)")
	}
	dir := filepath.Join(r.env.GoModCache, filepath.FromSlash(escapeModule(d.Name))+"@"+escapeModule(d.Version))
	if !isDir(dir) {
		return unknown(fmt.Sprintf("%s@%s is not in the Go module cache (%s) or vendor/", d.Name, d.Version, r.display(r.env.GoModCache)))
	}
	if res, ok := r.fromLicenseFiles(dir); ok {
		return res
	}
	return unknown("the cached module has no LICENSE, LICENCE or COPYING file at its root")
}

// ---------------------------------------------------------------- npm

func (r *resolver) npm(d Dependency) Result {
	if strings.Contains(d.Name, "..") || strings.HasPrefix(d.Name, "/") {
		return unknown("not a valid package name")
	}
	if dir, ok := r.localNPMPath(d.Manifest, d.Version); ok {
		return r.firstParty(dir, fmt.Sprintf("%s is linked from %s", d.Name, r.display(dir)))
	}
	var pkgDir string
	for _, dir := range r.ancestors(d.Manifest) {
		if real, ok := r.inRepo(filepath.Join(dir, "node_modules", filepath.FromSlash(d.Name))); ok && isDir(real) {
			pkgDir = real
			break
		}
	}
	var fallback Result
	if pkgDir != "" {
		pj := filepath.Join(pkgDir, "package.json")
		if b, err := readBounded(pj, maxMetaBytes); err == nil {
			if res, ok := r.npmPackageJSON(b, pj, pkgDir); ok {
				return res
			} else if res.Reason != "" {
				fallback = res
			}
		}
	}
	for _, dir := range r.ancestors(d.Manifest) {
		for _, name := range []string{"package-lock.json", "npm-shrinkwrap.json"} {
			lock := filepath.Join(dir, name)
			lic := r.lockLicense(lock, d.Name)
			if lic == "" {
				continue
			}
			if res, ok := r.declared(lic, lock); ok {
				return res
			}
		}
	}
	if pkgDir != "" {
		if res, ok := r.fromLicenseFiles(pkgDir); ok && res.Source != SourceUnknown {
			return res
		}
	}
	if fallback.Reason != "" {
		fallback.Category = Unknown
		return fallback
	}
	if pkgDir == "" {
		return unknown("not installed under node_modules, and no package-lock.json records its licence")
	}
	return unknown("the installed package declares no licence and has no recognisable licence file")
}

// npmPackageJSON reads license / licenses from an installed package.json.
func (r *resolver) npmPackageJSON(b []byte, path, pkgDir string) (Result, bool) {
	var pj struct {
		License  anyLicense   `json:"license"`
		Licenses []anyLicense `json:"licenses"`
	}
	if err := jsonUnmarshal(b, &pj); err != nil {
		return Result{}, false
	}
	value := string(pj.License)
	if value == "" && len(pj.Licenses) > 0 {
		var parts []string
		for _, l := range pj.Licenses {
			if l != "" {
				parts = append(parts, string(l))
			}
		}
		// The old "licenses" array lists alternatives.
		if len(parts) > 0 {
			var ids []string
			for _, p := range parts {
				if e := FromName(p); e != "" {
					ids = append(ids, e)
				} else {
					ids = nil
					break
				}
			}
			if len(ids) > 0 {
				return Result{License: joinIDs(ids, "OR"), Source: SourceDeclared, Path: r.display(path), Confidence: High,
					Declared: strings.Join(parts, ", ")}, true
			}
			value = strings.Join(parts, " OR ")
		}
	}
	if value == "" {
		return Result{}, false
	}
	if strings.EqualFold(value, "UNLICENSED") {
		return Result{License: "unknown", Source: SourceUnknown, Path: r.display(path), Declared: value,
			Reason: "the package declares UNLICENSED: its authors grant no licence to use it"}, false
	}
	if rest, ok := cutPrefixFold(value, "SEE LICENSE IN "); ok {
		name := filepath.Clean(strings.TrimSpace(rest))
		if !filepath.IsAbs(name) && !strings.HasPrefix(name, "..") {
			if res, ok := r.classifyFiles(pkgDir, []string{name}); ok && res.Source != SourceUnknown {
				res.Declared = value
				return res, true
			}
		}
		return Result{License: "unknown", Source: SourceUnknown, Path: r.display(path), Declared: value,
			Reason: "the package points at its own licence file, and that file's text is not one the classifier recognises"}, false
	}
	return r.declared(value, path)
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return "", false
}

// maxLockBytes bounds a package-lock.json. Large monorepos reach tens of MB.
const maxLockBytes = 64 << 20

// lockLicense returns the licence a package-lock.json (v2 or v3) records for
// the top-level copy of name.
func (r *resolver) lockLicense(lock, name string) string {
	m, ok := r.locks[lock]
	if !ok {
		m = map[string]string{}
		r.locks[lock] = m
		if _, inside := r.inRepo(lock); !inside {
			return ""
		}
		b, err := readBounded(lock, maxLockBytes)
		if err != nil {
			return ""
		}
		var pl struct {
			Packages map[string]struct {
				License anyLicense `json:"license"`
			} `json:"packages"`
		}
		if jsonUnmarshal(b, &pl) != nil {
			return ""
		}
		for k, v := range pl.Packages {
			if v.License != "" && strings.HasPrefix(k, "node_modules/") && !strings.Contains(k[len("node_modules/"):], "/node_modules/") {
				m[k] = string(v.License)
			}
		}
	}
	return m["node_modules/"+name]
}

// ---------------------------------------------------------------- Python

var pyNameSep = regexp.MustCompile(`[-_.]+`)

func pyNormalize(n string) string {
	if i := strings.IndexAny(n, "[ ;<>=!~"); i >= 0 {
		n = n[:i]
	}
	return pyNameSep.ReplaceAllString(strings.ToLower(strings.TrimSpace(n)), "-")
}

// venvNames are the directories a project virtualenv is conventionally in.
var venvNames = []string{".venv", "venv", "env", ".env", "virtualenv", ".virtualenv"}

func (r *resolver) findDists() {
	if r.vFound {
		return
	}
	r.vFound = true
	r.dists = map[string]string{}
	for _, v := range venvNames {
		base, ok := r.inRepo(filepath.Join(r.env.Root, v))
		if !ok || !isDir(base) {
			continue
		}
		var sites []string
		for _, pat := range []string{"lib/python*/site-packages", "lib64/python*/site-packages", "Lib/site-packages"} {
			ms, _ := filepath.Glob(filepath.Join(base, filepath.FromSlash(pat)))
			sites = append(sites, ms...)
		}
		for _, site := range sites {
			r.venvs = append(r.venvs, site)
			ents, err := os.ReadDir(site)
			if err != nil {
				continue
			}
			for _, e := range ents {
				n := e.Name()
				if !e.IsDir() || !strings.HasSuffix(n, ".dist-info") {
					continue
				}
				stem := strings.TrimSuffix(n, ".dist-info")
				if i := strings.Index(stem, "-"); i > 0 {
					stem = stem[:i]
				}
				key := pyNormalize(stem)
				if _, seen := r.dists[key]; !seen {
					r.dists[key] = filepath.Join(site, n)
				}
			}
		}
	}
}

func (r *resolver) python(d Dependency) Result {
	r.findDists()
	if len(r.venvs) == 0 {
		return unknown("no virtualenv (.venv, venv, env) with site-packages in the repository, so no installed metadata to read")
	}
	dist, ok := r.dists[pyNormalize(d.Name)]
	if !ok {
		return unknown("not installed in the repository's virtualenv")
	}
	path := filepath.Join(dist, "METADATA")
	b, err := readBounded(path, maxMetaBytes)
	if err != nil {
		return unknown("the installed package has no readable METADATA")
	}
	h := parseMetadata(b)
	var fallback Result
	if v := first(h["license-expression"]); v != "" {
		if res, ok := r.declared(v, path); ok {
			return res
		} else {
			fallback = res
		}
	}
	if v := first(h["license"]); v != "" {
		if len(v) <= 100 && !strings.Contains(v, "\n") {
			if !strings.EqualFold(v, "UNKNOWN") {
				if res, ok := r.declared(v, path); ok {
					if res.Confidence == Exact {
						res.Confidence = High // License: is free text, not an SPDX field
					}
					return res
				} else if fallback.Reason == "" {
					fallback = res
				}
			}
		} else if m := Classify([]byte(v)); m.ID != "" {
			return Result{License: m.ID, Source: SourceDeclared, Path: r.display(path), Confidence: m.Confidence}
		}
	}
	var ids, names []string
	for _, c := range h["classifier"] {
		parts := strings.Split(c, "::")
		if len(parts) < 2 || strings.TrimSpace(parts[0]) != "License" {
			continue
		}
		name := strings.TrimSpace(parts[len(parts)-1])
		names = append(names, name)
		if id := FromName(name); id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) > 0 && len(ids) == len(names) {
		conf := High
		if len(ids) > 1 {
			conf = Medium // several classifiers do not say whether they are a choice
		}
		return Result{License: joinIDs(ids, "OR"), Source: SourceDeclared, Path: r.display(path), Confidence: conf,
			Declared: strings.Join(names, ", ")}
	}
	// License files: PEP 639 puts them under licenses/, older wheels at the top.
	var files []string
	for _, lf := range h["license-file"] {
		lf = filepath.Clean(filepath.FromSlash(lf))
		if filepath.IsAbs(lf) || strings.HasPrefix(lf, "..") {
			continue
		}
		for _, cand := range []string{filepath.Join("licenses", lf), lf} {
			if _, err := os.Stat(filepath.Join(dist, cand)); err == nil {
				files = append(files, cand)
				break
			}
		}
	}
	if len(files) > maxLicenseFiles {
		files = files[:maxLicenseFiles]
	}
	if res, ok := r.classifyFiles(dist, files); ok && res.Source != SourceUnknown {
		return res
	}
	if res, ok := r.fromLicenseFiles(filepath.Join(dist, "licenses")); ok && res.Source != SourceUnknown {
		return res
	}
	if res, ok := r.fromLicenseFiles(dist); ok && res.Source != SourceUnknown {
		return res
	}
	if fallback.Reason != "" {
		fallback.Category = Unknown
		return fallback
	}
	if len(names) > 0 {
		return Result{License: "unknown", Source: SourceUnknown, Path: r.display(path), Declared: strings.Join(names, ", "),
			Reason: "the classifiers name a licence family without its variant or version, and no licence file was installed"}
	}
	return unknown("the installed METADATA declares no licence and the package installed no licence file")
}

// parseMetadata reads the header block of a core-metadata file.
func parseMetadata(b []byte) map[string][]string {
	h := map[string][]string{}
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	sc.Buffer(make([]byte, 64<<10), maxMetaBytes)
	last := ""
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break
		}
		if (line[0] == ' ' || line[0] == '\t') && last != "" {
			vs := h[last]
			cont := strings.TrimSpace(line)
			cont = strings.TrimPrefix(cont, "|")
			vs[len(vs)-1] += "\n" + cont
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		last = strings.ToLower(strings.TrimSpace(k))
		h[last] = append(h[last], strings.TrimSpace(v))
	}
	return h
}

func first(vs []string) string {
	if len(vs) == 0 {
		return ""
	}
	return strings.TrimSpace(vs[0])
}

// ---------------------------------------------------------------- Cargo

var versionStart = regexp.MustCompile(`-[0-9]+\.[0-9]`)

func (r *resolver) findCrates() {
	if r.cratesFound {
		return
	}
	r.cratesFound = true
	r.crates = map[string][]crateDir{}
	if r.env.CargoHome == "" {
		return
	}
	idx, _ := filepath.Glob(filepath.Join(r.env.CargoHome, "registry", "src", "*"))
	for _, dir := range idx {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			n := e.Name()
			loc := versionStart.FindStringIndex(n)
			if loc == nil || !e.IsDir() {
				continue
			}
			name, version := n[:loc[0]], n[loc[0]+1:]
			r.crates[name] = append(r.crates[name], crateDir{version: version, path: filepath.Join(dir, n)})
		}
	}
}

// cargoLockVersions reads the versions a Cargo.lock pins, by crate name.
func (r *resolver) cargoLockVersions(manifest string) map[string][]string {
	for _, dir := range r.ancestors(manifest) {
		lock := filepath.Join(dir, "Cargo.lock")
		if m, ok := r.cargoLocks[lock]; ok {
			if m != nil {
				return m
			}
			continue
		}
		r.cargoLocks[lock] = nil
		if _, inside := r.inRepo(lock); !inside {
			continue
		}
		b, err := readBounded(lock, 16<<20)
		if err != nil {
			continue
		}
		m := map[string][]string{}
		name := ""
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			switch {
			case line == "[[package]]":
				name = ""
			case strings.HasPrefix(line, "name = "):
				name = strings.Trim(strings.TrimPrefix(line, "name = "), `"`)
			case strings.HasPrefix(line, "version = ") && name != "":
				m[name] = append(m[name], strings.Trim(strings.TrimPrefix(line, "version = "), `"`))
			}
		}
		r.cargoLocks[lock] = m
		return m
	}
	return nil
}

func (r *resolver) cargo(d Dependency) Result {
	r.findCrates()
	cached := r.crates[d.Name]
	if len(cached) == 0 {
		return unknown(fmt.Sprintf("the crate is not in the Cargo registry cache (%s)", r.display(filepath.Join(r.env.CargoHome, "registry", "src"))))
	}
	byVersion := map[string]string{}
	for _, c := range cached {
		byVersion[c.version] = c.path
	}
	want := strings.TrimLeft(strings.TrimSpace(d.Version), "^~=")
	var dir, note string
	if locked := r.cargoLockVersions(d.Manifest)[d.Name]; len(locked) > 0 {
		for _, v := range locked {
			if _, ok := byVersion[v]; ok && (dir == "" || v == want || strings.HasPrefix(v, want+".")) {
				dir = byVersion[v]
			}
		}
	}
	if dir == "" {
		if p, ok := byVersion[want]; ok {
			dir = p
		}
	}
	if dir == "" && want != "" {
		best := ""
		for v := range byVersion {
			if strings.HasPrefix(v, want+".") || v == want {
				if best == "" || versionLess(best, v) {
					best = v
				}
			}
		}
		if best != "" {
			dir = byVersion[best]
			note = fmt.Sprintf("no Cargo.lock pins this crate; read the cached %s, the newest matching the declared %s", best, d.Version)
		}
	}
	if dir == "" {
		return unknown(fmt.Sprintf("no cached copy of a version matching %q", d.Version))
	}
	res := r.cargoDir(dir)
	if note != "" && res.Source != SourceUnknown {
		res.Confidence = downgrade(res.Confidence)
		res.Reason = note
	}
	return res
}

func (r *resolver) cargoDir(dir string) Result {
	path := filepath.Join(dir, "Cargo.toml")
	b, err := readBounded(path, maxMetaBytes)
	if err == nil {
		lic, file := cargoLicense(string(b))
		if lic != "" {
			if res, ok := r.declared(lic, path); ok {
				return res
			}
		}
		if file != "" {
			file = filepath.Clean(filepath.FromSlash(file))
			if !filepath.IsAbs(file) && !strings.HasPrefix(file, "..") {
				if res, ok := r.classifyFiles(dir, []string{file}); ok && res.Source != SourceUnknown {
					return res
				}
			}
		}
	}
	if res, ok := r.fromLicenseFiles(dir); ok && res.Source != SourceUnknown {
		return res
	}
	return unknown("the cached crate declares no licence and has no recognisable licence file")
}

// cargoLicense reads license and license-file from a Cargo.toml's [package].
func cargoLicense(src string) (license, file string) {
	section := ""
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			section = strings.Trim(t, "[] ")
			continue
		}
		if section != "package" {
			continue
		}
		k, v, ok := strings.Cut(t, "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch strings.TrimSpace(k) {
		case "license":
			license = v
		case "license-file":
			file = v
		}
	}
	return license, file
}

func versionLess(a, b string) bool {
	pa, pb := strings.FieldsFunc(a, splitVersion), strings.FieldsFunc(b, splitVersion)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		na, ea := strconv.Atoi(pa[i])
		nb, eb := strconv.Atoi(pb[i])
		if ea == nil && eb == nil {
			if na != nb {
				return na < nb
			}
			continue
		}
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return len(pa) < len(pb)
}

func splitVersion(c rune) bool { return c == '.' || c == '-' || c == '+' }

// Summary counts results by category, source and confidence. Every category
// and source is present, zero or not, so a reader can rely on the keys.
type Summary struct {
	ByCategory   map[Category]int `json:"by_category"`
	BySource     map[string]int   `json:"by_source"`
	ByConfidence map[string]int   `json:"by_confidence"`
}

// Summarize counts results.
func Summarize(rs []Result) Summary {
	s := Summary{ByCategory: map[Category]int{}, BySource: map[string]int{}, ByConfidence: map[string]int{}}
	for _, c := range Categories {
		s.ByCategory[c] = 0
	}
	for _, src := range []string{SourceDeclared, SourceFile, SourceFirstParty, SourceUnknown} {
		s.BySource[src] = 0
	}
	for _, r := range rs {
		s.ByCategory[r.Category]++
		s.BySource[r.Source]++
		if r.Confidence != "" {
			s.ByConfidence[r.Confidence]++
		}
	}
	return s
}
