package supplychain

import (
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/analyze/deps"
)

// TestVersionStates walks every fixture manifest and checks the state, the
// version and the package URL of each declaration. The table IS the
// specification of "exactly as the manifest or lockfile states it".
func TestVersionStates(t *testing.T) {
	components := fixtureComponents(t)

	cases := []struct {
		name    string
		state   VersionState
		version string
		purl    string
		source  string
	}{
		// Go: go.mod states exact versions.
		{"github.com/spf13/cobra", StatePinned, "v1.8.0", "pkg:golang/github.com/spf13/cobra@v1.8.0", "go.mod:6"},
		{"golang.org/x/sys", StatePinned, "v0.1.0", "pkg:golang/golang.org/x/sys@v0.1.0", "go.mod:12"},
		// A replace to another module: the fork is what is built.
		{"github.com/fork/lib", StatePinned, "v1.2.0", "pkg:golang/github.com/fork/lib@v1.2.0", "go.mod:9"},
		// A replace to a local path: no published version is built at all.
		{"example.com/local", StateUnresolved, "", "pkg:golang/example.com/local", "go.mod:10"},

		// npm.
		{"express", StatePinned, "4.18.2", "pkg:npm/express@4.18.2", "web/package.json:5"},
		{"lodash", StateLocked, "4.17.20", "pkg:npm/lodash@4.17.20", "web/package-lock.json"},
		{"jest", StateLocked, "29.7.0", "pkg:npm/jest@29.7.0", "web/package-lock.json"},
		{"@scope/pkg", StateRange, "", "pkg:npm/%40scope/pkg", "web/package.json:6"},
		{"left-pad", StateUnpinned, "", "pkg:npm/left-pad", "web/package.json:7"},
		{"gitdep", StateUnresolved, "", "pkg:npm/gitdep", "web/package.json:8"},

		// PyPI: only "==" without a wildcard is a pin.
		{"requests", StatePinned, "2.25.0", "pkg:pypi/requests@2.25.0", "py/requirements.txt:1"},
		{"PyYAML", StateRange, "", "pkg:pypi/pyyaml", "py/requirements.txt:2"},
		{"jinja2", StatePinned, "2.11.2", "pkg:pypi/jinja2@2.11.2", "py/requirements.txt:3"},
		{"flask", StateUnpinned, "", "pkg:pypi/flask", "py/requirements.txt:4"},
		{"Django", StatePinned, "3.2.0", "pkg:pypi/django@3.2.0", "py/requirements.txt:6"},
		{"urllib3", StateLocked, "1.26.5", "pkg:pypi/urllib3@1.26.5", "svc/poetry.lock"},
		{"idna", StatePinned, "3.4", "pkg:pypi/idna@3.4", "svc/pyproject.toml:5"},

		// Cargo: a bare version is a caret RANGE, not a pin.
		{"smallvec", StateLocked, "1.6.0", "pkg:cargo/smallvec@1.6.0", "rs/Cargo.lock"},
		{"serde", StatePinned, "1.0.130", "pkg:cargo/serde@1.0.130", "rs/Cargo.toml:7"},
		// Two versions of time in Cargo.lock: which one this line resolved to
		// is not recorded, so it stays a range.
		{"time", StateRange, "", "pkg:cargo/time", "rs/Cargo.toml:8"},
		// `renamed = { package = "real-crate" }` is a dependency on real-crate.
		{"real-crate", StateLocked, "2.5.0", "pkg:cargo/real-crate@2.5.0", "rs/Cargo.lock"},

		// Maven.
		{"com.fasterxml.jackson.core:jackson-databind", StatePinned, "2.9.8",
			"pkg:maven/com.fasterxml.jackson.core/jackson-databind@2.9.8", "java/pom.xml:6"},
		{"org.apache.logging.log4j:log4j-core", StatePinned, "2.14.1",
			"pkg:maven/org.apache.logging.log4j/log4j-core@2.14.1", "java/pom.xml:11"},
		{"com.example.unused:widget", StateRange, "", "pkg:maven/com.example.unused/widget", "java/pom.xml:16"},
		{"org.springframework:spring-web", StateUnresolved, "", "pkg:maven/org.springframework/spring-web", "java/pom.xml:21"},
		{"junit:junit", StateUnpinned, "", "pkg:maven/junit/junit", "java/pom.xml:26"},

		// Gradle: the lockfile wins even over a literal version.
		{"com.squareup.okhttp3:okhttp", StateLocked, "4.9.3", "pkg:maven/com.squareup.okhttp3/okhttp@4.9.3", "android/gradle.lockfile"},
		{"com.google.code.gson:gson", StateUnresolved, "", "pkg:maven/com.google.code.gson/gson", "android/build.gradle:3"},
		{"org.example:dynamic", StateLocked, "1.7.2", "pkg:maven/org.example/dynamic@1.7.2", "android/gradle.lockfile"},
		{"org.mockito:mockito-core", StatePinned, "4.0.0", "pkg:maven/org.mockito/mockito-core@4.0.0", "android/build.gradle:5"},
	}

	for _, tc := range cases {
		c := componentNamed(t, components, tc.name)
		if c.State != tc.state {
			t.Errorf("%s: state = %q, want %q (note: %s)", tc.name, c.State, tc.state, c.Note)
		}
		if c.Version != tc.version {
			t.Errorf("%s: version = %q, want %q", tc.name, c.Version, tc.version)
		}
		if c.PURL != tc.purl {
			t.Errorf("%s: purl = %q, want %q", tc.name, c.PURL, tc.purl)
		}
		if c.VersionSource != tc.source {
			t.Errorf("%s: version source = %q, want %q", tc.name, c.VersionSource, tc.source)
		}
		// The invariant behind the whole SBOM marker: a version is present
		// exactly when the state says there is one.
		if c.State.Concrete() != (c.Version != "") {
			t.Errorf("%s: state %q with version %q — a concrete state must carry a version and no other may",
				tc.name, c.State, c.Version)
		}
		if !c.State.Concrete() && c.Note == "" {
			t.Errorf("%s: state %q carries no note saying why", tc.name, c.State)
		}
	}
}

// TestARangeIsNeverWrittenIntoAPackageURL: "pkg:npm/lodash@^4.17.20" names a
// release that does not exist. Every component without a concrete version must
// have a version-less purl.
func TestARangeIsNeverWrittenIntoAPackageURL(t *testing.T) {
	for _, c := range fixtureComponents(t) {
		if c.State.Concrete() {
			continue
		}
		if strings.Contains(c.PURL, "@") {
			t.Errorf("%s (%s, declared %q): purl %q carries a version", c.Name, c.State, c.Declared, c.PURL)
		}
		if strings.ContainsAny(c.PURL, "^~*<>= ") {
			t.Errorf("%s: purl %q contains range syntax", c.Name, c.PURL)
		}
	}
}

func TestDeclaredTextIsKeptVerbatim(t *testing.T) {
	components := fixtureComponents(t)
	for name, want := range map[string]string{
		"lodash":   "^4.17.20",
		"PyYAML":   ">=5.3",
		"urllib3":  ">=1.26,<2",
		"time":     "0.1",
		"jinja2":   "==2.11.2", // the environment marker is not part of the version
		"Django":   "==3.2.0",  // nor is a --hash option
		"left-pad": "*",
		"com.fasterxml.jackson.core:jackson-databind": "${jackson.version}",
		"com.example.unused:widget":                   "[1.0,2.0)",
	} {
		if got := componentNamed(t, components, name).Declared; got != want {
			t.Errorf("%s: declared = %q, want %q", name, got, want)
		}
	}
}

// TestGoReplaceChangesWhatIsBuilt: the manifest names one module and the build
// uses another. The component must describe what is built and remember what
// was declared, because source still imports the declared path.
func TestGoReplaceChangesWhatIsBuilt(t *testing.T) {
	components := fixtureComponents(t)
	fork := componentNamed(t, components, "github.com/fork/lib")
	if fork.DeclaredName != "github.com/old/lib" {
		t.Errorf("declared name = %q, want github.com/old/lib", fork.DeclaredName)
	}
	if importName(fork) != "github.com/old/lib" {
		t.Errorf("import name = %q; source imports the DECLARED path under a replace", importName(fork))
	}
	if !strings.Contains(fork.Note, "replaces") {
		t.Errorf("note does not mention the replacement: %q", fork.Note)
	}

	reps := parseGoReplaces("replace (\n\ta.b/c v1.0.0 => a.b/c v1.0.1 // pinned\n\tx.y/z => ../z\n)\nreplace q.r/s => q.r/t v2.0.0\n")
	if len(reps) != 3 {
		t.Fatalf("parsed %d replace directives, want 3: %+v", len(reps), reps)
	}
	if reps[0].oldVersion != "v1.0.0" || reps[0].newVersion != "v1.0.1" || reps[1].newVersion != "" || reps[2].newPath != "q.r/t" {
		t.Errorf("replace directives misread: %+v", reps)
	}
}

// TestWithoutAReaderNothingIsInvented: with no way to read lockfiles the
// resolver must fall back to what the manifest row says, never to a guess.
func TestWithoutAReaderNothingIsInvented(t *testing.T) {
	components := NewResolver(nil).ResolveAll(fixtureDeclared(t))
	for _, name := range []string{"lodash", "jest", "smallvec", "urllib3", "org.example:dynamic"} {
		c := componentNamed(t, components, name)
		if c.State.Concrete() {
			t.Errorf("%s: state %q with no lockfile readable; a range must stay a range", name, c.State)
		}
	}
	// The property placeholder cannot be resolved without the POM either.
	if c := componentNamed(t, components, "com.fasterxml.jackson.core:jackson-databind"); c.State != StateUnresolved {
		t.Errorf("jackson-databind: state %q with the POM unreadable, want unresolved", c.State)
	}
}

// TestALegacyBareVersionIsNotAPin: an index written before pyproject.toml
// operators were recorded holds "2.0" for both ">=2.0" and "==2.0". Reading
// that as a pin would turn a range into a version.
func TestALegacyBareVersionIsNotAPin(t *testing.T) {
	c := NewResolver(nil).Resolve(Declared{Name: "requests", Ecosystem: deps.EcosystemPyPI, Version: "2.0"})
	if c.State != StateUnresolved || c.Version != "" {
		t.Errorf("a bare PyPI version resolved to state %q version %q, want unresolved and no version", c.State, c.Version)
	}
}

func TestNPMSpecClassification(t *testing.T) {
	cases := map[string]VersionState{
		"1.2.3":                    StatePinned,
		"=1.2.3":                   StatePinned,
		"v1.2.3":                   StatePinned,
		"1.2.3-beta.1":             StatePinned,
		"^1.2.3":                   StateRange,
		"~1.2.3":                   StateRange,
		">=1.0.0 <2.0.0":           StateRange,
		"1.x":                      StateRange,
		"1.2":                      StateRange,
		"1.0.0 || 2.0.0":           StateRange,
		"":                         StateUnpinned,
		"*":                        StateUnpinned,
		"latest":                   StateUnpinned,
		"next":                     StateUnpinned,
		"git+https://x/y.git":      StateUnresolved,
		"github:user/repo":         StateUnresolved,
		"user/repo":                StateUnresolved,
		"file:../local":            StateUnresolved,
		"workspace:*":              StateUnresolved,
		"npm:other@1.0.0":          StateUnresolved,
		"https://example.com/x.gz": StateUnresolved,
	}
	for spec, want := range cases {
		c := NewResolver(nil).Resolve(Declared{Name: "p", Ecosystem: deps.EcosystemNPM, Version: spec})
		if c.State != want {
			t.Errorf("npm %q: state = %q, want %q", spec, c.State, want)
		}
	}
}

func TestYarnLockBothFormats(t *testing.T) {
	classic := "# yarn lockfile v1\n\n" +
		"\"@scope/pkg@~1.2.0\", \"@scope/pkg@^1.2.1\":\n  version \"1.2.9\"\n  resolved \"https://x\"\n\n" +
		"lodash@^4.17.20:\n  version \"4.17.21\"\n"
	berry := "__metadata:\n  version: 6\n\n" +
		"\"@scope/pkg@npm:~1.2.0, @scope/pkg@npm:^1.2.1\":\n  version: 1.2.9\n  resolution: \"@scope/pkg@npm:1.2.9\"\n\n" +
		"\"lodash@npm:^4.17.20\":\n  version: 4.17.21\n"
	for name, src := range map[string]string{"classic": classic, "berry": berry} {
		got := parseYarnLock(src)
		for key, want := range map[string]string{
			"@scope/pkg@~1.2.0": "1.2.9", "@scope/pkg@^1.2.1": "1.2.9", "lodash@^4.17.20": "4.17.21",
		} {
			if got[key] != want {
				t.Errorf("%s yarn.lock: %s = %q, want %q (parsed: %v)", name, key, got[key], want, got)
			}
		}
	}

	// And through the resolver: package.json beside a yarn.lock.
	files := map[string]string{"yarn.lock": classic}
	r := NewResolver(func(rel string) ([]byte, error) {
		if s, ok := files[rel]; ok {
			return []byte(s), nil
		}
		return nil, errIndexDown
	})
	c := r.Resolve(Declared{Name: "lodash", Ecosystem: deps.EcosystemNPM, Version: "^4.17.20", Manifest: "package.json", Line: 3})
	if c.State != StateLocked || c.Version != "4.17.21" || c.VersionSource != "yarn.lock" {
		t.Errorf("lodash via yarn.lock: %q %q from %q", c.State, c.Version, c.VersionSource)
	}
	if got := r.LockfilesRead(); len(got) != 1 || got[0] != "yarn.lock" {
		t.Errorf("LockfilesRead = %v, want [yarn.lock]", got)
	}
}

// TestWorkspaceLockfileAboveTheManifest: a workspace member's versions live in
// the lockfile at the workspace root, under the member's own path when a
// package is not hoisted.
func TestWorkspaceLockfileAboveTheManifest(t *testing.T) {
	files := map[string]string{
		"package-lock.json": `{"lockfileVersion": 3, "packages": {
			"node_modules/lodash": {"version": "4.17.21"},
			"packages/api/node_modules/lodash": {"version": "3.10.1"},
			"node_modules/react": {"version": "18.2.0"}}}`,
	}
	r := NewResolver(func(rel string) ([]byte, error) {
		if s, ok := files[rel]; ok {
			return []byte(s), nil
		}
		return nil, errIndexDown
	})
	lodash := r.Resolve(Declared{Name: "lodash", Ecosystem: deps.EcosystemNPM, Version: "^3.0.0", Manifest: "packages/api/package.json"})
	if lodash.Version != "3.10.1" {
		t.Errorf("member's own lodash = %q, want the nested 3.10.1, not the hoisted one", lodash.Version)
	}
	react := r.Resolve(Declared{Name: "react", Ecosystem: deps.EcosystemNPM, Version: "^18.0.0", Manifest: "packages/api/package.json"})
	if react.Version != "18.2.0" || react.VersionSource != "package-lock.json" {
		t.Errorf("hoisted react = %q from %q", react.Version, react.VersionSource)
	}
}

func TestPURLEncoding(t *testing.T) {
	cases := []struct {
		eco     deps.Ecosystem
		name    string
		version string
		want    string
	}{
		{deps.EcosystemGo, "github.com/BurntSushi/toml", "v1.3.2", "pkg:golang/github.com/BurntSushi/toml@v1.3.2"},
		{deps.EcosystemGo, "github.com/docker/docker", "v24.0.7+incompatible", "pkg:golang/github.com/docker/docker@v24.0.7%2Bincompatible"},
		{deps.EcosystemGo, "gopkg.in/yaml.v3", "v3.0.1", "pkg:golang/gopkg.in/yaml.v3@v3.0.1"},
		{deps.EcosystemNPM, "@babel/core", "7.23.0", "pkg:npm/%40babel/core@7.23.0"},
		{deps.EcosystemNPM, "React", "18.2.0", "pkg:npm/react@18.2.0"},
		{deps.EcosystemPyPI, "Flask_SQLAlchemy", "3.0.0", "pkg:pypi/flask-sqlalchemy@3.0.0"},
		{deps.EcosystemPyPI, "torch", "2.1.0+cu118", "pkg:pypi/torch@2.1.0%2Bcu118"},
		{deps.EcosystemCargo, "serde_json", "1.0.108", "pkg:cargo/serde_json@1.0.108"},
		{deps.EcosystemMaven, "org.apache.logging.log4j:log4j-core", "2.14.1", "pkg:maven/org.apache.logging.log4j/log4j-core@2.14.1"},
		{deps.EcosystemGradle, "com.google.guava:guava", "30.0-jre", "pkg:maven/com.google.guava/guava@30.0-jre"},
		{deps.EcosystemMaven, "no-colon", "1.0", ""},
		{deps.Ecosystem("nuget"), "Newtonsoft.Json", "13.0.1", ""},
	}
	for _, tc := range cases {
		if got := PURL(tc.eco, tc.name, tc.version); got != tc.want {
			t.Errorf("PURL(%s, %q, %q) = %q, want %q", tc.eco, tc.name, tc.version, got, tc.want)
		}
	}
}

// TestOlderAndOddLockfiles: lockfileVersion 1 has no "packages" map, and a
// lockfile can record something that is not a version at all.
func TestOlderAndOddLockfiles(t *testing.T) {
	files := map[string]string{
		"package-lock.json": `{"lockfileVersion": 1, "dependencies": {
			"lodash": {"version": "4.17.21"},
			"forked": {"version": "github:someone/forked#0123abc"}}}`,
	}
	r := NewResolver(func(rel string) ([]byte, error) {
		if s, ok := files[rel]; ok {
			return []byte(s), nil
		}
		return nil, errIndexDown
	})
	lodash := r.Resolve(Declared{Name: "lodash", Ecosystem: deps.EcosystemNPM, Version: "^4.17.0", Manifest: "package.json"})
	if lodash.State != StateLocked || lodash.Version != "4.17.21" {
		t.Errorf("lockfileVersion 1: lodash = %q %q, want locked 4.17.21", lodash.State, lodash.Version)
	}
	// A Git reference in the lockfile is not a release: the range stays a range.
	forked := r.Resolve(Declared{Name: "forked", Ecosystem: deps.EcosystemNPM, Version: "^1.0.0", Manifest: "package.json"})
	if forked.State.Concrete() {
		t.Errorf("forked = %q %q; a lockfile entry that is not a version must not become one", forked.State, forked.Version)
	}

	// A lockfile that is not valid JSON is the same as no lockfile.
	broken := NewResolver(func(string) ([]byte, error) { return []byte("{not json"), nil })
	c := broken.Resolve(Declared{Name: "lodash", Ecosystem: deps.EcosystemNPM, Version: "^4.17.0", Manifest: "package.json"})
	if c.State != StateRange {
		t.Errorf("with an unreadable lockfile lodash = %q, want range", c.State)
	}
}

func TestPyPIDirectReferenceIsNotAVersion(t *testing.T) {
	c := NewResolver(nil).Resolve(Declared{
		Name: "direct", Ecosystem: deps.EcosystemPyPI, Version: "@ https://example.invalid/direct-1.0.tar.gz",
	})
	if c.State != StateUnresolved || c.Version != "" {
		t.Errorf("a direct URL reference resolved to %q %q, want unresolved", c.State, c.Version)
	}
}
