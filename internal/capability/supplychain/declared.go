package supplychain

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/internal/analyze/deps"
)

// Declared is one dependency exactly as the index recorded it from a manifest.
type Declared struct {
	Name      string
	Ecosystem deps.Ecosystem
	// Version is the manifest's own text: a pin, a range, a placeholder, or "".
	Version  string
	Scope    string
	Manifest string
	Line     int
	// Indirect is the manifest's own marker for a dependency no first-party
	// file is expected to import (Go's `// indirect`, npm's peerDependencies).
	Indirect bool
}

// VersionState says how much is known about the version of a dependency.
//
// The distinction is the whole point of the SBOM's marker and of the
// reachability report's "could not compare": a range is not a version, and a
// tool that silently treats `^4.17.20` as 4.17.20 has made up the answer to
// the only question an advisory asks.
type VersionState string

const (
	// StatePinned: the manifest states one exact version.
	StatePinned VersionState = "pinned"
	// StateLocked: the manifest gives a range or nothing, and a lockfile beside
	// it states the version that was resolved.
	StateLocked VersionState = "locked"
	// StateRange: declared as a range, and no lockfile was found that pins it.
	StateRange VersionState = "range"
	// StateUnpinned: no version constraint at all — empty, "*", "latest", or
	// managed somewhere this tool does not read (a parent POM, a Gradle BOM).
	StateUnpinned VersionState = "unpinned"
	// StateUnresolved: the manifest names a version this tool cannot read as
	// one — a property placeholder, a Git or path source, a local replacement.
	StateUnresolved VersionState = "unresolved"
)

// Concrete reports whether the state carries one exact version.
func (s VersionState) Concrete() bool { return s == StatePinned || s == StateLocked }

// Component is a declared dependency with its version worked out as far as the
// manifest and any lockfile allow, and no further.
type Component struct {
	Ecosystem deps.Ecosystem `json:"ecosystem"`
	// Name is the package the build actually uses. It differs from
	// DeclaredName under a Go `replace` or a Cargo `package = "..."` rename.
	Name         string `json:"name"`
	DeclaredName string `json:"declared_name,omitempty"`
	// Declared is the version text as the manifest states it.
	Declared string `json:"declared"`
	// Version is the one exact version, when State is concrete; otherwise "".
	Version string       `json:"version,omitempty"`
	State   VersionState `json:"version_state"`
	// VersionSource is where Version (or the declaration) was read from.
	VersionSource string `json:"version_source"`
	// Note explains a state that is not a plain pin, in one sentence.
	Note     string `json:"note,omitempty"`
	Scope    string `json:"scope"`
	Indirect bool   `json:"indirect,omitempty"`
	Manifest string `json:"manifest"`
	Line     int    `json:"line"`
	PURL     string `json:"purl,omitempty"`
	// License is the licence found for it on this machine, when the caller
	// looked one up; nil leaves the SBOM component without one.
	License *ComponentLicense `json:"license,omitempty"`
}

// FileReader returns the bytes of a repository-relative, slash-separated path.
// It is how lockfiles and manifests are read; a nil reader disables every
// enrichment and each component is classified from the index alone.
type FileReader func(relPath string) ([]byte, error)

// Resolver turns declared dependencies into components.
//
// It holds the files it has read, so resolving two hundred dependencies of one
// package.json parses its lockfile once.
type Resolver struct {
	read  FileReader
	files map[string]*string

	npmLocks  map[string]*npmLock
	yarnLocks map[string]map[string]string
	tomlLocks map[string]map[string][]string
	gradle    map[string]map[string][]string
	replaces  map[string][]goReplace
	pomProps  map[string]map[string]string

	lockfiles map[string]bool
}

// NewResolver builds a resolver. read may be nil.
func NewResolver(read FileReader) *Resolver {
	return &Resolver{
		read:      read,
		files:     map[string]*string{},
		npmLocks:  map[string]*npmLock{},
		yarnLocks: map[string]map[string]string{},
		tomlLocks: map[string]map[string][]string{},
		gradle:    map[string]map[string][]string{},
		replaces:  map[string][]goReplace{},
		pomProps:  map[string]map[string]string{},
		lockfiles: map[string]bool{},
	}
}

// LockfilesRead lists the lockfiles a version was actually taken from, sorted.
func (r *Resolver) LockfilesRead() []string {
	out := make([]string, 0, len(r.lockfiles))
	for p := range r.lockfiles {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// file returns a file's text and whether it could be read. Misses are cached.
func (r *Resolver) file(rel string) (string, bool) {
	if r.read == nil {
		return "", false
	}
	if cached, ok := r.files[rel]; ok {
		if cached == nil {
			return "", false
		}
		return *cached, true
	}
	b, err := r.read(rel)
	if err != nil {
		r.files[rel] = nil
		return "", false
	}
	s := string(b)
	r.files[rel] = &s
	return s, true
}

// ModuleName returns the project name a manifest declares for itself, or "".
func (r *Resolver) ModuleName(manifest string) string {
	src, ok := r.file(manifest)
	if !ok {
		return ""
	}
	m, _ := deps.Parse(manifest, []byte(src))
	return m.Module
}

// ancestors lists a manifest's directory and each parent up to the root, as
// repository-relative paths ("" is the root). A lockfile lives beside its
// manifest or, in a workspace, at the workspace root above it.
func ancestors(manifest string) []string {
	dir := path.Dir(manifest)
	if dir == "." {
		dir = ""
	}
	out := []string{dir}
	for dir != "" {
		dir = path.Dir(dir)
		if dir == "." {
			dir = ""
		}
		out = append(out, dir)
	}
	return out
}

func joinRel(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// ResolveAll resolves every declaration, in a stable order.
func (r *Resolver) ResolveAll(declared []Declared) []Component {
	out := make([]Component, 0, len(declared))
	for _, d := range declared {
		out = append(out, r.Resolve(d))
	}
	sort.SliceStable(out, func(i, j int) bool { return componentLess(out[i], out[j]) })
	return out
}

func componentLess(a, b Component) bool {
	if a.Ecosystem != b.Ecosystem {
		return a.Ecosystem < b.Ecosystem
	}
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	if a.Version != b.Version {
		return a.Version < b.Version
	}
	if a.Manifest != b.Manifest {
		return a.Manifest < b.Manifest
	}
	return a.Line < b.Line
}

// Resolve classifies one declaration.
func (r *Resolver) Resolve(d Declared) Component {
	c := Component{
		Ecosystem: d.Ecosystem, Name: d.Name, Declared: strings.TrimSpace(d.Version),
		Scope: d.Scope, Indirect: d.Indirect, Manifest: d.Manifest, Line: d.Line,
		VersionSource: fmt.Sprintf("%s:%d", d.Manifest, d.Line),
	}
	switch d.Ecosystem {
	case deps.EcosystemGo:
		r.resolveGo(&c)
	case deps.EcosystemNPM:
		r.resolveNPM(&c)
	case deps.EcosystemPyPI:
		r.resolvePyPI(&c)
	case deps.EcosystemCargo:
		r.resolveCargo(&c)
	case deps.EcosystemMaven:
		r.resolveMaven(&c)
	case deps.EcosystemGradle:
		r.resolveGradle(&c)
	default:
		c.State = StateUnresolved
		c.Note = fmt.Sprintf("ecosystem %q is not one this tool classifies versions for", d.Ecosystem)
	}
	if !c.State.Concrete() {
		c.Version = ""
	}
	c.PURL = PURL(c.Ecosystem, c.Name, c.Version)
	return c
}

// ---------------------------------------------------------------- Go

type goReplace struct{ oldPath, oldVersion, newPath, newVersion string }

// parseGoReplaces reads go.mod `replace` directives, single-line and block.
//
// They matter because a replaced module is not the module the manifest names:
// `replace golang.org/x/net => ./vendor/net` means no published version of
// x/net is being built at all, and comparing the declared version against an
// advisory would be answering a question about code that is not there.
func parseGoReplaces(src string) []goReplace {
	var out []goReplace
	inBlock := false
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "replace ("), line == "replace(":
			inBlock = true
			continue
		case inBlock && line == ")":
			inBlock = false
			continue
		case strings.HasPrefix(line, "replace "):
			line = strings.TrimSpace(strings.TrimPrefix(line, "replace "))
		case !inBlock:
			continue
		}
		left, right, ok := strings.Cut(line, "=>")
		if !ok {
			continue
		}
		l, rt := strings.Fields(left), strings.Fields(right)
		if len(l) == 0 || len(rt) == 0 {
			continue
		}
		rep := goReplace{oldPath: l[0], newPath: rt[0]}
		if len(l) > 1 {
			rep.oldVersion = l[1]
		}
		if len(rt) > 1 {
			rep.newVersion = rt[1]
		}
		out = append(out, rep)
	}
	return out
}

func (r *Resolver) resolveGo(c *Component) {
	if c.Declared == "" {
		c.State = StateUnpinned
		c.Note = "go.mod states no version for this module"
		return
	}
	c.State, c.Version = StatePinned, c.Declared

	reps, ok := r.replaces[c.Manifest]
	if !ok {
		if src, found := r.file(c.Manifest); found {
			reps = parseGoReplaces(src)
		}
		r.replaces[c.Manifest] = reps
	}
	for _, rep := range reps {
		if rep.oldPath != c.Name || (rep.oldVersion != "" && rep.oldVersion != c.Declared) {
			continue
		}
		if rep.newVersion == "" {
			// A replacement with no version is a filesystem path.
			c.State = StateUnresolved
			c.Note = fmt.Sprintf("go.mod replaces this module with the local path %s; "+
				"no published version is being built", rep.newPath)
			return
		}
		c.Version = rep.newVersion
		if rep.newPath != c.Name {
			c.DeclaredName, c.Name = c.Name, rep.newPath
			c.Note = fmt.Sprintf("go.mod replaces %s %s with %s %s",
				c.DeclaredName, c.Declared, rep.newPath, rep.newVersion)
		} else {
			c.Note = fmt.Sprintf("go.mod replaces the declared %s with %s", c.Declared, rep.newVersion)
		}
		return
	}
}

// ---------------------------------------------------------------- npm

var npmExact = regexp.MustCompile(`^[v=]?\s*([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)$`)

// npmNonRegistry are the prefixes of a dependency that does not come from the
// registry at a version: a Git URL, a tarball, a local path, a workspace
// sibling, an alias to another package.
var npmNonRegistry = []string{
	"git+", "git:", "git@", "github:", "gitlab:", "bitbucket:", "gist:", "http:", "https:",
	"file:", "link:", "workspace:", "npm:", "portal:", "patch:", "catalog:",
}

func (r *Resolver) resolveNPM(c *Component) {
	spec := c.Declared
	switch {
	case spec == "" || spec == "*" || spec == "x" || spec == "latest":
		c.State = StateUnpinned
		c.Note = "no version constraint: any published version satisfies it"
	case hasAnyPrefix(spec, npmNonRegistry) || (strings.Contains(spec, "/") && !strings.ContainsAny(spec, " <>=")):
		c.State = StateUnresolved
		c.Note = "not a registry version (a Git, URL, path, workspace or alias source)"
		return
	default:
		if m := npmExact.FindStringSubmatch(spec); m != nil {
			c.State, c.Version = StatePinned, m[1]
			return
		}
		if npmLooksLikeRange(spec) {
			c.State = StateRange
			c.Note = "declared as a range"
		} else {
			c.State = StateUnpinned
			c.Note = "declared as a distribution tag, which moves"
		}
	}

	if v, lock := r.npmLocked(c.Manifest, c.Name, spec); v != "" {
		c.State, c.Version, c.VersionSource = StateLocked, v, lock
		c.Note = fmt.Sprintf("declared as %q; %s resolves it", spec, lock)
		r.lockfiles[lock] = true
		return
	}
	c.Note += "; no package-lock.json, npm-shrinkwrap.json or yarn.lock entry pins it" +
		" (pnpm-lock.yaml is not read)"
}

// npmLooksLikeRange separates a range ("^1.2.0", "1.x", ">=1 <2") from a
// distribution tag ("next"). Both leave the version unknown; the note differs.
func npmLooksLikeRange(spec string) bool {
	return strings.ContainsAny(spec[:1], "^~<>=*0123456789") || strings.Contains(spec, "||")
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- PyPI

// pypiCleanSpec strips what follows a requirement's version specifier:
// environment markers, pip's per-requirement options and a line continuation.
func pypiCleanSpec(spec string) string {
	if i := strings.Index(spec, ";"); i >= 0 {
		spec = spec[:i]
	}
	if i := strings.Index(spec, " --"); i >= 0 {
		spec = spec[:i]
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(spec), `\`))
}

func (r *Resolver) resolvePyPI(c *Component) {
	spec := pypiCleanSpec(c.Declared)
	c.Declared = spec
	switch {
	case spec == "":
		c.State = StateUnpinned
		c.Note = "no version specifier"
	case strings.HasPrefix(spec, "@"):
		c.State = StateUnresolved
		c.Note = "a direct reference to a URL or path, not a version from the package index"
		return
	case strings.Contains(spec, ","):
		c.State = StateRange
		c.Note = "declared as a range"
	case strings.HasPrefix(spec, "==="):
		c.State, c.Version = StatePinned, strings.TrimSpace(spec[3:])
		return
	case strings.HasPrefix(spec, "=="):
		v := strings.TrimSpace(spec[2:])
		if strings.Contains(v, "*") {
			c.State = StateRange
			c.Note = "declared as a prefix match"
			break
		}
		c.State, c.Version = StatePinned, v
		return
	case strings.ContainsAny(spec[:1], "<>~!="):
		c.State = StateRange
		c.Note = "declared as a range"
	default:
		// A bare version with no operator is not something a manifest states.
		// It is what an index written before the operator was recorded holds
		// for pyproject.toml, where ">=2.0" and "==2.0" were both stored as
		// "2.0". Reading it as a pin would turn a range into a version.
		c.State = StateUnresolved
		c.Note = "the comparison operator was not recorded for this requirement; re-run 'graphlume index'"
		return
	}

	if v, lock := r.tomlLocked(c.Manifest, c.Name, []string{"poetry.lock", "uv.lock"}, pypiNormalize); v != "" {
		c.State, c.Version, c.VersionSource = StateLocked, v, lock
		c.Note = fmt.Sprintf("declared as %q; %s resolves it", spec, lock)
		r.lockfiles[lock] = true
		return
	}
	c.Note += "; no poetry.lock or uv.lock entry pins it"
}

var pypiSeparators = regexp.MustCompile(`[-_.]+`)

// pypiNormalize is PEP 503 name normalisation: case-insensitive, with runs of
// "-", "_" and "." equivalent.
func pypiNormalize(name string) string {
	return pypiSeparators.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
}

// ---------------------------------------------------------------- Cargo

var cargoPackageRename = regexp.MustCompile(`\bpackage\s*=\s*"([^"]+)"`)

func (r *Resolver) resolveCargo(c *Component) {
	// `alias = { package = "real-name", version = "1" }` declares a dependency
	// on real-name; the key is only what the source calls it.
	if src, ok := r.file(c.Manifest); ok {
		lines := strings.Split(src, "\n")
		if c.Line >= 1 && c.Line <= len(lines) {
			if m := cargoPackageRename.FindStringSubmatch(lines[c.Line-1]); m != nil && m[1] != c.Name {
				c.DeclaredName, c.Name = c.Name, m[1]
			}
		}
	}

	spec := c.Declared
	switch {
	case spec == "" || spec == "*":
		c.State = StateUnpinned
		c.Note = "no version constraint"
	case strings.HasPrefix(spec, "=") && !strings.Contains(spec, ",") && !strings.Contains(spec, "*"):
		c.State, c.Version = StatePinned, strings.TrimSpace(spec[1:])
		return
	default:
		c.State = StateRange
		c.Note = "declared as a range (Cargo reads a bare version as a caret requirement)"
	}

	if v, lock := r.tomlLocked(c.Manifest, c.Name, []string{"Cargo.lock"}, func(s string) string { return s }); v != "" {
		c.State, c.Version, c.VersionSource = StateLocked, v, lock
		c.Note = fmt.Sprintf("declared as %q; %s resolves it", spec, lock)
		r.lockfiles[lock] = true
		return
	}
	c.Note += "; no single Cargo.lock entry pins it"
}

// ---------------------------------------------------------------- Maven

var (
	pomPropertiesBlock = regexp.MustCompile(`(?s)<properties>(.*?)</properties>`)
	pomProperty        = regexp.MustCompile(`<([A-Za-z0-9_.-]+)>\s*([^<]*?)\s*</([A-Za-z0-9_.-]+)>`)
	pomPlaceholder     = regexp.MustCompile(`^\$\{([^}]+)\}$`)
)

func parsePOMProperties(src string) map[string]string {
	out := map[string]string{}
	for _, block := range pomPropertiesBlock.FindAllStringSubmatch(src, -1) {
		for _, m := range pomProperty.FindAllStringSubmatch(block[1], -1) {
			if m[1] == m[3] {
				out[m[1]] = m[2]
			}
		}
	}
	return out
}

func (r *Resolver) resolveMaven(c *Component) {
	spec := c.Declared
	switch {
	case spec == "":
		c.State = StateUnpinned
		c.Note = "no version in this POM: it is managed by a parent POM or a BOM this tool does not read"
		return
	case strings.EqualFold(spec, "LATEST") || strings.EqualFold(spec, "RELEASE"):
		c.State = StateUnpinned
		c.Note = "a moving meta-version"
		return
	}

	if m := pomPlaceholder.FindStringSubmatch(spec); m != nil {
		props, ok := r.pomProps[c.Manifest]
		if !ok {
			if src, found := r.file(c.Manifest); found {
				props = parsePOMProperties(src)
			}
			r.pomProps[c.Manifest] = props
		}
		value, found := props[m[1]]
		if !found || value == "" || strings.Contains(value, "${") {
			c.State = StateUnresolved
			c.Note = fmt.Sprintf("the version is the property %s, which this POM does not define "+
				"(a parent POM or a build profile sets it)", m[1])
			return
		}
		c.Note = fmt.Sprintf("the version is the property %s, defined in the same POM", m[1])
		spec = value
	} else if strings.Contains(spec, "${") {
		c.State = StateUnresolved
		c.Note = "the version is built from property placeholders"
		return
	}

	c.State, c.Version, c.Note = classifyMavenVersion(spec, c.Note)
}

// classifyMavenVersion reads a literal Maven or Gradle version requirement.
func classifyMavenVersion(spec, note string) (VersionState, string, string) {
	if strings.HasPrefix(spec, "[") || strings.HasPrefix(spec, "(") {
		// "[1.0]" is the one range syntax that is a pin: exactly 1.0.
		inner := strings.TrimSuffix(strings.TrimPrefix(spec, "["), "]")
		if strings.HasPrefix(spec, "[") && strings.HasSuffix(spec, "]") &&
			!strings.ContainsAny(inner, ",[]()") && inner != "" {
			return StatePinned, inner, note
		}
		return StateRange, "", "declared as a version range"
	}
	return StatePinned, spec, note
}

// ---------------------------------------------------------------- Gradle

func (r *Resolver) resolveGradle(c *Component) {
	spec := c.Declared
	switch {
	case spec == "":
		c.State = StateUnpinned
		c.Note = "no version in the build file: a platform, BOM or version catalog supplies it"
	case strings.Contains(spec, "$"):
		c.State = StateUnresolved
		c.Note = "the version is a build-script variable"
	case strings.HasSuffix(spec, "+") || strings.HasPrefix(spec, "latest."):
		c.State = StateRange
		c.Note = "declared as a dynamic version"
	default:
		c.State, c.Version, c.Note = classifyMavenVersion(spec, "")
	}

	// A Gradle lockfile overrides even a literal version: Gradle resolves
	// conflicts upward, so the version built can be higher than the one
	// written here.
	if v, lock := r.gradleLocked(c.Manifest, c.Name); v != "" {
		if c.State == StatePinned && v == c.Version {
			return
		}
		c.State, c.Version, c.VersionSource = StateLocked, v, lock
		c.Note = fmt.Sprintf("declared as %q; %s resolves it", spec, lock)
		r.lockfiles[lock] = true
		return
	}
	switch c.State {
	case StatePinned:
		c.Note = "no gradle.lockfile: Gradle resolves version conflicts upward, " +
			"so the version built may be higher than the one declared"
	case StateRange, StateUnpinned:
		c.Note += "; no gradle.lockfile entry pins it"
	}
}
