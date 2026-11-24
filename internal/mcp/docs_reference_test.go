package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/config"
)

// toolReference is docs/reference/mcp-tools.md.
func toolReference(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "reference", "mcp-tools.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestToolReferenceMatchesTheServer holds the reference page to what the
// server registers: which tools exist, how many each profile exposes, which
// profile first exposes each, and every argument each takes.
//
// The page said "standard: All 18" and "full: All 18" while the full profile
// registered 25, listed seven tools in a table with no parameter descriptions,
// and gave impact a default its code does not have. Nothing failed,
// because nothing compared the two.
func TestToolReferenceMatchesTheServer(t *testing.T) {
	page := toolReference(t)

	type profile struct {
		id    config.MCPProfile
		label string
		tools map[string]map[string]bool // tool -> argument names
	}
	profiles := []*profile{
		{id: config.MCPMinimal, label: "minimal"},
		{id: config.MCPStandard, label: "standard"},
		{id: config.MCPFull, label: "full"},
	}
	for _, p := range profiles {
		// With an estate and a report runner configured, so the page
		// documents every tool a profile can expose, estate and the report
		// tools included.
		fs := newFullServer(t, seedChain, withProfile(p.id), withEstate(newFakeEstate()), withReports(&fakeReports{}))
		res, err := fs.sess.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatalf("list tools on %s: %v", p.label, err)
		}
		p.tools = map[string]map[string]bool{}
		for _, tool := range res.Tools {
			raw, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatal(err)
			}
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			if err := json.Unmarshal(raw, &schema); err != nil {
				t.Fatal(err)
			}
			args := map[string]bool{}
			for name := range schema.Properties {
				args[name] = true
			}
			p.tools[tool.Name] = args
		}

		// The count the page states for this profile.
		row := regexp.MustCompile("(?m)^\\| `" + p.label + "`[^|]*\\| (\\d+):").FindStringSubmatch(page)
		if row == nil {
			t.Errorf("the profile table has no row stating a tool count for %q", p.label)
			continue
		}
		if want := fmt.Sprintf("%d", len(p.tools)); row[1] != want {
			t.Errorf("the page says the %s profile exposes %s tools; the server registers %s",
				p.label, row[1], want)
		}
	}

	firstProfile := func(tool string) string {
		for _, p := range profiles {
			if _, ok := p.tools[tool]; ok {
				return p.label
			}
		}
		return ""
	}

	full := profiles[2].tools
	for tool, args := range full {
		// One row in the tools table, naming the profile that first exposes it.
		row := regexp.MustCompile("(?m)^\\| `" + tool + "` \\| ([a-z]+)[^|]*\\| ([^|]*) \\|").FindStringSubmatch(page)
		if row == nil {
			t.Errorf("tool %q is registered and has no row in the tools table", tool)
			continue
		}
		if want := firstProfile(tool); row[1] != want {
			t.Errorf("the page lists %q under profile %q; it is first exposed by %q", tool, row[1], want)
		}

		// One section, documenting exactly the arguments the schema has.
		heading := "### `" + tool + "`\n"
		start := strings.Index(page, heading)
		if start < 0 {
			t.Errorf("tool %q has no section under Parameters", tool)
			continue
		}
		section := page[start+len(heading):]
		if end := strings.Index(section, "\n### "); end >= 0 {
			section = section[:end]
		}
		if end := strings.Index(section, "\n## "); end >= 0 {
			section = section[:end]
		}
		documented := map[string]bool{}
		for _, m := range regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\| [a-z/]+ \\| (?:yes|no) \\|").FindAllStringSubmatch(section, -1) {
			documented[m[1]] = true
		}
		for arg := range args {
			if !documented[arg] {
				t.Errorf("%s takes %q, which its section does not document", tool, arg)
			}
			if !strings.Contains(row[2], "`"+arg+"`") {
				t.Errorf("%s takes %q, which its row in the tools table does not list", tool, arg)
			}
		}
		for arg := range documented {
			if !args[arg] {
				t.Errorf("the page documents %s argument %q, which the tool does not take", tool, arg)
			}
		}
	}

	// And nothing documented that is not registered.
	for _, m := range regexp.MustCompile("(?m)^### `([a-z_]+)`$").FindAllStringSubmatch(page, -1) {
		if _, ok := full[m[1]]; !ok {
			t.Errorf("the page has a section for %q, which the server does not register", m[1])
		}
	}
}

// TestExportedTableIsDocumented keeps the page's copy of the one table that
// says where the index's exported flag can be trusted in step with the table.
func TestExportedTableIsDocumented(t *testing.T) {
	page := toolReference(t)
	start := strings.Index(page, "| Language | Why the flag is trusted |")
	if start < 0 {
		t.Fatal("docs/reference/mcp-tools.md has no table of languages whose exported flag is trusted")
	}
	table := page[start:]
	if end := strings.Index(table, "\n\n"); end >= 0 {
		table = table[:end]
	}
	var documented []string
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z_0-9]+)` \\|").FindAllStringSubmatch(table, -1) {
		documented = append(documented, m[1])
	}
	want := capability.ExportedFlagTrustedLanguages()
	if strings.Join(documented, ",") != strings.Join(want, ",") {
		t.Errorf("the page lists %v as languages whose exported flag is trusted; the table in "+
			"internal/capability/exported.go is %v", documented, want)
	}
	for _, lang := range want {
		if !capability.ExportedKnown(lang, "") {
			t.Errorf("%s is in the table and ExportedKnown denies it", lang)
		}
	}
	for _, lang := range []string{"java", "typescript", "tsx", "javascript", "python", "kotlin", "c_sharp", "rust", ""} {
		if capability.ExportedKnown(lang, "a/b.x") {
			t.Errorf("ExportedKnown(%q) is true; the index derives that flag from the first letter of the name", lang)
		}
	}
	if !capability.ExportedKnown("", "pkg/a.go") {
		t.Error("a Go file with no recorded language should still be recognised by its extension")
	}
}
