package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `licenses --json` end to end: a real index, a temporary module cache with
// one licensed module, and one module the cache does not hold.
func TestLicensesJSONReadsTheLocalModuleCache(t *testing.T) {
	root := isolate(t)
	cache := t.TempDir()
	t.Setenv("GOMODCACHE", cache)
	mit := "MIT License\n\nCopyright (c) 2024 Someone\n\nPermission is hereby granted, free of charge, to any person obtaining a copy of this software and associated documentation files (the \"Software\"), to deal in the Software without restriction, including without limitation the rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to permit persons to whom the Software is furnished to do so, subject to the following conditions:\n\nThe above copyright notice and this permission notice shall be included in all copies or substantial portions of the Software.\n\nTHE SOFTWARE IS PROVIDED \"AS IS\", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.\n"
	modDir := filepath.Join(cache, "example.com", "!licensed@v1.2.3")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modDir, "LICENSE"), []byte(mit), 0o644); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, root, map[string]string{
		"go.mod":  "module example.com/shop\n\ngo 1.22\n\nrequire (\n\texample.com/Licensed v1.2.3\n\texample.com/missing v0.1.0\n)\n",
		"main.go": "package main\n\nfunc main() {}\n",
	})
	for _, args := range [][]string{{"init"}, {"index"}} {
		if out, err := run(t, root, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	out, err := run(t, root, "licenses", "--json")
	if err != nil {
		t.Fatalf("licenses --json: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(root, "THIRD_PARTY.md")); err == nil {
		t.Error("licenses --json wrote THIRD_PARTY.md")
	}
	var rep struct {
		Total        int  `json:"total"`
		LicensesRead bool `json:"licenses_read"`
		Summary      struct {
			ByCategory map[string]int `json:"by_category"`
			BySource   map[string]int `json:"by_source"`
		} `json:"summary"`
		Dependencies []map[string]any `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &rep); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if rep.Total != 2 || !rep.LicensesRead || len(rep.Dependencies) != 2 {
		t.Fatalf("report = %+v", rep)
	}
	byName := map[string]map[string]any{}
	for _, d := range rep.Dependencies {
		byName[d["name"].(string)] = d
	}
	lic := byName["example.com/Licensed"]
	if lic["license"] != "MIT" || lic["source"] != "license-file" || lic["confidence"] != "exact" ||
		!strings.HasSuffix(lic["path"].(string), "example.com/!licensed@v1.2.3/LICENSE") {
		t.Errorf("licensed module = %v", lic)
	}
	miss := byName["example.com/missing"]
	if miss["license"] != "unknown" || miss["source"] != "unknown" || miss["reason"] == nil {
		t.Errorf("missing module = %v", miss)
	}
	if rep.Summary.ByCategory["permissive"] != 1 || rep.Summary.ByCategory["unknown"] != 1 ||
		rep.Summary.BySource["license-file"] != 1 || rep.Summary.BySource["unknown"] != 1 {
		t.Errorf("summary = %+v", rep.Summary)
	}
}

// --check compares the module graph, not the licence column: a file written
// before the column existed, or on a machine with another module cache,
// still checks; a changed module does not.
func TestThirdPartyCheckIgnoresTheLicenceColumn(t *testing.T) {
	mods := []moduleInfo{{Path: "a.example/x", Version: "v1.0.0", License: "MIT"}, {Path: "b.example/y", Version: "v2.0.0", Indirect: true}}
	now := renderThirdParty(mods)
	if !strings.Contains(now, "| `a.example/x` | v1.0.0 | yes | MIT |") || !strings.Contains(now, "| `b.example/y` | v2.0.0 | no | unknown |") {
		t.Fatalf("rendered:\n%s", now)
	}
	old := "| Module | Version | Direct |\n|---|---|---|\n| `a.example/x` | v1.0.0 | yes |\n| `b.example/y` | v2.0.0 | no |\n"
	if got, want := inventoryRows(old), inventoryRows(now); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("old-format rows %v != %v", got, want)
	}
	mods[1].Version = "v2.0.1"
	if strings.Join(inventoryRows(renderThirdParty(mods)), ",") == strings.Join(inventoryRows(now), ",") {
		t.Error("a version change was not noticed")
	}
}
