package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// communitiesFixture is two packages that each call within themselves and
// never each other, and a file nothing refers to.
func communitiesFixture(t *testing.T) string {
	t.Helper()
	root := isolate(t)
	writeFiles(t, root, map[string]string{
		"billing/invoice.go": `package billing

func CreateInvoice(id string) string { return formatInvoice(lookupRate(id)) }
`,
		"billing/format.go": `package billing

func formatInvoice(rate int) string { return render(rate) }

func render(rate int) string { return "invoice" }
`,
		"billing/rates.go": `package billing

func lookupRate(id string) int { return defaultRate() }

func defaultRate() int { return 3 }
`,
		"shipping/label.go": `package shipping

func PrintLabel(id string) string { return layoutLabel(resolveAddress(id)) }
`,
		"shipping/layout.go": `package shipping

func layoutLabel(addr string) string { return addr }
`,
		"shipping/address.go": `package shipping

func resolveAddress(id string) string { return "address" }
`,
		"notes/standalone.go": `package notes

func Unconnected() int { return 1 }
`,
	})
	indexProject(t, root)
	return root
}

func TestCommunitiesGroupsFilesThatReferToEachOther(t *testing.T) {
	root := communitiesFixture(t)
	out, err := run(t, root, "communities")
	if err != nil {
		t.Fatalf("communities: %v\n%s", err, out)
	}
	flat := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{
		"2 communities over 6 of 7 file(s)",
		"1. billing (3 files)",
		"2. shipping (3 files)",
		"NAMED FOR the directory every member is under",
		"ID billing/format.go",
		"ID shipping/address.go",
		"MOSTLY IN billing (3)",
		// The file nothing refers to is accounted for, and not called unused.
		"1 of 7 file(s) are in no community",
		"Unlinked is not unused",
		// What the index could not see, and what a community is not.
		"What the index could not see:",
		"not a declared architecture",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "notes/standalone.go") {
		t.Errorf("a file with no relationship to another must not be listed in a community:\n%s", out)
	}
}

// Two runs over one index print the same bytes: ids, labels and order.
func TestCommunitiesOutputIsStableAcrossRuns(t *testing.T) {
	root := communitiesFixture(t)
	first, err := run(t, root, "communities")
	if err != nil {
		t.Fatalf("communities: %v\n%s", err, first)
	}
	for i := 0; i < 3; i++ {
		again, err := run(t, root, "communities")
		if err != nil {
			t.Fatal(err)
		}
		if again != first {
			t.Fatalf("run %d printed something different:\n%s\n---\n%s", i+2, again, first)
		}
	}
}

func TestCommunitiesBoundsAreStatedWhenTheyCut(t *testing.T) {
	root := communitiesFixture(t)
	out, err := run(t, root, "communities", "--limit", "1", "--members", "1")
	if err != nil {
		t.Fatalf("communities: %v\n%s", err, out)
	}
	flat := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{
		"Showing the 1 largest of 2 communities. The 1 not shown hold 3 file(s)",
		"... and 2 more; raise --members to list them",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "2. shipping") {
		t.Errorf("--limit 1 listed a second community:\n%s", out)
	}

	none, err := run(t, root, "communities", "--members", "0")
	if err != nil {
		t.Fatalf("communities --members 0: %v\n%s", err, none)
	}
	if strings.Contains(none, "MEMBERS") || !strings.Contains(none, "1. billing") {
		t.Errorf("--members 0 should list the communities and none of their files:\n%s", none)
	}
}

func TestCommunitiesPathNarrowsTheGraph(t *testing.T) {
	root := communitiesFixture(t)
	out, err := run(t, root, "communities", "--path", "shipping/")
	if err != nil {
		t.Fatalf("communities --path: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 community of 3 of 3 file(s)") || strings.Contains(out, "billing") {
		t.Errorf("only shipping/ should be grouped:\n%s", out)
	}

	empty, err := run(t, root, "communities", "--path", "nowhere/")
	if err != nil {
		t.Fatalf("a prefix matching nothing is an answer, not an error: %v\n%s", err, empty)
	}
	flat := strings.Join(strings.Fields(empty), " ")
	if !strings.Contains(flat, "No communities found.") || !strings.Contains(flat, `No indexed file is under "nowhere/"`) {
		t.Errorf("an empty answer must say why it is empty:\n%s", empty)
	}
}

func TestCommunitiesJSONIsTheReport(t *testing.T) {
	root := communitiesFixture(t)
	raw, err := run(t, root, "--json", "communities")
	if err != nil {
		t.Fatalf("communities --json: %v\n%s", err, raw)
	}
	var rep struct {
		Method      string `json:"method"`
		Communities []struct {
			ID          string `json:"id"`
			Label       string `json:"label"`
			LabelMethod string `json:"label_method"`
			Files       int    `json:"files"`
			Members     []struct {
				Path string `json:"path"`
			} `json:"members"`
			MembersOmitted int `json:"members_omitted"`
		} `json:"communities"`
		Total         int      `json:"communities_total"`
		Omitted       int      `json:"communities_omitted"`
		Files         int      `json:"files"`
		Unlinked      int      `json:"files_unlinked"`
		MinConfidence string   `json:"min_confidence"`
		Unbound       *int64   `json:"unbound_references"`
		Truncated     bool     `json:"truncated"`
		Notes         []string `json:"notes"`
	}
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	if rep.Total != 2 || len(rep.Communities) != 2 || rep.Omitted != 0 || rep.Files != 7 || rep.Unlinked != 1 {
		t.Errorf("counts: %+v", rep)
	}
	c := rep.Communities[0]
	if c.ID != "billing/format.go" || c.Label != "billing" || c.LabelMethod != "path-prefix" || c.Files != 3 || len(c.Members) != 3 {
		t.Errorf("first community: %+v", c)
	}
	if rep.Method == "" || rep.MinConfidence != "medium" || rep.Unbound == nil || len(rep.Notes) == 0 {
		t.Errorf("the JSON report must say how it was computed and what it could not see: %+v", rep)
	}
}

func TestCommunitiesRejectsBadFlags(t *testing.T) {
	root := communitiesFixture(t)
	for _, args := range [][]string{
		{"communities", "--limit", "0"},
		{"communities", "--members", "-1"},
		{"communities", "--min-confidence", "probable"},
		{"communities", "extra-argument"},
	} {
		if out, err := run(t, root, args...); err == nil {
			t.Errorf("%v should be rejected:\n%s", args, out)
		}
	}
}

func TestCommunitiesRefusesWithoutAnIndex(t *testing.T) {
	root := isolate(t)
	out, err := run(t, root, "communities")
	if err == nil {
		t.Fatalf("communities succeeded without an index:\n%s", out)
	}
	if !strings.Contains(err.Error()+out, "graphlume init") {
		t.Errorf("the error should name the fix: %v\n%s", err, out)
	}
}

// The agent form of the same answer is reachable from the shell, and agrees
// with the command about what was found.
func TestToolCallFindCommunitiesAgreesWithTheCommand(t *testing.T) {
	root := communitiesFixture(t)
	out, err := run(t, root, "tool", "call", "communities")
	if err != nil {
		t.Fatalf("tool call: %v\n%s", err, out)
	}
	for _, want := range []string{
		"2 communities of files found, all shown",
		"- community billing  billing/format.go",
		"- community shipping  shipping/address.go",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}
