package supplychain

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const anyVersion = `{"introduced": "0"}`

// TestTheThreeReachabilityStates is the feature in one test: three advisories
// against the fixture, one landing in each state.
func TestTheThreeReachabilityStates(t *testing.T) {
	rep := analyzeFixture(t, fixtureIndex(), map[string]string{
		// cobra is imported by cmd/main.go, which calls cobra.ExactArgs.
		"referenced.json": osv("T-REFERENCED", "Go", "github.com/spf13/cobra", "SEMVER", anyVersion,
			`"ecosystem_specific": {"imports": [{"path": "github.com/spf13/cobra", "symbols": ["ExactArgs"]}]}`),
		// yaml.v3 is imported; the advisory names a symbol nothing calls.
		"imported.json": osv("T-IMPORTED", "Go", "gopkg.in/yaml.v3", "SEMVER", anyVersion,
			`"ecosystem_specific": {"imports": [{"path": "gopkg.in/yaml.v3", "symbols": ["NewDecoder"]}]}`),
		// example.com/never is declared and imported by nothing.
		"notimported.json": osv("T-NOT-IMPORTED", "Go", "example.com/never", "SEMVER", anyVersion, ""),
	}, Options{})

	referenced := findingFor(t, rep, "T-REFERENCED")
	if referenced.Reachability.State != ReachReferenced {
		t.Fatalf("cobra: state = %q, want %q (%s)", referenced.Reachability.State, ReachReferenced, referenced.Reachability.Detail)
	}
	refs := referenced.Reachability.SymbolReferences
	if len(refs) != 1 || refs[0].File != "cmd/main.go" || refs[0].Line != 12 ||
		refs[0].Symbol != "github.com/spf13/cobra.ExactArgs" {
		t.Errorf("cobra: symbol references = %+v, want one at cmd/main.go:12", refs)
	}
	if got := referenced.Reachability.ImportingFiles; len(got) != 1 || got[0].File != "cmd/main.go" || got[0].Line != 4 {
		t.Errorf("cobra: importing files = %+v, want cmd/main.go:4", got)
	}

	imported := findingFor(t, rep, "T-IMPORTED")
	if imported.Reachability.State != ReachImported {
		t.Fatalf("yaml: state = %q, want %q", imported.Reachability.State, ReachImported)
	}
	if len(imported.Reachability.SymbolReferences) != 0 {
		t.Errorf("yaml: a reference was claimed: %+v", imported.Reachability.SymbolReferences)
	}
	if !strings.Contains(imported.Reachability.Detail, "not proof of absence") {
		t.Errorf("yaml: the detail does not say an unbound symbol is not a clearance: %s", imported.Reachability.Detail)
	}
	if imported.Reachability.ImportingFileCount != 1 {
		t.Errorf("yaml: importing file count = %d, want 1", imported.Reachability.ImportingFileCount)
	}

	notImported := findingFor(t, rep, "T-NOT-IMPORTED")
	if notImported.Reachability.State != ReachNotImported {
		t.Fatalf("never: state = %q, want %q", notImported.Reachability.State, ReachNotImported)
	}
	if notImported.Reachability.ImportingFileCount != 0 || len(notImported.Reachability.ImportingFiles) != 0 {
		t.Errorf("never: importing files reported for a package nothing imports")
	}
	if !strings.Contains(notImported.Reachability.Detail, "first-party indexed code only") {
		t.Errorf("never: the detail does not bound the claim to indexed first-party code: %s", notImported.Reachability.Detail)
	}

	s := rep.Summary
	if s.Affected != 3 || s.Referenced != 1 || s.Imported != 1 || s.NotImported != 1 || s.ImportUseUnknown != 0 {
		t.Errorf("summary = %+v, want 3 affected: 1 referenced, 1 imported, 1 not imported", s)
	}
	// Most reachable first.
	if rep.Findings[0].Advisory != "T-REFERENCED" || rep.Findings[2].Advisory != "T-NOT-IMPORTED" {
		t.Errorf("findings are not ordered by reachability: %s, %s, %s",
			rep.Findings[0].Advisory, rep.Findings[1].Advisory, rep.Findings[2].Advisory)
	}
}

// TestAReferenceNeedsItsQualifier: cmd/main.go calls a local Parse() and also
// imports cobra. An advisory naming cobra's Parse must not be "referenced" on
// the strength of a bare name.
func TestAReferenceNeedsItsQualifier(t *testing.T) {
	rep := analyzeFixture(t, fixtureIndex(), map[string]string{
		"a.json": osv("T-BARE", "Go", "github.com/spf13/cobra", "SEMVER", anyVersion,
			`"ecosystem_specific": {"imports": [{"path": "github.com/spf13/cobra", "symbols": ["Parse", "Command.Execute"]}]}`),
	}, Options{})
	f := findingFor(t, rep, "T-BARE")
	if f.Reachability.State != ReachImported {
		t.Errorf("state = %q, want %q: an unqualified Parse() is a local function, not cobra.Parse", f.Reachability.State, ReachImported)
	}
	if !strings.Contains(f.Reachability.Detail, "Command.Execute") || !strings.Contains(f.Reachability.Detail, "method") {
		t.Errorf("the detail does not say the method could not be bound: %s", f.Reachability.Detail)
	}
}

// TestGoAdvisoryNarrowedToAPackage: the module is imported, but only a package
// the advisory does not name. Importing golang.org/x/net/context is not
// importing golang.org/x/net/html.
func TestGoAdvisoryNarrowedToAPackage(t *testing.T) {
	rep := analyzeFixture(t, fixtureIndex(), map[string]string{
		"html.json": osv("T-HTML", "Go", "golang.org/x/net", "SEMVER", anyVersion,
			`"ecosystem_specific": {"imports": [{"path": "golang.org/x/net/html", "symbols": ["Parse"]}]}`),
		"whole.json": osv("T-WHOLE", "Go", "golang.org/x/net", "SEMVER", anyVersion, ""),
	}, Options{})

	narrowed := findingFor(t, rep, "T-HTML")
	if narrowed.Reachability.State != ReachNotImported {
		t.Errorf("narrowed: state = %q, want %q", narrowed.Reachability.State, ReachNotImported)
	}
	if !strings.Contains(narrowed.Reachability.Detail, "golang.org/x/net/html") ||
		!strings.Contains(narrowed.Reachability.Detail, "1 file(s) import other packages") {
		t.Errorf("narrowed: detail does not explain the package-level answer: %s", narrowed.Reachability.Detail)
	}
	// Without the narrowing, the same import is an import of the module.
	if whole := findingFor(t, rep, "T-WHOLE"); whole.Reachability.State != ReachImported {
		t.Errorf("whole module: state = %q, want %q", whole.Reachability.State, ReachImported)
	}
}

// TestIndirectDependencyIsNotClearedByItsOwnMarker: golang.org/x/sys is marked
// indirect, so nothing imports it — by design. The report must say that is
// expected and proves nothing about the dependencies that DO use it.
func TestIndirectDependencyIsNotClearedByItsOwnMarker(t *testing.T) {
	rep := analyzeFixture(t, fixtureIndex(), map[string]string{
		"sys.json": osv("T-INDIRECT", "Go", "golang.org/x/sys", "SEMVER", anyVersion, ""),
	}, Options{})
	f := findingFor(t, rep, "T-INDIRECT")
	if f.Reachability.State != ReachNotImported {
		t.Fatalf("state = %q, want %q", f.Reachability.State, ReachNotImported)
	}
	if !strings.Contains(f.Reachability.Detail, "indirect") || !strings.Contains(f.Reachability.Detail, "not visible here") {
		t.Errorf("detail does not say why an indirect dependency has no importer: %s", f.Reachability.Detail)
	}
}

// TestReplacedModuleIsMatchedByTheNameSourceImports: go.mod replaces
// github.com/old/lib with a fork. Source still imports the old path, and an
// advisory about the OLD module cannot be compared against the fork's version.
func TestReplacedModuleIsMatchedByTheNameSourceImports(t *testing.T) {
	rep := analyzeFixture(t, fixtureIndex(), map[string]string{
		"fork.json": osv("T-FORK", "Go", "github.com/fork/lib", "SEMVER", `{"introduced": "0"}, {"fixed": "1.3.0"}`, ""),
		"old.json":  osv("T-OLD", "Go", "github.com/old/lib", "SEMVER", `{"introduced": "0"}, {"fixed": "0.5.0"}`, ""),
	}, Options{})

	fork := findingFor(t, rep, "T-FORK")
	if fork.Status != MatchAffected || fork.Package.Version != "v1.2.0" {
		t.Errorf("fork: status %q at %q, want affected at v1.2.0", fork.Status, fork.Package.Version)
	}
	if fork.Reachability.State != ReachImported || fork.Reachability.ImportingFiles[0].Path != "github.com/old/lib/sub" {
		t.Errorf("fork: reachability %q via %+v; the import of the declared path is the import of the fork",
			fork.Reachability.State, fork.Reachability.ImportingFiles)
	}
	// The declared v1.0.0 is past the old module's fix, but v1.0.0 of the old
	// module is not what is built. That must not read as "not affected".
	old := findingFor(t, rep, "T-OLD")
	if old.Status != MatchUnknown {
		t.Errorf("old: status %q, want %q", old.Status, MatchUnknown)
	}
}

// TestCommonJSRequireIsAnImport: the index records require("express") as a
// call, not an import. Without reading the argument back, every CommonJS
// dependency would be "not imported".
func TestCommonJSRequireIsAnImport(t *testing.T) {
	rep := analyzeFixture(t, fixtureIndex(), map[string]string{
		"express.json":  osv("T-EXPRESS", "npm", "express", "SEMVER", anyVersion, ""),
		"lodash.json":   osv("T-LODASH", "npm", "lodash", "SEMVER", anyVersion, ""),
		"jest.json":     osv("T-JEST", "npm", "jest", "SEMVER", anyVersion, ""),
		"minimist.json": osv("T-MINIMIST", "npm", "minimist", "SEMVER", anyVersion, ""),
	}, Options{})

	express := findingFor(t, rep, "T-EXPRESS")
	if express.Reachability.State != ReachImported {
		t.Fatalf("express: state = %q, want %q", express.Reachability.State, ReachImported)
	}
	if site := express.Reachability.ImportingFiles[0]; site.Via != "require" || site.File != "web/src/app.js" || site.Line != 2 {
		t.Errorf("express: importing site = %+v, want web/src/app.js:2 via require", site)
	}
	if lodash := findingFor(t, rep, "T-LODASH"); lodash.Reachability.State != ReachImported {
		t.Errorf("lodash (ES import): state = %q", lodash.Reachability.State)
	}
	if jest := findingFor(t, rep, "T-JEST"); jest.Reachability.State != ReachNotImported {
		t.Errorf("jest: state = %q, want %q", jest.Reachability.State, ReachNotImported)
	}
	// py/app.py imports a module called "minimist". That is Python.
	if minimist := findingFor(t, rep, "T-MINIMIST"); minimist.Reachability.State != ReachNotImported {
		t.Errorf("minimist: state = %q; an import in a Python file is not evidence about an npm package",
			minimist.Reachability.State)
	}

	// require(pick()) has no literal argument: it is an import this report
	// cannot see, and the coverage must say so.
	var npm EcosystemCoverage
	for _, ec := range rep.Coverage {
		if ec.Ecosystem == "npm" {
			npm = ec
		}
	}
	notes := strings.Join(npm.Notes, " | ")
	if !strings.Contains(notes, "1 require() call(s) have an argument this report could not read") {
		t.Errorf("npm coverage does not report the unreadable require(): %s", notes)
	}
	if !strings.Contains(notes, "dynamic import() is not recorded") {
		t.Errorf("npm coverage does not mention dynamic import(): %s", notes)
	}
	// The .vue file is in a language the test's index records no imports for.
	if !strings.Contains(notes, "vue file(s) are indexed but this index records no imports") {
		t.Errorf("npm coverage does not flag a language with no import binding: %s", notes)
	}
}

// TestHeuristicEcosystemsNeverClaimNotImported: where a package name does not
// determine an import path, "nothing matched" must be "unknown".
func TestHeuristicEcosystemsNeverClaimNotImported(t *testing.T) {
	rep := analyzeFixture(t, fixtureIndex(), map[string]string{
		// Maven: no import under com.example.unused.
		"widget.json": osv("T-MAVEN-NONE", "Maven", "com.example.unused:widget", "ECOSYSTEM", anyVersion, ""),
		// Cargo: serde is declared; no `use serde`. It may be used by path.
		"serde.json": osv("T-CARGO-NONE", "crates.io", "serde", "SEMVER", anyVersion, ""),
		// PyPI: a distribution whose module name is not known.
		"obscure.json": osv("T-PYPI-NONE", "PyPI", "obscure-dist", "ECOSYSTEM", anyVersion, ""),
		// PyPI: flask's module IS known, and nothing imports it.
		"flask.json": osv("T-PYPI-KNOWN", "PyPI", "Flask", "ECOSYSTEM", anyVersion, ""),
		// Gradle dependency, Maven advisory.
		"okhttp.json": osv("T-GRADLE-NONE", "Maven", "com.squareup.okhttp3:okhttp", "ECOSYSTEM", anyVersion, ""),
	}, Options{})

	for _, id := range []string{"T-MAVEN-NONE", "T-CARGO-NONE", "T-PYPI-NONE", "T-GRADLE-NONE"} {
		f := findingFor(t, rep, id)
		if f.Reachability.State != ReachUnknown {
			t.Errorf("%s (%s): state = %q, want %q", id, f.Package.Name, f.Reachability.State, ReachUnknown)
		}
		if f.Reachability.ImportMapping != "heuristic" {
			t.Errorf("%s: import mapping = %q, want heuristic", id, f.Reachability.ImportMapping)
		}
		if !strings.Contains(f.Reachability.Detail, "shows nothing") {
			t.Errorf("%s: detail does not say the absence of a match shows nothing: %s", id, f.Reachability.Detail)
		}
	}
	if f := findingFor(t, rep, "T-PYPI-KNOWN"); f.Reachability.State != ReachNotImported {
		t.Errorf("flask: state = %q, want %q — its module name is known for certain", f.Reachability.State, ReachNotImported)
	}
}

func TestImportsUnderADifferentName(t *testing.T) {
	rep := analyzeFixture(t, fixtureIndex(), map[string]string{
		// PyYAML is imported as yaml. It is declared as a range, so the
		// version is uncompared — and the reachability is still worked out.
		"yaml.json": osv("T-PYYAML", "PyPI", "pyyaml", "ECOSYSTEM", `{"introduced": "0"}, {"fixed": "5.4"}`,
			`"ecosystem_specific": {"affected_functions": ["yaml.load", "yaml.full_load"]}`),
		// jackson-databind's package is not under its groupId.
		"jackson.json": osv("T-JACKSON", "Maven", "com.fasterxml.jackson.core:jackson-databind", "ECOSYSTEM",
			`{"introduced": "2.9.0"}, {"fixed": "2.9.10"}`, ""),
		// log4j-core is matched on the groupId prefix.
		"log4j.json": osv("T-LOG4J", "Maven", "org.apache.logging.log4j:log4j-core", "ECOSYSTEM",
			`{"introduced": "2.0-beta9"}, {"fixed": "2.15.0"}`, ""),
	}, Options{})

	yaml := findingFor(t, rep, "T-PYYAML")
	if yaml.Status != MatchUnknown {
		t.Errorf("PyYAML >=5.3 with no lockfile: status = %q, want %q", yaml.Status, MatchUnknown)
	}
	if yaml.Reachability.State != ReachReferenced {
		t.Fatalf("PyYAML: state = %q, want %q (%s)", yaml.Reachability.State, ReachReferenced, yaml.Reachability.Detail)
	}
	if ref := yaml.Reachability.SymbolReferences[0]; ref.Symbol != "yaml.load" || ref.Line != 9 {
		t.Errorf("PyYAML: reference = %+v, want yaml.load at line 9 (called through the alias y)", ref)
	}

	jackson := findingFor(t, rep, "T-JACKSON")
	if jackson.Status != MatchAffected || jackson.Package.Version != "2.9.8" {
		t.Errorf("jackson: %q at %q, want affected at the property-resolved 2.9.8", jackson.Status, jackson.Package.Version)
	}
	if jackson.Reachability.State != ReachImported {
		t.Errorf("jackson: state = %q, want %q", jackson.Reachability.State, ReachImported)
	}
	if log4j := findingFor(t, rep, "T-LOG4J"); log4j.Reachability.State != ReachImported ||
		!strings.Contains(log4j.Reachability.Detail, "heuristic") {
		t.Errorf("log4j: state %q, detail %q; a groupId-prefix match must be labelled a heuristic",
			log4j.Reachability.State, log4j.Reachability.Detail)
	}
}

func TestPythonAndRustSymbolBinding(t *testing.T) {
	rep := analyzeFixture(t, fixtureIndex(), map[string]string{
		// from jinja2.utils import urlize ... urlize(x)
		"jinja.json": osv("T-JINJA", "PyPI", "Jinja2", "ECOSYSTEM", `{"introduced": "0"}, {"fixed": "2.11.3"}`,
			`"ecosystem_specific": {"affected_functions": ["jinja2.utils.urlize"]}`),
		// import requests ... requests.get(u) — but the advisory names a method.
		"requests.json": osv("T-REQUESTS", "PyPI", "requests", "ECOSYSTEM", `{"introduced": "0"}, {"fixed": "2.31.0"}`,
			`"ecosystem_specific": {"affected_functions": ["requests.sessions.Session.rebuild_proxies"]}`),
		// use smallvec::SmallVec; — the advisory names a method on the type.
		"smallvec.json": osv("T-SMALLVEC", "crates.io", "smallvec", "SEMVER", `{"introduced": "1.0.0"}, {"fixed": "1.6.1"}`,
			`"ecosystem_specific": {"affects": {"functions": ["smallvec::SmallVec::insert_many"]}}`),
	}, Options{})

	jinja := findingFor(t, rep, "T-JINJA")
	if jinja.Status != MatchAffected || jinja.Reachability.State != ReachReferenced {
		t.Errorf("jinja2: %q / %q, want affected / referenced (%s)", jinja.Status, jinja.Reachability.State, jinja.Reachability.Detail)
	}
	if requests := findingFor(t, rep, "T-REQUESTS"); requests.Reachability.State != ReachImported {
		t.Errorf("requests: state = %q, want %q — a method on an object is not bound", requests.Reachability.State, ReachImported)
	}
	smallvec := findingFor(t, rep, "T-SMALLVEC")
	if smallvec.Status != MatchAffected || smallvec.Package.VersionSource != "rs/Cargo.lock" {
		t.Errorf("smallvec: %q from %q, want affected at the Cargo.lock version", smallvec.Status, smallvec.Package.VersionSource)
	}
	if smallvec.Reachability.State != ReachImported {
		t.Errorf("smallvec: state = %q, want %q", smallvec.Reachability.State, ReachImported)
	}

	// A `use` of the affected function itself IS bound.
	idx := fixtureIndex()
	rep = analyzeFixture(t, idx, map[string]string{
		"time.json": osv("T-TIME", "crates.io", "time", "SEMVER", anyVersion,
			`"ecosystem_specific": {"affects": {"functions": ["time::now"]}}`),
	}, Options{})
	if f := findingFor(t, rep, "T-TIME"); f.Reachability.State != ReachReferenced {
		t.Errorf("time: state = %q, want %q (use time::now)", f.Reachability.State, ReachReferenced)
	}
}

func TestGoQualifierGuesses(t *testing.T) {
	cases := []struct {
		path, alias string
		want        []string
		dot         bool
	}{
		{"github.com/spf13/cobra", "", []string{"cobra"}, false},
		{"gopkg.in/yaml.v3", "", []string{"yaml.v3", "yaml"}, false},
		{"github.com/go-git/go-git/v5", "", []string{"go-git", "git"}, false},
		{"github.com/mattn/go-isatty", "", []string{"go-isatty", "isatty"}, false},
		{"github.com/x/y", "z", []string{"z"}, false},
		{"github.com/x/y", ".", nil, true},
		{"github.com/x/y", "_", nil, false},
	}
	for _, tc := range cases {
		got, dot := goQualifiers(tc.path, tc.alias)
		if dot != tc.dot {
			t.Errorf("goQualifiers(%q, %q): dot = %v, want %v", tc.path, tc.alias, dot, tc.dot)
		}
		for _, want := range tc.want {
			if !containsString(got, want) {
				t.Errorf("goQualifiers(%q, %q) = %v, missing %q", tc.path, tc.alias, got, want)
			}
		}
	}
}

// TestCoverageStatesWhatTheIndexCouldNotSee: every report says which languages
// record imports and which source files were not fully parsed.
func TestCoverageStatesWhatTheIndexCouldNotSee(t *testing.T) {
	rep := analyzeFixture(t, fixtureIndex(), map[string]string{
		"a.json": osv("T-A", "Go", "example.com/never", "SEMVER", anyVersion, ""),
	}, Options{})

	byEco := map[string]EcosystemCoverage{}
	for _, ec := range rep.Coverage {
		byEco[string(ec.Ecosystem)] = ec
	}
	for _, eco := range []string{"go", "npm", "pypi", "cargo", "maven", "gradle"} {
		ec, ok := byEco[eco]
		if !ok {
			t.Errorf("no coverage entry for %s, which has declared dependencies", eco)
			continue
		}
		if ec.VersionScheme == "" || ec.NotImportedIsClaimed == "" || ec.SymbolBinding == "" {
			t.Errorf("%s: coverage is missing a statement: %+v", eco, ec)
		}
	}

	goCov := byEco["go"]
	if goCov.FilesNotParsedTotal != 1 || len(goCov.FilesNotParsed) != 1 || goCov.FilesNotParsed[0].Path != "internal/broken.go" {
		t.Errorf("go: files not fully parsed = %+v (total %d), want internal/broken.go",
			goCov.FilesNotParsed, goCov.FilesNotParsedTotal)
	}
	if len(goCov.Languages) != 1 || !goCov.Languages[0].BindsImports || goCov.Languages[0].Files != 3 || goCov.Languages[0].NotParsed != 1 {
		t.Errorf("go: language coverage = %+v", goCov.Languages)
	}
	if goCov.Dependencies != 7 || goCov.Concrete != 6 {
		t.Errorf("go: %d declared, %d concrete; want 7 and 6 (one is replaced by a local path)", goCov.Dependencies, goCov.Concrete)
	}

	for _, want := range []string{DeclaredOnlyCaveat, FirstPartyCaveat, NoVerdictCaveat} {
		found := false
		for _, c := range rep.Caveats {
			if c == want {
				found = true
			}
		}
		if !found {
			t.Errorf("the report is missing a mandatory caveat: %q", want)
		}
	}
}

func TestIgnoreIsReportedNotSilent(t *testing.T) {
	rep := analyzeFixture(t, fixtureIndex(), map[string]string{
		"a.json": `{"id": "T-A", "aliases": ["CVE-2026-0001"], "affected": [{"package": {"ecosystem": "Go", "name": "example.com/never"},
			"ranges": [{"type": "SEMVER", "events": [{"introduced": "0"}]}]}]}`,
		"b.json": osv("T-B", "Go", "example.com/never", "SEMVER", anyVersion, ""),
	}, Options{Ignore: []string{"CVE-2026-0001"}})

	if len(rep.Findings) != 1 || rep.Findings[0].Advisory != "T-B" {
		t.Errorf("findings = %d; ignoring by alias should leave only T-B", len(rep.Findings))
	}
	if len(rep.Advisories.Ignored) != 1 || rep.Advisories.Ignored[0] != "T-A" {
		t.Errorf("ignored = %v, want [T-A]: an ignored advisory must be listed", rep.Advisories.Ignored)
	}
}

func TestGate(t *testing.T) {
	rep := analyzeFixture(t, fixtureIndex(), map[string]string{
		"referenced.json": osv("T-REFERENCED", "Go", "github.com/spf13/cobra", "SEMVER", anyVersion,
			`"ecosystem_specific": {"imports": [{"path": "github.com/spf13/cobra", "symbols": ["ExactArgs"]}]}`),
		"notimported.json": osv("T-NOT-IMPORTED", "Go", "example.com/never", "SEMVER", anyVersion, ""),
		"cleared.json":     osv("T-CLEARED", "Go", "gopkg.in/yaml.v3", "SEMVER", `{"introduced": "0"}, {"fixed": "3.0.0"}`, ""),
	}, Options{})
	for _, level := range []GateLevel{GateReferenced, GateImported, GateAffected} {
		if len(rep.Gate(level)) == 0 {
			t.Errorf("--fail-on %s passed with a referenced, affected dependency", level)
		}
	}

	// Only a not-imported finding: fails "affected", passes the narrower two.
	notImported := analyzeFixture(t, fixtureIndex(), map[string]string{
		"notimported.json": osv("T-NOT-IMPORTED", "Go", "example.com/never", "SEMVER", anyVersion, ""),
	}, Options{})
	if len(notImported.Gate(GateAffected)) == 0 {
		t.Error("--fail-on affected passed with an affected dependency")
	}
	if r := notImported.Gate(GateImported); len(r) != 0 {
		t.Errorf("--fail-on imported failed on a dependency nothing imports: %v", r)
	}

	// A version that could not be compared fails a gate as an affected one
	// does: lodash would be cleared at 4.17.20 by this advisory, but
	// @scope/pkg is a range with no lockfile entry.
	uncompared := analyzeFixture(t, fixtureIndex(), map[string]string{
		"scope.json": osv("T-RANGE", "npm", "@scope/pkg", "SEMVER", `{"introduced": "0"}, {"fixed": "0.0.1"}`, ""),
	}, Options{})
	if f := findingFor(t, uncompared, "T-RANGE"); f.Status != MatchUnknown {
		t.Fatalf("@scope/pkg: status = %q, want %q", f.Status, MatchUnknown)
	}
	if r := uncompared.Gate(GateAffected); len(r) == 0 || !strings.Contains(strings.Join(r, " "), "could not be compared") {
		t.Errorf("--fail-on affected passed (or gave no reason) with an uncompared version: %v", r)
	}

	// An undeterminable import ranks with an import: it must not slip past
	// --fail-on imported.
	unknown := analyzeFixture(t, fixtureIndex(), map[string]string{
		"serde.json": osv("T-CARGO", "crates.io", "serde", "SEMVER", anyVersion, ""),
	}, Options{})
	if len(unknown.Gate(GateImported)) == 0 {
		t.Error("--fail-on imported passed on a dependency whose import could not be determined")
	}
	if r := unknown.Gate(GateReferenced); len(r) != 0 {
		t.Errorf("--fail-on referenced failed with nothing referenced: %v", r)
	}

	// Nothing found at all: every level passes.
	clean := analyzeFixture(t, fixtureIndex(), map[string]string{
		"cleared.json": osv("T-CLEARED", "Go", "gopkg.in/yaml.v3", "SEMVER", `{"introduced": "0"}, {"fixed": "3.0.0"}`, ""),
	}, Options{})
	if len(clean.Findings) != 0 || len(clean.NotAffected) != 1 {
		t.Fatalf("clean: %d findings, %d not affected; want 0 and 1", len(clean.Findings), len(clean.NotAffected))
	}
	for _, level := range []GateLevel{GateReferenced, GateImported, GateAffected} {
		if r := clean.Gate(level); len(r) != 0 {
			t.Errorf("--fail-on %s failed on a clean report: %v", level, r)
		}
	}

	// An unreadable advisory file fails every level.
	clean.Advisories.Unreadable = []LoadProblem{{Path: "x.json", Reason: "truncated"}}
	for _, level := range []GateLevel{GateReferenced, GateImported, GateAffected} {
		if len(clean.Gate(level)) == 0 {
			t.Errorf("--fail-on %s passed with an advisory file that could not be read", level)
		}
	}

	if _, ok := ParseGateLevel("high"); ok {
		t.Error("ParseGateLevel accepted a blindspot severity; the levels here are reachability states")
	}
}

func TestIndexErrorsAreReturnedNotSwallowed(t *testing.T) {
	idx := fixtureIndex()
	idx.fail = errIndexDown
	set, err := LoadAdvisories([]string{writeAdvisories(t, map[string]string{
		"a.json": osv("T-A", "Go", "example.com/never", "SEMVER", anyVersion, ""),
	})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Analyze(context.Background(), fixtureComponents(t), set, idx, Options{}); !errors.Is(err, errIndexDown) {
		t.Errorf("Analyze returned %v; an index failure must not produce a report", err)
	}
}

// TestAnUndeclaredPackageThatIsImportedIsNotDropped: an advisory names a
// package no manifest declares. Usually that ends the matter, but source can
// import a package nobody declared — a transitive dependency used directly.
// There is no version to compare, and the import is still a fact worth saying.
func TestAnUndeclaredPackageThatIsImportedIsNotDropped(t *testing.T) {
	rep := analyzeFixture(t, fixtureIndex(), map[string]string{
		"qs.json":  osv("T-QS", "npm", "qs", "SEMVER", `{"introduced": "0"}, {"fixed": "6.10.3"}`, ""),
		"mod.json": osv("T-GOMOD", "Go", "example.org/undeclared", "SEMVER", anyVersion, ""),
		// `import "url"` is Node's own module, not the npm package "url".
		"url.json": osv("T-URL", "npm", "url", "SEMVER", anyVersion, ""),
		// Neither declared nor imported: nothing to say beyond the count.
		"web.json": osv("T-WEB", "Go", "example.org/webframework", "SEMVER", anyVersion, ""),
		// An undeclared PyPI package: the name does not determine the import.
		"py.json": osv("T-PY", "PyPI", "minimist", "ECOSYSTEM", anyVersion, ""),
	}, Options{})

	for id, file := range map[string]string{"T-QS": "web/src/app.js", "T-GOMOD": "cmd/main.go"} {
		f := findingFor(t, rep, id)
		if f.Status != MatchUnknown {
			t.Errorf("%s: status = %q, want %q: there is no declared version", id, f.Status, MatchUnknown)
		}
		if f.Reachability.State != ReachImported || f.Reachability.ImportingFiles[0].File != file {
			t.Errorf("%s: reachability = %q via %+v, want imported by %s", id, f.Reachability.State, f.Reachability.ImportingFiles, file)
		}
		if f.Package.Manifest != "" || f.Package.Version != "" || !strings.Contains(f.Reason, "no manifest declares") {
			t.Errorf("%s: package = %+v, reason = %q", id, f.Package, f.Reason)
		}
	}
	if len(rep.Findings) != 2 {
		t.Errorf("%d findings, want only the two undeclared imports", len(rep.Findings))
	}
	if rep.Summary.AdvisoriesNamingNothing != 3 {
		t.Errorf("advisories naming nothing = %d, want 3 (url, the web framework, and the PyPI one)", rep.Summary.AdvisoriesNamingNothing)
	}
	// An import nobody declared, named by an advisory, must stop a gate set
	// at "imported".
	if len(rep.Gate(GateImported)) == 0 {
		t.Error("--fail-on imported passed with an undeclared package imported and named by an advisory")
	}
}

// TestADeclaredGoModuleOwnsItsImports: golang.org/x/net is declared, so an
// advisory naming the undeclared path "golang.org/x" must not pick up
// x/net's imports as its own.
func TestADeclaredGoModuleOwnsItsImports(t *testing.T) {
	rep := analyzeFixture(t, fixtureIndex(), map[string]string{
		"x.json": osv("T-PARENT", "Go", "golang.org/x", "SEMVER", anyVersion, ""),
	}, Options{})
	if len(rep.Findings) != 0 {
		t.Errorf("an import of golang.org/x/net/context was attributed to golang.org/x: %+v", rep.Findings[0].Reachability)
	}
}

// TestNoSourceMeansNoClaim: npm dependencies are declared and the index holds
// no JavaScript at all — the frontend was excluded, say. "No file imports
// lodash" is then true of an empty set. It must not be reported as "not
// imported", and it must not let a gate set at "imported" pass.
func TestNoSourceMeansNoClaim(t *testing.T) {
	idx := fixtureIndex()
	var kept []FileInfo
	for _, f := range idx.files {
		if f.Language != "javascript" && f.Language != "vue" {
			kept = append(kept, f)
		}
	}
	idx.files = kept
	var sites []ImportSite
	for _, s := range idx.imports {
		if s.Language != "javascript" {
			sites = append(sites, s)
		}
	}
	idx.imports = sites

	rep := analyzeFixture(t, idx, map[string]string{
		"jest.json": osv("T-JEST", "npm", "jest", "SEMVER", anyVersion, ""),
		"go.json":   osv("T-GO", "Go", "example.com/never", "SEMVER", anyVersion, ""),
	}, Options{})

	jest := findingFor(t, rep, "T-JEST")
	if jest.Reachability.State != ReachUnknown {
		t.Errorf("jest with no JavaScript indexed: state = %q, want %q", jest.Reachability.State, ReachUnknown)
	}
	if !strings.Contains(jest.Reachability.Detail, "no source file") {
		t.Errorf("the detail does not say why: %s", jest.Reachability.Detail)
	}
	// Go source IS indexed, so the Go answer is unchanged.
	if f := findingFor(t, rep, "T-GO"); f.Reachability.State != ReachNotImported {
		t.Errorf("Go: state = %q, want %q", f.Reachability.State, ReachNotImported)
	}

	// The same holds when the files exist but the index records no imports
	// for their language.
	rep = analyzeFixture(t, fixtureIndex(), map[string]string{
		"jest.json": osv("T-JEST", "npm", "jest", "SEMVER", anyVersion, ""),
	}, Options{ImportLanguages: []string{"go", "python"}})
	if f := findingFor(t, rep, "T-JEST"); f.Reachability.State != ReachUnknown {
		t.Errorf("jest with JavaScript imports unrecorded: state = %q, want %q", f.Reachability.State, ReachUnknown)
	}
	if len(rep.Gate(GateImported)) == 0 {
		t.Error("--fail-on imported passed on a dependency whose language the index records no imports for")
	}
}
