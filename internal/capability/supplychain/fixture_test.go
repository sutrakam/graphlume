package supplychain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/analyze/deps"
)

// The fixture is a small polyglot repository: one manifest per ecosystem, the
// lockfiles that sit beside some of them, and the source that imports them.
//
// It is held as strings rather than as files under testdata so that the bytes
// are the same on every platform — a checkout with autocrlf would otherwise
// hand the Windows run different manifests from the ones reviewed here.
var fixtureProject = map[string]string{
	"go.mod": `module example.com/app

go 1.22

require (
	github.com/spf13/cobra v1.8.0
	golang.org/x/net v0.17.0
	gopkg.in/yaml.v3 v3.0.1
	github.com/old/lib v1.0.0
	example.com/local v0.1.0
	example.com/never v1.4.0
	golang.org/x/sys v0.1.0 // indirect
)

replace github.com/old/lib => github.com/fork/lib v1.2.0

replace example.com/local => ../local
`,
	"web/package.json": `{
  "name": "@example/web",
  "dependencies": {
    "lodash": "^4.17.20",
    "express": "4.18.2",
    "@scope/pkg": "~1.2.0",
    "left-pad": "*",
    "gitdep": "github:user/repo",
    "minimist": "^1.2.0"
  },
  "devDependencies": {
    "jest": "^29.0.0"
  }
}
`,
	"web/package-lock.json": `{
  "name": "@example/web",
  "lockfileVersion": 3,
  "packages": {
    "": { "name": "@example/web" },
    "node_modules/lodash": { "version": "4.17.20" },
    "node_modules/jest": { "version": "29.7.0", "dev": true },
    "node_modules/minimist": { "version": "1.2.5" },
    "node_modules/express": { "version": "4.18.2" }
  }
}
`,
	"py/requirements.txt": `requests==2.25.0
PyYAML>=5.3
jinja2==2.11.2 ; python_version >= "3.6"
flask
obscure-dist==1.0.0
Django==3.2.0 --hash=sha256:abc
`,
	"svc/pyproject.toml": `[project]
name = "svc"
dependencies = [
    "urllib3>=1.26,<2",
    "idna==3.4",
]
`,
	"svc/poetry.lock": `[[package]]
name = "urllib3"
version = "1.26.5"

[[package]]
name = "idna"
version = "3.4"
`,
	"rs/Cargo.toml": `[package]
name = "demo"
version = "0.1.0"

[dependencies]
smallvec = "1.6.0"
serde = { version = "=1.0.130", features = ["derive"] }
time = "0.1"
renamed = { package = "real-crate", version = "2" }
`,
	"rs/Cargo.lock": `version = 3

[[package]]
name = "smallvec"
version = "1.6.0"

[[package]]
name = "time"
version = "0.1.43"

[[package]]
name = "time"
version = "0.3.20"

[[package]]
name = "real-crate"
version = "2.5.0"
`,
	"java/pom.xml": `<project>
  <properties>
    <jackson.version>2.9.8</jackson.version>
  </properties>
  <dependencies>
    <dependency>
      <groupId>com.fasterxml.jackson.core</groupId>
      <artifactId>jackson-databind</artifactId>
      <version>${jackson.version}</version>
    </dependency>
    <dependency>
      <groupId>org.apache.logging.log4j</groupId>
      <artifactId>log4j-core</artifactId>
      <version>2.14.1</version>
    </dependency>
    <dependency>
      <groupId>com.example.unused</groupId>
      <artifactId>widget</artifactId>
      <version>[1.0,2.0)</version>
    </dependency>
    <dependency>
      <groupId>org.springframework</groupId>
      <artifactId>spring-web</artifactId>
      <version>${spring.version}</version>
    </dependency>
    <dependency>
      <groupId>junit</groupId>
      <artifactId>junit</artifactId>
      <scope>test</scope>
    </dependency>
  </dependencies>
</project>
`,
	"android/build.gradle": `dependencies {
    implementation 'com.squareup.okhttp3:okhttp:4.9.0'
    implementation "com.google.code.gson:gson:$gsonVersion"
    implementation 'org.example:dynamic:1.+'
    testImplementation 'org.mockito:mockito-core:4.0.0'
}
`,
	"android/gradle.lockfile": `# This is a Gradle generated file for dependency locking.
com.squareup.okhttp3:okhttp:4.9.3=compileClasspath,runtimeClasspath
org.example:dynamic:1.7.2=compileClasspath
empty=
`,
}

// fixtureReader serves the fixture as a FileReader.
func fixtureReader(rel string) ([]byte, error) {
	if src, ok := fixtureProject[rel]; ok {
		return []byte(src), nil
	}
	return nil, os.ErrNotExist
}

// fixtureDeclared parses every fixture manifest the way the indexer does, so
// the tests exercise the same Dependency rows the index would hold.
func fixtureDeclared(t *testing.T) []Declared {
	t.Helper()
	var paths []string
	for p := range fixtureProject {
		if deps.IsManifest(p) {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	var out []Declared
	for _, p := range paths {
		m, ok := deps.Parse(p, []byte(fixtureProject[p]))
		if !ok {
			t.Fatalf("fixture manifest %s did not parse", p)
		}
		for _, d := range m.Dependencies {
			out = append(out, Declared{
				Name: d.Name, Ecosystem: d.Ecosystem, Version: d.Version, Scope: string(d.Scope),
				Manifest: p, Line: d.Line, Indirect: d.Indirect,
			})
		}
	}
	return out
}

func fixtureComponents(t *testing.T) []Component {
	t.Helper()
	return NewResolver(fixtureReader).ResolveAll(fixtureDeclared(t))
}

func componentNamed(t *testing.T, components []Component, name string) Component {
	t.Helper()
	for _, c := range components {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no component named %q", name)
	return Component{}
}

// fakeIndex is an in-memory Index. It holds exactly what the registry would
// return for the fixture's source files, which keeps these tests off the
// database while still going through the interface production uses.
type fakeIndex struct {
	imports []ImportSite
	refs    []RefSite
	text    map[string]string
	files   []FileInfo
	// asked records the names References was called with, so a test can
	// assert a lookup was or was not made.
	asked []string
	fail  error
}

func (f *fakeIndex) ImportSites(context.Context) ([]ImportSite, error) {
	return f.imports, f.fail
}

func (f *fakeIndex) References(_ context.Context, names []string) ([]RefSite, error) {
	f.asked = append(f.asked, names...)
	var out []RefSite
	for _, r := range f.refs {
		for _, n := range names {
			if r.Name == n {
				out = append(out, r)
			}
		}
	}
	return out, f.fail
}

func (f *fakeIndex) LineText(_ context.Context, file string, line int) (string, bool) {
	src, ok := f.text[file]
	if !ok {
		return "", false
	}
	lines := strings.Split(src, "\n")
	if line < 1 || line > len(lines) {
		return "", false
	}
	return lines[line-1], true
}

func (f *fakeIndex) Files(context.Context) ([]FileInfo, error) { return f.files, f.fail }

// fixtureIndex is what indexing the fixture's source would record.
func fixtureIndex() *fakeIndex {
	jsSource := "import _ from \"lodash\";\n" +
		"const express = require(\"express\");\n" +
		"const dynamic = require(pick());\n"
	return &fakeIndex{
		files: []FileInfo{
			{Path: "cmd/main.go", Language: "go", ParseStatus: "ok"},
			{Path: "internal/conf/conf.go", Language: "go", ParseStatus: "ok"},
			{Path: "internal/broken.go", Language: "go", ParseStatus: "partial"},
			{Path: "web/src/app.js", Language: "javascript", ParseStatus: "ok"},
			{Path: "web/src/legacy.vue", Language: "vue", ParseStatus: "ok"},
			{Path: "py/app.py", Language: "python", ParseStatus: "ok"},
			{Path: "rs/src/main.rs", Language: "rust", ParseStatus: "ok"},
			{Path: "java/App.java", Language: "java", ParseStatus: "ok"},
			{Path: "go.mod", Language: "gomod", ParseStatus: "skipped"},
		},
		imports: []ImportSite{
			{File: "cmd/main.go", Line: 4, Path: "github.com/spf13/cobra", Language: "go"},
			{File: "cmd/main.go", Line: 5, Path: "golang.org/x/net/context", Language: "go"},
			{File: "internal/conf/conf.go", Line: 3, Path: "gopkg.in/yaml.v3", Language: "go"},
			{File: "internal/conf/conf.go", Line: 4, Path: "github.com/old/lib/sub", Language: "go"},
			{File: "web/src/app.js", Line: 1, Path: "lodash", Language: "javascript"},
			// Imported, and declared by no manifest: a transitive package used
			// directly, a Node core module, and a Go module go.mod lacks.
			{File: "web/src/app.js", Line: 5, Path: "qs/lib/parse", Language: "javascript"},
			{File: "web/src/app.js", Line: 6, Path: "url", Language: "javascript"},
			{File: "cmd/main.go", Line: 6, Path: "example.org/undeclared/pkg", Language: "go"},
			// A Python file importing a module that happens to share an npm
			// package's name must never count as importing the npm package.
			{File: "py/app.py", Line: 1, Path: "minimist", Language: "python"},
			{File: "py/app.py", Line: 2, Path: "requests", Language: "python"},
			{File: "py/app.py", Line: 3, Path: "yaml", Alias: "y", Language: "python"},
			{File: "py/app.py", Line: 4, Path: "jinja2.utils", Language: "python"},
			{File: "rs/src/main.rs", Line: 1, Path: "smallvec::SmallVec", Language: "rust"},
			{File: "rs/src/main.rs", Line: 2, Path: "time::now", Language: "rust"},
			{File: "java/App.java", Line: 2, Path: "com.fasterxml.jackson.databind.ObjectMapper", Language: "java"},
			{File: "java/App.java", Line: 3, Path: "org.apache.logging.log4j.LogManager", Language: "java"},
		},
		refs: []RefSite{
			{File: "cmd/main.go", Line: 12, Name: "ExactArgs", Receiver: "cobra", Kind: "reference.call"},
			// A local function that merely shares a symbol's name: no receiver.
			{File: "cmd/main.go", Line: 20, Name: "Parse", Receiver: "", Kind: "reference.call"},
			{File: "internal/conf/conf.go", Line: 9, Name: "Unmarshal", Receiver: "yaml", Kind: "reference.call"},
			{File: "web/src/app.js", Line: 2, Name: "require", Kind: "reference.call"},
			{File: "web/src/app.js", Line: 3, Name: "require", Kind: "reference.call"},
			{File: "py/app.py", Line: 8, Name: "get", Receiver: "requests", Kind: "reference.call"},
			{File: "py/app.py", Line: 9, Name: "load", Receiver: "y", Kind: "reference.call"},
			{File: "py/app.py", Line: 10, Name: "urlize", Receiver: "", Kind: "reference.call"},
		},
		text: map[string]string{"web/src/app.js": jsSource},
	}
}

// writeAdvisories writes OSV documents into a temporary directory and returns
// it. Each value is a complete JSON document.
func writeAdvisories(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// osv builds a one-package OSV record. events is the body of a range's
// "events" array; extra is spliced into the affected entry.
func osv(id, ecosystem, name, rangeType, events, extra string) string {
	if extra != "" {
		extra = ", " + extra
	}
	return `{"id": "` + id + `", "affected": [{"package": {"ecosystem": "` + ecosystem + `", "name": "` + name +
		`"}, "ranges": [{"type": "` + rangeType + `", "events": [` + events + `]}]` + extra + `}]}`
}

// analyzeFixture runs the whole report over the fixture with the given
// advisory documents.
func analyzeFixture(t *testing.T, idx *fakeIndex, advisories map[string]string, opt Options) Report {
	t.Helper()
	set, err := LoadAdvisories([]string{writeAdvisories(t, advisories)})
	if err != nil {
		t.Fatalf("LoadAdvisories: %v", err)
	}
	if opt.ImportLanguages == nil {
		opt.ImportLanguages = []string{"go", "javascript", "typescript", "tsx", "python", "rust", "java", "kotlin", "scala"}
	}
	rep, err := Analyze(context.Background(), fixtureComponents(t), set, idx, opt)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	return rep
}

func findingFor(t *testing.T, rep Report, advisory string) Finding {
	t.Helper()
	for _, f := range rep.Findings {
		if f.Advisory == advisory {
			return f
		}
	}
	t.Fatalf("no finding for advisory %s; findings: %d, not affected: %d", advisory, len(rep.Findings), len(rep.NotAffected))
	return Finding{}
}

var errIndexDown = errors.New("index unavailable")
