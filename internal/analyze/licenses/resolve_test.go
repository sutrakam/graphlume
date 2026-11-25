package licenses

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// place copies a testdata file to dst, creating directories.
func place(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", filepath.FromSlash(src)))
	if err != nil {
		t.Fatal(err)
	}
	write(t, dst, string(b))
}

func write(t *testing.T, dst, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture is a repository and a home directory with every cache, empty.
type fixture struct {
	root, home string
	env        Env
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	base := t.TempDir()
	f := fixture{root: filepath.Join(base, "repo"), home: filepath.Join(base, "home")}
	for _, d := range []string{f.root, f.home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.env = Env{
		Root: f.root, Home: f.home,
		GoModCache: filepath.Join(f.home, "go", "pkg", "mod"),
		CargoHome:  filepath.Join(f.home, ".cargo"),
		MavenRepo:  filepath.Join(f.home, ".m2", "repository"),
		GradleHome: filepath.Join(f.home, ".gradle"),
	}
	return f
}

func resolveOne(t *testing.T, env Env, d Dependency) Result {
	t.Helper()
	rs, _ := Resolve(context.Background(), env, []Dependency{d})
	return rs[0]
}

func expect(t *testing.T, what string, got Result, license, source, confidence, pathSuffix string) {
	t.Helper()
	if got.License != license || got.Source != source || got.Confidence != confidence || !strings.HasSuffix(got.Path, pathSuffix) {
		t.Errorf("%s: got %+v\nwant license=%s source=%s confidence=%s path=...%s", what, got, license, source, confidence, pathSuffix)
	}
}

func TestNPMMetadata(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.root, "web", "package.json"), `{"dependencies":{}}`)
	nm := filepath.Join(f.root, "web", "node_modules")
	place(t, "meta/npm-package.json", filepath.Join(nm, "left-pad", "package.json"))
	place(t, "meta/npm-package-old.json", filepath.Join(nm, "@scope", "old-style", "package.json"))
	place(t, "meta/npm-package-see.json", filepath.Join(nm, "see-file", "package.json"))
	place(t, "canonical/ISC", filepath.Join(nm, "see-file", "COPYING.txt"))
	write(t, filepath.Join(nm, "bare", "package.json"), `{"name":"bare"}`)
	place(t, "canonical/BSD-2-Clause", filepath.Join(nm, "bare", "LICENSE"))
	place(t, "meta/package-lock.json", filepath.Join(f.root, "package-lock.json"))

	m := "web/package.json"
	expect(t, "license field", resolveOne(t, f.env, Dependency{Name: "left-pad", Ecosystem: "npm", Manifest: m}),
		"WTFPL OR MIT", SourceDeclared, Exact, "web/node_modules/left-pad/package.json")
	expect(t, "licenses array", resolveOne(t, f.env, Dependency{Name: "@scope/old-style", Ecosystem: "npm", Manifest: m}),
		"Apache-2.0 OR MIT", SourceDeclared, High, "old-style/package.json")
	expect(t, "SEE LICENSE IN", resolveOne(t, f.env, Dependency{Name: "see-file", Ecosystem: "npm", Manifest: m}),
		"ISC", SourceFile, Exact, "see-file/COPYING.txt")
	expect(t, "licence file only", resolveOne(t, f.env, Dependency{Name: "bare", Ecosystem: "npm", Manifest: m}),
		"BSD-2-Clause", SourceFile, Exact, "bare/LICENSE")
	// Not installed: the root lockfile's top-level entry, not the nested one.
	expect(t, "package-lock", resolveOne(t, f.env, Dependency{Name: "lodash", Ecosystem: "npm", Manifest: m}),
		"MIT", SourceDeclared, Exact, "package-lock.json")
	got := resolveOne(t, f.env, Dependency{Name: "absent", Ecosystem: "npm", Manifest: m})
	if got.License != "unknown" || got.Source != SourceUnknown || got.Reason == "" || got.Category != Unknown {
		t.Errorf("absent: %+v", got)
	}
}

// A repository cannot point node_modules at a file outside itself.
func TestNPMSymlinkOutOfRepositoryIsNotFollowed(t *testing.T) {
	f := newFixture(t)
	outside := filepath.Join(f.home, "elsewhere")
	place(t, "meta/npm-package.json", filepath.Join(outside, "package.json"))
	if err := os.MkdirAll(filepath.Join(f.root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(f.root, "node_modules", "left-pad")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	got := resolveOne(t, f.env, Dependency{Name: "left-pad", Ecosystem: "npm", Manifest: "package.json"})
	if got.Source != SourceUnknown {
		t.Errorf("followed a link out of the repository: %+v", got)
	}
}

func TestPythonMetadata(t *testing.T) {
	f := newFixture(t)
	site := filepath.Join(f.root, ".venv", "lib", "python3.12", "site-packages")
	place(t, "meta/METADATA", filepath.Join(site, "requests-2.31.0.dist-info", "METADATA"))
	place(t, "meta/METADATA-expression", filepath.Join(site, "typing_extensions-4.12.0.dist-info", "METADATA"))
	place(t, "meta/METADATA-classifiers", filepath.Join(site, "six-1.16.0.dist-info", "METADATA"))
	place(t, "meta/METADATA-bsd", filepath.Join(site, "idna-3.6.dist-info", "METADATA"))
	place(t, "canonical/BSD-3-Clause", filepath.Join(site, "idna-3.6.dist-info", "LICENSE.md"))
	write(t, filepath.Join(f.root, "requirements.txt"), "requests\n")

	m := "requirements.txt"
	expect(t, "License:", resolveOne(t, f.env, Dependency{Name: "requests", Ecosystem: "pypi", Manifest: m}),
		"Apache-2.0", SourceDeclared, High, "requests-2.31.0.dist-info/METADATA")
	expect(t, "License-Expression", resolveOne(t, f.env, Dependency{Name: "typing-extensions", Ecosystem: "pypi", Manifest: m}),
		"PSF-2.0", SourceDeclared, Exact, "METADATA")
	expect(t, "classifier", resolveOne(t, f.env, Dependency{Name: "Six", Ecosystem: "pypi", Manifest: m}),
		"MIT", SourceDeclared, High, "six-1.16.0.dist-info/METADATA")
	// "BSD License" does not say which BSD; the installed licence file does.
	expect(t, "licence file", resolveOne(t, f.env, Dependency{Name: "idna", Ecosystem: "pypi", Manifest: m}),
		"BSD-3-Clause", SourceFile, Exact, "idna-3.6.dist-info/LICENSE.md")
	got := resolveOne(t, f.env, Dependency{Name: "flask", Ecosystem: "pypi", Manifest: m})
	if got.Source != SourceUnknown || !strings.Contains(got.Reason, "not installed") {
		t.Errorf("flask: %+v", got)
	}

	// No virtualenv at all.
	g := newFixture(t)
	got = resolveOne(t, g.env, Dependency{Name: "requests", Ecosystem: "pypi", Manifest: m})
	if got.Source != SourceUnknown || !strings.Contains(got.Reason, "virtualenv") {
		t.Errorf("no venv: %+v", got)
	}
}

func TestCargoRegistryCache(t *testing.T) {
	f := newFixture(t)
	src := filepath.Join(f.env.CargoHome, "registry", "src", "index.crates.io-6f17d22bba15001f")
	place(t, "meta/Cargo.toml", filepath.Join(src, "serde-1.0.197", "Cargo.toml"))
	place(t, "meta/Cargo.toml", filepath.Join(src, "serde-1.0.150", "Cargo.toml"))
	write(t, filepath.Join(src, "anyhow-1.0.80", "Cargo.toml"), "[package]\nname = \"anyhow\"\nversion = \"1.0.80\"\n")
	place(t, "canonical/MIT", filepath.Join(src, "anyhow-1.0.80", "LICENSE-MIT"))
	write(t, filepath.Join(f.root, "Cargo.toml"), "[dependencies]\nserde = \"1.0\"\n")

	// Without a lock file the newest matching copy is read, one grade down.
	got := resolveOne(t, f.env, Dependency{Name: "serde", Version: "1.0", Ecosystem: "cargo", Manifest: "Cargo.toml"})
	expect(t, "serde unlocked", got, "MIT OR Apache-2.0", SourceDeclared, High, "serde-1.0.197/Cargo.toml")
	if got.Reason == "" {
		t.Error("an unlocked version must say which copy was read")
	}

	place(t, "meta/Cargo.lock", filepath.Join(f.root, "Cargo.lock"))
	expect(t, "serde locked", resolveOne(t, f.env, Dependency{Name: "serde", Version: "1.0", Ecosystem: "cargo", Manifest: "Cargo.toml"}),
		"MIT OR Apache-2.0", SourceDeclared, Exact, "serde-1.0.197/Cargo.toml")
	expect(t, "licence file", resolveOne(t, f.env, Dependency{Name: "anyhow", Version: "1", Ecosystem: "cargo", Manifest: "Cargo.toml"}),
		"MIT", SourceFile, High, "anyhow-1.0.80/LICENSE-MIT")
}

func TestMavenPOM(t *testing.T) {
	f := newFixture(t)
	repo := f.env.MavenRepo
	place(t, "meta/guava.pom", filepath.Join(repo, "com", "google", "guava", "guava", "33.0.0-jre", "guava-33.0.0-jre.pom"))
	place(t, "meta/guava-parent.pom", filepath.Join(repo, "com", "google", "guava", "guava-parent", "33.0.0-jre", "guava-parent-33.0.0-jre.pom"))
	gradle := filepath.Join(f.env.GradleHome, "caches", "modules-2", "files-2.1", "org.example", "odd", "1.0", "abc123")
	place(t, "meta/odd.pom", filepath.Join(gradle, "odd-1.0.pom"))

	// The licence is inherited from the parent POM.
	expect(t, "guava", resolveOne(t, f.env, Dependency{Name: "com.google.guava:guava", Version: "33.0.0-jre", Ecosystem: "maven"}),
		"Apache-2.0", SourceDeclared, High, "guava-parent-33.0.0-jre.pom")
	// A version a BOM supplies: the only cached copy, one grade down.
	got := resolveOne(t, f.env, Dependency{Name: "com.google.guava:guava", Ecosystem: "gradle"})
	expect(t, "guava unversioned", got, "Apache-2.0", SourceDeclared, Medium, "guava-parent-33.0.0-jre.pom")
	got = resolveOne(t, f.env, Dependency{Name: "org.example:odd", Version: "1.0", Ecosystem: "gradle"})
	if got.License != "unknown" || got.Declared != "Acme Commercial Terms & Conditions" || got.Reason == "" {
		t.Errorf("odd: %+v", got)
	}
	if !strings.HasPrefix(got.Path, "~/.gradle/") {
		t.Errorf("a cache path must be shown home-relative: %q", got.Path)
	}
}

// End to end over a temporary Go module cache, with the case encoding, a
// vendored copy, a module with no licence file, and one not in the cache.
func TestGoModuleCacheEndToEnd(t *testing.T) {
	f := newFixture(t)
	mc := f.env.GoModCache
	place(t, "canonical/Apache-2.0", filepath.Join(mc, "github.com", "!burnt!sushi", "toml@v1.3.2", "COPYING"))
	place(t, "canonical/BSD-3-Clause", filepath.Join(mc, "golang.org", "x", "sys@v0.50.0", "LICENSE"))
	write(t, filepath.Join(mc, "example.com", "nolicense@v1.0.0", "go.mod"), "module example.com/nolicense\n")
	place(t, "canonical/MPL-2.0", filepath.Join(f.root, "vendor", "github.com", "hashicorp", "hcl", "LICENSE"))
	write(t, filepath.Join(f.root, "go.mod"), "module example.com/app\n")

	deps := []Dependency{
		{Name: "github.com/BurntSushi/toml", Version: "v1.3.2", Ecosystem: "go", Manifest: "go.mod"},
		{Name: "golang.org/x/sys", Version: "v0.50.0", Ecosystem: "go", Manifest: "go.mod"},
		{Name: "github.com/hashicorp/hcl", Version: "v1.0.0", Ecosystem: "go", Manifest: "go.mod"},
		{Name: "example.com/nolicense", Version: "v1.0.0", Ecosystem: "go", Manifest: "go.mod"},
		{Name: "example.com/missing", Version: "v2.0.0", Ecosystem: "go", Manifest: "go.mod"},
		{Name: "golang.org/x/sys", Version: "v0.50.0", Ecosystem: "go", Manifest: "tools/go.mod"},
		{Name: "thing", Ecosystem: "nuget"},
	}
	rs, st := Resolve(context.Background(), f.env, deps)
	if st.BudgetSpent {
		t.Error("budget spent on a tiny fixture")
	}
	expect(t, "toml", rs[0], "Apache-2.0", SourceFile, High, "~/go/pkg/mod/github.com/!burnt!sushi/toml@v1.3.2/COPYING")
	expect(t, "x/sys", rs[1], "BSD-3-Clause", SourceFile, Exact, "golang.org/x/sys@v0.50.0/LICENSE")
	expect(t, "vendored", rs[2], "MPL-2.0", SourceFile, High, "vendor/github.com/hashicorp/hcl/LICENSE")
	if rs[2].Path != "vendor/github.com/hashicorp/hcl/LICENSE" {
		t.Errorf("a repository path must be repository-relative: %q", rs[2].Path)
	}
	for i, wantReason := range map[int]string{3: "no LICENSE", 4: "not in the Go module cache", 6: "nuget"} {
		if rs[i].Source != SourceUnknown || !strings.Contains(rs[i].Reason, wantReason) {
			t.Errorf("row %d: %+v, want reason containing %q", i, rs[i], wantReason)
		}
	}
	if rs[5] != rs[1] {
		t.Errorf("same module from another manifest: %+v vs %+v", rs[5], rs[1])
	}

	s := Summarize(rs)
	if s.ByCategory[Permissive] != 3 || s.ByCategory[WeakCopyleft] != 1 || s.ByCategory[Unknown] != 3 ||
		s.BySource[SourceFile] != 4 || s.BySource[SourceUnknown] != 3 || s.BySource[SourceDeclared] != 0 {
		t.Errorf("summary = %+v", s)
	}
}

// With no cache anywhere, every licence is unknown with a reason, and nothing fails.
func TestNoCachesAnywhere(t *testing.T) {
	env := Env{Root: t.TempDir()}
	deps := []Dependency{
		{Name: "golang.org/x/sys", Version: "v0.50.0", Ecosystem: "go"},
		{Name: "react", Ecosystem: "npm", Manifest: "package.json"},
		{Name: "requests", Ecosystem: "pypi", Manifest: "requirements.txt"},
		{Name: "serde", Version: "1", Ecosystem: "cargo", Manifest: "Cargo.toml"},
		{Name: "a:b", Version: "1", Ecosystem: "maven"},
	}
	rs, _ := Resolve(context.Background(), env, deps)
	for i, r := range rs {
		if r.License != "unknown" || r.Source != SourceUnknown || r.Reason == "" {
			t.Errorf("%s: %+v", deps[i].Name, r)
		}
	}
}

func TestBudgetLeavesTheRestUnknown(t *testing.T) {
	env := Env{Root: t.TempDir(), Budget: time.Nanosecond}
	time.Sleep(time.Millisecond)
	rs, st := Resolve(context.Background(), env, []Dependency{{Name: "x", Version: "v1.0.0", Ecosystem: "go"}})
	if !st.BudgetSpent || !strings.Contains(rs[0].Reason, "time bound") {
		t.Errorf("stats=%+v result=%+v", st, rs[0])
	}
}

func TestEscapeModule(t *testing.T) {
	if got := escapeModule("github.com/BurntSushi/toml"); got != "github.com/!burnt!sushi/toml" {
		t.Error(got)
	}
}

// A module that lives in this repository (a Go replace by a local directory,
// an npm file: link) is first-party: no cache holds it, so its licence is the
// repository's own, read from the nearest licence file up to the root, and a
// repository with none says so instead of pointing at the module cache.
func TestFirstPartyModulesReadTheRepositoryLicence(t *testing.T) {
	f := newFixture(t)
	place(t, "canonical/MIT", filepath.Join(f.root, "LICENSE"))
	write(t, filepath.Join(f.root, "svc", "go.mod"), "module example.com/app/svc\n\n"+
		"require (\n\texample.com/app/svc/adapter v0.0.0\n\texample.com/app/lib v0.0.0\n)\n\n"+
		"replace example.com/app/svc/adapter => ./adapter // local\n"+
		"replace (\n\texample.com/app/lib v0.0.0 => ../lib\n)\n")
	write(t, filepath.Join(f.root, "svc", "adapter", "go.mod"), "module example.com/app/svc/adapter\n")
	write(t, filepath.Join(f.root, "lib", "go.mod"), "module example.com/app/lib\n")
	place(t, "canonical/Apache-2.0", filepath.Join(f.root, "lib", "LICENSE"))
	write(t, filepath.Join(f.root, "web", "package.json"), `{"dependencies":{"ui-kit":"file:../ui-kit"}}`)
	write(t, filepath.Join(f.root, "ui-kit", "package.json"), `{"name":"ui-kit"}`)

	rs, _ := Resolve(context.Background(), f.env, []Dependency{
		{Name: "example.com/app/svc/adapter", Version: "v0.0.0", Ecosystem: "go", Manifest: "svc/go.mod"},
		{Name: "example.com/app/lib", Version: "v0.0.0", Ecosystem: "go", Manifest: "svc/go.mod"},
		{Name: "ui-kit", Version: "file:../ui-kit", Ecosystem: "npm", Manifest: "web/package.json"},
		{Name: "example.com/elsewhere", Version: "v1.0.0", Ecosystem: "go", Manifest: "svc/go.mod"},
	})
	expect(t, "replaced module, repository LICENSE", rs[0], "MIT", SourceFirstParty, Exact, "LICENSE")
	if rs[0].Path != "LICENSE" {
		t.Errorf("path = %q, want the repository's LICENSE", rs[0].Path)
	}
	expect(t, "replaced module, its own LICENSE", rs[1], "Apache-2.0", SourceFirstParty, rs[1].Confidence, "lib/LICENSE")
	expect(t, "npm file: link", rs[2], "MIT", SourceFirstParty, Exact, "LICENSE")
	if rs[3].Source != SourceUnknown || !strings.Contains(rs[3].Reason, "Go module cache") {
		t.Errorf("a module that is not replaced still reads the cache: %+v", rs[3])
	}
	if s := Summarize(rs); s.BySource[SourceFirstParty] != 3 {
		t.Errorf("summary by source = %+v", s.BySource)
	}

	// No licence anywhere in the repository: first-party, unknown, with why.
	bare := newFixture(t)
	write(t, filepath.Join(bare.root, "go.mod"), "module example.com/b\n\nreplace example.com/b/x => ./x\n")
	write(t, filepath.Join(bare.root, "x", "go.mod"), "module example.com/b/x\n")
	got := resolveOne(t, bare.env, Dependency{Name: "example.com/b/x", Version: "v0.0.0", Ecosystem: "go", Manifest: "go.mod"})
	if got.Source != SourceFirstParty || got.License != "unknown" || !strings.Contains(got.Reason, "part of this repository") {
		t.Errorf("first-party module in an unlicensed repository: %+v", got)
	}
}
