package supplychain

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/analyze/deps"
)

// A record in the shape the Go vulnerability database publishes.
const goRecord = `{
  "schema_version": "1.6.0",
  "id": "GO-TEST-0001",
  "aliases": ["CVE-2026-1111", "GHSA-aaaa-bbbb-cccc"],
  "summary": "  A test advisory  ",
  "affected": [{
    "package": {"name": "golang.org/x/net", "ecosystem": "Go"},
    "ranges": [{"type": "SEMVER", "events": [{"introduced": "0"}, {"fixed": "0.23.0"}]}],
    "ecosystem_specific": {"imports": [{"path": "golang.org/x/net/html", "symbols": ["Parse", "Tokenizer.Next"]}]}
  }],
  "database_specific": {"url": "https://example.invalid/GO-TEST-0001", "severity": "HIGH"}
}`

func TestLoadAdvisoriesReadsEveryShape(t *testing.T) {
	dir := writeAdvisories(t, map[string]string{
		// One record.
		"go/GO-TEST-0001.json": goRecord,
		// A JSON array of records.
		"batch.json": `[` + osv("T-ARRAY-1", "npm", "lodash", "SEMVER", anyVersion, "") + `,` +
			osv("T-ARRAY-2", "PyPI", "requests", "ECOSYSTEM", anyVersion, "") + `]`,
		// An OSV API response.
		"api.json": `{"vulns": [` + osv("T-API", "crates.io", "smallvec", "SEMVER", anyVersion, "") + `]}`,
		// osv-scanner's report.
		"scanner.json": `{"results": [{"source": {"path": "go.mod"}, "packages": [{"package": {"name": "x"},
			"vulnerabilities": [` + osv("T-SCANNER", "Maven", "g:a", "ECOSYSTEM", anyVersion, "") + `]}]}]}`,
		// Not OSV at all, but valid JSON: counted, not fatal inside a directory.
		"package.json": `{"name": "stray"}`,
		// Not JSON: possibly a damaged advisory.
		"broken.json": `{"id": "T-BROKEN", "affected": [`,
		// Withdrawn by its database.
		"withdrawn.json": `{"id": "T-WITHDRAWN", "withdrawn": "2026-01-01T00:00:00Z", "affected": []}`,
		// The same id twice.
		"dup.json": osv("T-API", "crates.io", "smallvec", "SEMVER", anyVersion, ""),
		// An ecosystem this tool does not match.
		"debian.json": osv("T-DEBIAN", "Debian:12", "openssl", "ECOSYSTEM", anyVersion, ""),
		// Not a .json file: never opened.
		"README.md": "not an advisory",
	})

	set, err := LoadAdvisories([]string{dir})
	if err != nil {
		t.Fatalf("LoadAdvisories: %v", err)
	}

	var ids []string
	byID := map[string]Advisory{}
	for _, a := range set.Advisories {
		ids = append(ids, a.ID)
		byID[a.ID] = a
	}
	if got, want := strings.Join(ids, " "), "GO-TEST-0001 T-API T-ARRAY-1 T-ARRAY-2 T-DEBIAN T-SCANNER"; got != want {
		t.Errorf("advisories = %s\n          want %s", got, want)
	}
	if set.Files != 9 {
		t.Errorf("files opened = %d, want the 9 .json files", set.Files)
	}
	if set.Withdrawn != 1 || set.Duplicates != 1 || set.NotOSV != 1 {
		t.Errorf("withdrawn %d, duplicates %d, not OSV %d; want 1 each", set.Withdrawn, set.Duplicates, set.NotOSV)
	}
	if len(set.Unreadable) != 1 || filepath.Base(set.Unreadable[0].Path) != "broken.json" {
		t.Errorf("unreadable = %+v, want broken.json — a file that may be an advisory must be reported", set.Unreadable)
	}
	if set.OtherEcosystems["Debian"] != 1 {
		t.Errorf("other ecosystems = %v, want Debian counted", set.OtherEcosystems)
	}

	g := byID["GO-TEST-0001"]
	if g.Summary != "A test advisory" || g.Severity != "high" || len(g.Aliases) != 2 {
		t.Errorf("GO-TEST-0001: summary %q, severity %q, aliases %v", g.Summary, g.Severity, g.Aliases)
	}
	a := g.Affected[0]
	if a.Ecosystem != deps.EcosystemGo || a.Name != "golang.org/x/net" {
		t.Errorf("affected package = %s %q", a.Ecosystem, a.Name)
	}
	if len(a.Ranges) != 1 || a.Ranges[0].Type != "SEMVER" || len(a.Ranges[0].Events) != 2 ||
		a.Ranges[0].Events[1] != (Event{Kind: "fixed", Version: "0.23.0"}) {
		t.Errorf("ranges = %+v", a.Ranges)
	}
	if len(a.Symbols.Packages) != 1 || a.Symbols.Packages[0].Path != "golang.org/x/net/html" ||
		strings.Join(a.Symbols.Packages[0].Symbols, ",") != "Parse,Tokenizer.Next" {
		t.Errorf("symbols = %+v", a.Symbols)
	}
	if byID["T-API"].Affected[0].Ecosystem != deps.EcosystemCargo || byID["T-SCANNER"].Affected[0].Ecosystem != deps.EcosystemMaven {
		t.Error("crates.io or Maven was not mapped to this tool's ecosystem names")
	}
}

// TestANamedFileMustBeUsable: a path the user typed is a statement of intent.
// A typo, or a file that is not OSV, must fail rather than yield a clean report.
func TestANamedFileMustBeUsable(t *testing.T) {
	dir := writeAdvisories(t, map[string]string{
		"broken.json": `{"id": `,
		"other.json":  `{"name": "not an advisory"}`,
		"empty.json":  ``,
		// A scanner that found nothing writes a report with no records. That
		// is a legitimate, usable file.
		"clean-scan.json": `{"results": []}`,
		"good.json":       osv("T-GOOD", "Go", "example.com/x", "SEMVER", anyVersion, ""),
	})
	for _, name := range []string{"broken.json", "other.json", "empty.json", "missing.json"} {
		if _, err := LoadAdvisories([]string{filepath.Join(dir, name)}); err == nil {
			t.Errorf("LoadAdvisories(%s) succeeded; a named file that cannot be used must be an error", name)
		}
	}
	// A directory with no JSON file in it is the wrong directory.
	if _, err := LoadAdvisories([]string{t.TempDir()}); err == nil {
		t.Error("an empty advisory directory was accepted; it would report a clean result for a check that read nothing")
	}
	set, err := LoadAdvisories([]string{filepath.Join(dir, "clean-scan.json"), filepath.Join(dir, "good.json")})
	if err != nil {
		t.Fatalf("a clean scanner report and a good record: %v", err)
	}
	if len(set.Advisories) != 1 || set.Advisories[0].ID != "T-GOOD" {
		t.Errorf("advisories = %+v", set.Advisories)
	}
}

func TestSymbolScopeFromThreeDatabases(t *testing.T) {
	ghsa := parseSymbolScope([]byte(`{"affected_functions": ["jinja2.utils.urlize"]}`))
	if len(ghsa.Functions) != 1 || ghsa.Functions[0] != "jinja2.utils.urlize" {
		t.Errorf("GitHub affected_functions = %+v", ghsa)
	}
	rustsec := parseSymbolScope([]byte(`{"affects": {"arch": [], "os": [], "functions": ["smallvec::SmallVec::insert_many"]}}`))
	if len(rustsec.Functions) != 1 || rustsec.Functions[0] != "smallvec::SmallVec::insert_many" {
		t.Errorf("RustSec affects.functions = %+v", rustsec)
	}
	for _, none := range []string{``, `{}`, `null`, `"text"`, `{"imports": "wrong type"}`} {
		if s := parseSymbolScope([]byte(none)); !s.Empty() {
			t.Errorf("parseSymbolScope(%q) = %+v, want empty", none, s)
		}
	}
	// GitHub writes Go functions as "import/path.Symbol".
	pkgs := goFunctionPackages([]string{"golang.org/x/net/html.Parse", "gopkg.in/yaml.v3.Unmarshal", "nodot"})
	if len(pkgs) != 2 || pkgs[0].Path != "golang.org/x/net/html" || pkgs[0].Symbols[0] != "Parse" ||
		pkgs[1].Path != "gopkg.in/yaml.v3" || pkgs[1].Symbols[0] != "Unmarshal" {
		t.Errorf("goFunctionPackages = %+v", pkgs)
	}
}

// FuzzParseAdvisoryJSON: advisory files come from outside. Whatever they hold,
// reading one must not panic, and a record it returns must be convertible.
func FuzzParseAdvisoryJSON(f *testing.F) {
	f.Add([]byte(goRecord))
	f.Add([]byte(`[{"id": "X", "affected": [{"ranges": [{"events": [{"introduced": 0}, null, {"fixed": ["a"]}]}]}]}]`))
	f.Add([]byte(`{"results": [{"packages": [{"vulnerabilities": [{"id": "Y"}]}]}]}`))
	f.Add([]byte(`{"id": "Z", "affected": [{"ecosystem_specific": [1, 2], "package": {}}], "database_specific": 7}`))
	f.Fuzz(func(t *testing.T, src []byte) {
		records, _, err := parseAdvisoryJSON(src)
		if err != nil {
			return
		}
		for _, rec := range records {
			adv := rec.advisory("fuzz")
			for _, a := range adv.Affected {
				_ = matchVersion(deps.EcosystemNPM, "1.0.0", a)
			}
		}
	})
}
