package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// supplyFixture indexes a small Go and npm project and returns its root.
func supplyFixture(t *testing.T) string {
	t.Helper()
	root := isolate(t)
	writeFiles(t, root, map[string]string{
		"go.mod": "module example.com/svc\n\ngo 1.25.0\n\nrequire (\n" +
			"\tgithub.com/spf13/cobra v1.8.0\n" +
			"\texample.com/never v1.4.0\n" +
			"\tgopkg.in/yaml.v3 v3.0.1\n" +
			"\tgolang.org/x/sys v0.1.0 // indirect\n)\n",
		"main.go": `package main

import (
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func main() {
	cmd := &cobra.Command{Args: cobra.ExactArgs(1)}
	var out map[string]any
	_ = yaml.Unmarshal(nil, &out)
	_ = cmd
}
`,
		"web/package.json": `{
  "name": "web",
  "dependencies": {
    "lodash": "^4.17.20",
    "express": "4.18.2",
    "left-pad": "^1.3.0"
  },
  "devDependencies": {
    "jest": "^29.0.0"
  }
}
`,
		"web/package-lock.json": `{
  "lockfileVersion": 3,
  "packages": {
    "node_modules/lodash": {"version": "4.17.20", "license": "MIT"},
    "node_modules/jest": {"version": "29.7.0"}
  }
}
`,
		"web/app.js": `import _ from "lodash";
const express = require("express");

export function run(x) {
  return _.merge({}, x, express());
}
`,
	})
	for _, args := range [][]string{{"init"}, {"index"}} {
		if out, err := run(t, root, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	return root
}

// runSplit runs a command keeping standard output and standard error apart,
// which `run` deliberately does not.
func runSplit(t *testing.T, projectRoot string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := NewRoot()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"--project", projectRoot, "--quiet"}, args...))
	err = root.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

// advisoryDir writes OSV documents for the fixture and returns the directory.
func advisoryDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeFiles(t, dir, files)
	return dir
}

func osvDoc(id, ecosystem, name, events, extra string) string {
	if extra != "" {
		extra = ", " + extra
	}
	return `{"id": "` + id + `", "summary": "test advisory ` + id + `", "affected": [{"package": {"ecosystem": "` +
		ecosystem + `", "name": "` + name + `"}, "ranges": [{"type": "SEMVER", "events": [` + events + `]}]` + extra + `}]}`
}

const (
	everyVersion = `{"introduced": "0"}`
	cobraSymbols = `"ecosystem_specific": {"imports": [{"path": "github.com/spf13/cobra", "symbols": ["ExactArgs"]}]}`
	yamlSymbols  = `"ecosystem_specific": {"imports": [{"path": "gopkg.in/yaml.v3", "symbols": ["NewDecoder"]}]}`
)

// TestSupplyChainCommands runs `sbom` and `vulns` end to end over one indexed
// fixture. One fixture for all of them, deliberately: each property below is a
// read of the same index, and building an index per property made this the
// slowest file in the package for no extra coverage.
func TestSupplyChainCommands(t *testing.T) {
	root := supplyFixture(t)

	t.Run("sbom is the only thing on stdout", func(t *testing.T) { sbomIsTheOnlyThingOnStdout(t, root) })
	t.Run("vulns without advisories checks nothing and says so", func(t *testing.T) {
		vulnsWithoutAdvisoriesChecksNothingAndSaysSo(t, root)
	})
	t.Run("vulns reports the three states", func(t *testing.T) { vulnsReportsTheThreeStates(t, root) })
	t.Run("vulns --fail-on", func(t *testing.T) { vulnsFailOn(t, root) })
	// Last: it replaces the fixture's lockfile.
	t.Run("a lockfile cannot point outside the repository", func(t *testing.T) {
		aLockfileCannotPointOutsideTheRepository(t, root)
	})
}

// sbomIsTheOnlyThingOnStdout: the command's contract is that its standard
// output can be redirected to a file and committed. One JSON document, nothing
// before it, nothing after it.
func sbomIsTheOnlyThingOnStdout(t *testing.T, root string) {

	stdout, _, err := runSplit(t, root, "sbom")
	if err != nil {
		t.Fatalf("sbom: %v", err)
	}
	dec := json.NewDecoder(strings.NewReader(stdout))
	var doc struct {
		BOMFormat   string `json:"bomFormat"`
		SpecVersion string `json:"specVersion"`
		Metadata    struct {
			Timestamp string `json:"timestamp"`
			Component struct {
				Name string `json:"name"`
			} `json:"component"`
		} `json:"metadata"`
		Components []struct {
			BOMRef   string `json:"bom-ref"`
			Version  string `json:"version"`
			PURL     string `json:"purl"`
			Licenses []struct {
				License *struct {
					ID string `json:"id"`
				} `json:"license"`
				Expression string `json:"expression"`
			} `json:"licenses"`
			Properties []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"properties"`
		} `json:"components"`
	}
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("standard output is not a JSON document: %v\n%s", err, stdout)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Errorf("standard output holds something after the document (%v)", err)
	}
	if !strings.HasPrefix(stdout, "{") {
		t.Errorf("standard output does not begin with the document: %.40q", stdout)
	}

	if doc.BOMFormat != "CycloneDX" || doc.SpecVersion != "1.5" {
		t.Errorf("format %q version %q", doc.BOMFormat, doc.SpecVersion)
	}
	if doc.Metadata.Timestamp != "" {
		t.Errorf("a timestamp was written without --timestamp: %q", doc.Metadata.Timestamp)
	}
	// The project is named by its own manifest, never by the checkout directory.
	if doc.Metadata.Component.Name != "example.com/svc" {
		t.Errorf("project name = %q, want the go.mod module path", doc.Metadata.Component.Name)
	}

	state := map[string]string{}
	version := map[string]string{}
	for _, c := range doc.Components {
		version[c.PURL] = c.Version
		for _, p := range c.Properties {
			if p.Name == "graphlume:versionState" {
				state[c.PURL] = p.Value
			}
		}
	}
	for purl, want := range map[string][2]string{
		"pkg:golang/github.com/spf13/cobra@v1.8.0": {"pinned", "v1.8.0"},
		"pkg:golang/golang.org/x/sys@v0.1.0":       {"pinned", "v0.1.0"},
		"pkg:npm/express@4.18.2":                   {"pinned", "4.18.2"},
		"pkg:npm/lodash@4.17.20":                   {"locked", "4.17.20"}, // from package-lock.json
		"pkg:npm/jest@29.7.0":                      {"locked", "29.7.0"},
		"pkg:npm/left-pad":                         {"range", "^1.3.0"}, // no lockfile entry: no version in the purl
	} {
		if state[purl] != want[0] || version[purl] != want[1] {
			t.Errorf("%s: state %q version %q, want %q %q", purl, state[purl], version[purl], want[0], want[1])
		}
	}
	// The licence found on this machine travels with the component: the SPDX
	// id in the CycloneDX licenses field, where it was read in properties, and
	// for a licence not found, no licenses entry and the reason.
	for _, c := range doc.Components {
		props := map[string]string{}
		for _, p := range c.Properties {
			props[p.Name] = p.Value
		}
		switch c.PURL {
		case "pkg:npm/lodash@4.17.20":
			if len(c.Licenses) != 1 || c.Licenses[0].License == nil || c.Licenses[0].License.ID != "MIT" ||
				props["graphlume:licenseSource"] != "declared-metadata" || props["graphlume:licensePath"] != "web/package-lock.json" {
				t.Errorf("lodash licence = %+v, properties %v; want MIT read from web/package-lock.json", c.Licenses, props)
			}
		case "pkg:npm/left-pad":
			if len(c.Licenses) != 0 || props["graphlume:licenseSource"] != "unknown" || props["graphlume:licenseReason"] == "" {
				t.Errorf("left-pad licence = %+v, properties %v; want none, with the reason", c.Licenses, props)
			}
		}
	}
	if len(doc.Components) != 8 {
		t.Errorf("%d components, want the 8 declared dependencies", len(doc.Components))
	}

	// Deterministic, and --json changes nothing: the document is already JSON.
	again, _, err := runSplit(t, root, "sbom")
	if err != nil || again != stdout {
		t.Errorf("a second run produced a different document (err %v)", err)
	}
	asJSON, _, err := runSplit(t, root, "--json", "sbom")
	if err != nil || asJSON != stdout {
		t.Errorf("--json changed the document (err %v)", err)
	}

	stamped, _, err := runSplit(t, root, "sbom", "--timestamp", "2026-01-02T03:04:05Z", "--name", "svc")
	if err != nil || !strings.Contains(stamped, `"timestamp": "2026-01-02T03:04:05Z"`) || !strings.Contains(stamped, `"name": "svc"`) {
		t.Errorf("--timestamp / --name were not applied (err %v)", err)
	}
	if _, _, err := runSplit(t, root, "sbom", "--timestamp", "yesterday"); err == nil {
		t.Error("--timestamp accepted a value that is not RFC 3339")
	}
}

// vulnsWithoutAdvisoriesChecksNothingAndSaysSo: no advisory database is
// built in, so with none supplied the command explains where to get them and
// exits cleanly — and refuses to act as a gate.
func vulnsWithoutAdvisoriesChecksNothingAndSaysSo(t *testing.T, root string) {

	out, err := run(t, root, "vulns")
	if err != nil {
		t.Fatalf("vulns with no advisories must exit cleanly: %v\n%s", err, out)
	}
	for _, want := range []string{"nothing was checked", "never downloads", "osv-scanner", "--advisories", "8 declared"} {
		if !strings.Contains(out, want) {
			t.Errorf("the explanation does not mention %q:\n%s", want, out)
		}
	}

	jsonOut, err := run(t, root, "--json", "vulns")
	if err != nil {
		t.Fatalf("vulns --json with no advisories: %v", err)
	}
	var doc struct {
		Checked bool     `json:"checked"`
		How     []string `json:"how_to_obtain_advisories"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, jsonOut)
	}
	if doc.Checked || len(doc.How) == 0 {
		t.Errorf("checked = %v, how = %v; want false and a list", doc.Checked, doc.How)
	}

	// A gate with nothing to check would always pass.
	if out, err := run(t, root, "vulns", "--fail-on", "affected"); err == nil {
		t.Errorf("--fail-on with no advisories passed:\n%s", out)
	}
}

// vulnsReportsTheThreeStates runs the whole path — manifest, lockfile,
// index, advisory files — and checks one dependency lands in each state.
func vulnsReportsTheThreeStates(t *testing.T, root string) {
	dir := advisoryDir(t, map[string]string{
		"referenced.json":  osvDoc("T-REFERENCED", "Go", "github.com/spf13/cobra", everyVersion+`, {"fixed": "1.9.0"}`, cobraSymbols),
		"imported.json":    osvDoc("T-IMPORTED", "Go", "gopkg.in/yaml.v3", everyVersion, yamlSymbols),
		"notimported.json": osvDoc("T-NOT-IMPORTED", "Go", "example.com/never", everyVersion, ""),
		"cleared.json":     osvDoc("T-CLEARED", "npm", "lodash", everyVersion+`, {"fixed": "4.17.20"}`, ""),
		"require.json":     osvDoc("T-REQUIRE", "npm", "express", everyVersion, ""),
		"range.json":       osvDoc("T-RANGE", "npm", "left-pad", everyVersion+`, {"fixed": "0.0.1"}`, ""),
		"undeclared.json":  osvDoc("T-UNDECLARED", "Go", "github.com/gin-gonic/gin", everyVersion, ""),
	})

	out, err := run(t, root, "--json", "vulns", "--advisories", dir)
	if err != nil {
		t.Fatalf("vulns --json: %v\n%s", err, out)
	}
	var rep struct {
		Summary struct {
			Affected        int `json:"affected"`
			CouldNotCompare int `json:"could_not_compare"`
			NotAffected     int `json:"not_affected"`
			NamingNothing   int `json:"advisories_naming_no_declared_dependency"`
		} `json:"summary"`
		Findings []struct {
			Advisory string `json:"advisory"`
			Status   string `json:"status"`
			Reason   string `json:"reason"`
			Package  struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"package"`
			Reachability struct {
				State          string `json:"state"`
				ImportingFiles []struct {
					File string `json:"file"`
					Line int    `json:"line"`
					Via  string `json:"via"`
				} `json:"importing_files"`
				SymbolReferences []struct {
					Symbol string `json:"symbol"`
					File   string `json:"file"`
					Line   int    `json:"line"`
				} `json:"symbol_references"`
			} `json:"reachability"`
		} `json:"findings"`
		NotAffected []struct {
			Advisory string `json:"advisory"`
			Version  string `json:"version"`
		} `json:"not_affected"`
		Coverage []struct {
			Ecosystem string `json:"ecosystem"`
			Languages []struct {
				Language     string `json:"language"`
				Files        int    `json:"files"`
				BindsImports bool   `json:"binds_imports"`
			} `json:"languages"`
		} `json:"coverage"`
		Caveats []string `json:"caveats"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}

	type got struct {
		status, state, version string
		files, refs            int
		via                    string
	}
	byID := map[string]got{}
	for _, f := range rep.Findings {
		g := got{status: f.Status, state: f.Reachability.State, version: f.Package.Version,
			files: len(f.Reachability.ImportingFiles), refs: len(f.Reachability.SymbolReferences)}
		if g.files > 0 {
			g.via = f.Reachability.ImportingFiles[0].Via
		}
		byID[f.Advisory] = g
	}
	want := map[string]got{
		"T-REFERENCED":   {status: "affected", state: "referenced", version: "v1.8.0", files: 1, refs: 1},
		"T-IMPORTED":     {status: "affected", state: "imported_symbol_use_unknown", version: "v3.0.1", files: 1},
		"T-NOT-IMPORTED": {status: "affected", state: "not_imported", version: "v1.4.0"},
		// require("express") was recorded as a call whose argument was read
		// back from the text, and the site said it was a require. The index
		// now records a top-level require as an import (parse/import_bindings.go),
		// so the same site arrives as an import and carries no via.
		"T-REQUIRE": {status: "affected", state: "imported_symbol_use_unknown", version: "4.18.2", files: 1},
		// "^1.3.0" with no lockfile entry: the advisory would clear any real
		// release, and it still cannot be said to.
		"T-RANGE": {status: "could_not_compare", state: "not_imported"},
	}
	for id, w := range want {
		if byID[id] != w {
			t.Errorf("%s: got %+v, want %+v", id, byID[id], w)
		}
	}
	if len(rep.Findings) != len(want) {
		t.Errorf("%d findings, want %d: %+v", len(rep.Findings), len(want), byID)
	}
	// lodash resolves to 4.17.20 through package-lock.json, which is the fix.
	if len(rep.NotAffected) != 1 || rep.NotAffected[0].Advisory != "T-CLEARED" || rep.NotAffected[0].Version != "4.17.20" {
		t.Errorf("not affected = %+v, want lodash 4.17.20 cleared by T-CLEARED", rep.NotAffected)
	}
	if rep.Summary.Affected != 4 || rep.Summary.CouldNotCompare != 1 || rep.Summary.NotAffected != 1 || rep.Summary.NamingNothing != 1 {
		t.Errorf("summary = %+v", rep.Summary)
	}
	for _, f := range rep.Findings {
		if f.Advisory == "T-REFERENCED" {
			ref := f.Reachability.SymbolReferences[0]
			if ref.File != "main.go" || ref.Line != 9 || ref.Symbol != "github.com/spf13/cobra.ExactArgs" {
				t.Errorf("the reference to cobra.ExactArgs = %+v, want main.go:9", ref)
			}
		}
	}

	// The answer states which languages record imports.
	seen := map[string]bool{}
	for _, ec := range rep.Coverage {
		for _, l := range ec.Languages {
			if l.Files > 0 && l.BindsImports {
				seen[ec.Ecosystem+"/"+l.Language] = true
			}
		}
	}
	if !seen["go/go"] || !seen["npm/javascript"] {
		t.Errorf("coverage does not state that go and javascript imports are recorded: %+v", rep.Coverage)
	}
	if len(rep.Caveats) < 3 {
		t.Errorf("the report carries %d caveats, want the three mandatory ones", len(rep.Caveats))
	}

	// The same report for a person.
	text, err := run(t, root, "vulns", "--advisories", dir)
	if err != nil {
		t.Fatalf("vulns: %v\n%s", err, text)
	}
	for _, wantText := range []string{
		"AFFECTED (4)",
		"COULD NOT COMPARE — not the same as unaffected (1)",
		"NOT AFFECTED (1)",
		"REFERENCED",
		"IMPORTED, SYMBOL USE UNKNOWN",
		"NOT IMPORTED in the indexed code",
		"main.go:9",
		"name no DECLARED dependency",
		"What this report covers",
		"Only DECLARED dependencies were checked",
		"semver 2.0.0",
	} {
		if !strings.Contains(text, wantText) {
			t.Errorf("the text report does not contain %q:\n%s", wantText, text)
		}
	}
	// A cleared dependency is named only on request.
	if strings.Contains(text, "T-CLEARED") {
		t.Error("the not-affected comparison is listed without --all")
	}
	if all, _ := run(t, root, "vulns", "--advisories", dir, "--all"); !strings.Contains(all, "T-CLEARED") {
		t.Error("--all does not list the not-affected comparison")
	}
}

// aLockfileCannotPointOutsideTheRepository: lockfiles are read from the
// working tree. A repository that makes "package-lock.json" a symbolic link to
// a file elsewhere must not have that file read as its lockfile.
func aLockfileCannotPointOutsideTheRepository(t *testing.T, root string) {
	outside := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.WriteFile(outside, []byte(`{"lockfileVersion": 3, "packages": {"node_modules/left-pad": {"version": "9.9.9"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(root, "web", "package-lock.json")
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, lock); err != nil {
		t.Skipf("cannot create a symbolic link here: %v", err)
	}

	stdout, _, err := runSplit(t, root, "sbom")
	if err != nil {
		t.Fatalf("sbom: %v", err)
	}
	if strings.Contains(stdout, "9.9.9") {
		t.Error("a version was read from a file outside the repository through a symbolic link")
	}
	if !strings.Contains(stdout, `"bom-ref": "pkg:npm/left-pad#web/package.json:6"`) {
		t.Errorf("left-pad should have stayed a range with no lockfile to pin it:\n%s", stdout)
	}
}

// vulnsFailOn: the exit status is a gate only when asked, at the level
// asked, and a version that could not be compared fails it.
func vulnsFailOn(t *testing.T, root string) {
	notImported := advisoryDir(t, map[string]string{
		"a.json": osvDoc("T-NOT-IMPORTED", "Go", "example.com/never", everyVersion, ""),
	})
	referenced := advisoryDir(t, map[string]string{
		"a.json": osvDoc("T-REFERENCED", "Go", "github.com/spf13/cobra", everyVersion, cobraSymbols),
	})
	imported := advisoryDir(t, map[string]string{
		"a.json": osvDoc("T-IMPORTED", "Go", "gopkg.in/yaml.v3", everyVersion, yamlSymbols),
	})
	uncompared := advisoryDir(t, map[string]string{
		"a.json": osvDoc("T-RANGE", "npm", "left-pad", everyVersion+`, {"fixed": "0.0.1"}`, ""),
	})
	clean := advisoryDir(t, map[string]string{
		"a.json": osvDoc("T-CLEARED", "npm", "lodash", everyVersion+`, {"fixed": "4.17.20"}`, ""),
	})
	damaged := advisoryDir(t, map[string]string{
		"a.json": osvDoc("T-CLEARED", "npm", "lodash", everyVersion+`, {"fixed": "4.17.20"}`, ""),
		"b.json": `{"id": "T-TRUNCATED", "affected": [`,
	})

	cases := []struct {
		name     string
		dir      string
		level    string
		wantFail bool
	}{
		{"no flag never fails", referenced, "", false},
		{"referenced at referenced", referenced, "referenced", true},
		{"referenced at imported", referenced, "imported", true},
		{"referenced at affected", referenced, "affected", true},
		{"imported at referenced", imported, "referenced", false},
		{"imported at imported", imported, "imported", true},
		{"not imported at imported", notImported, "imported", false},
		{"not imported at affected", notImported, "affected", true},
		{"uncompared at affected", uncompared, "affected", true},
		{"clean at affected", clean, "affected", false},
		{"clean at referenced", clean, "referenced", false},
		{"an unreadable advisory file", damaged, "referenced", true},
		{"an unreadable advisory file, no gate", damaged, "", false},
	}
	for _, tc := range cases {
		args := []string{"vulns", "--advisories", tc.dir}
		if tc.level != "" {
			args = append(args, "--fail-on", tc.level)
		}
		out, err := run(t, root, args...)
		if (err != nil) != tc.wantFail {
			t.Errorf("%s: err = %v, want failure %v\n%s", tc.name, err, tc.wantFail, out)
		}
		if err != nil && !strings.Contains(err.Error(), "--fail-on "+tc.level) {
			t.Errorf("%s: the error does not name the gate: %v", tc.name, err)
		}
	}

	// The report is printed before the gate fails: CI shows why.
	out, err := run(t, root, "vulns", "--advisories", referenced, "--fail-on", "referenced")
	if err == nil || !strings.Contains(out, "REFERENCED") {
		t.Errorf("a failing gate did not print its report first (err %v):\n%s", err, out)
	}

	// An unreadable file is named in the report.
	if out, _ := run(t, root, "vulns", "--advisories", damaged); !strings.Contains(out, "UNREADABLE") || !strings.Contains(out, "b.json") {
		t.Errorf("the damaged advisory file is not named:\n%s", out)
	}

	// --ignore leaves the advisory out and says so.
	out, err = run(t, root, "vulns", "--advisories", referenced, "--fail-on", "referenced", "--ignore", "T-REFERENCED")
	if err != nil || !strings.Contains(out, "ignored by --ignore: T-REFERENCED") {
		t.Errorf("--ignore: err %v, output:\n%s", err, out)
	}

	// A mistyped level is an error, not a gate that never fires.
	if _, err := run(t, root, "vulns", "--advisories", referenced, "--fail-on", "high"); err == nil ||
		!strings.Contains(err.Error(), "referenced, imported or affected") {
		t.Errorf("--fail-on high: err = %v, want the list of levels", err)
	}
	// So is a path that does not exist, or a file that is not an advisory.
	if _, err := run(t, root, "vulns", "--advisories", filepath.Join(clean, "missing.json")); err == nil {
		t.Error("a missing advisory file was accepted")
	}
	if _, err := run(t, root, "vulns", "--advisories", filepath.Join(root, "web", "package.json")); err == nil {
		t.Error("a file that is not an OSV document was accepted as an advisory")
	}
}
