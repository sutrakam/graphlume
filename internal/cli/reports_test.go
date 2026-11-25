package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/config"
)

func TestOnlyReadOnlyCommandsMayRunAsReports(t *testing.T) {
	for _, ok := range [][]string{
		{"policy", "check", "--format=json"},
		{"arch", "--json"},
		{"arch", "adr", "--json", "--", "-x"},
		{"deps", "--json"},
		{"vulns", "--json"},
		{"sbom"},
		{"tour", "--json"},
		{"lesson", "report", "--json"},
		{"licenses", "--json"},
	} {
		if err := allowedReport(ok); err != nil {
			t.Errorf("%v refused: %v", ok, err)
		}
	}
	for _, bad := range [][]string{
		nil,
		{"lesson", "add", "--task=x"},
		{"lesson"},
		{"licenses"},
		{"licenses", "--check"},
		{"licenses", "--json", "--check"},
		{"licenses", "--check", "--json"},
		{"policy", "explain"},
		{"policy"},
		{"index"},
		{"serve"},
		{"clean"},
		{"arch", "bogus"},
		{"--project", "/", "deps"},
	} {
		if err := allowedReport(bad); err == nil {
			t.Errorf("%v was allowed", bad)
		}
	}
}

func TestCappedBufferKeepsTheStartAndSaysItCut(t *testing.T) {
	b := cappedBuffer{max: 5}
	if n, err := b.Write([]byte("abc")); n != 3 || err != nil {
		t.Fatalf("write 1: %d %v", n, err)
	}
	if n, err := b.Write([]byte("defgh")); n != 5 || err != nil {
		t.Fatalf("write 2 reported %d, %v; a capped writer must not fail the command", n, err)
	}
	if b.buf.String() != "abcde" || !b.over {
		t.Errorf("kept %q, over=%v", b.buf.String(), b.over)
	}
}

// The report tools answer with what the commands print, over a real index,
// through the same invoker the REST endpoint and the web portal use.
func TestReportToolsRunTheCommandsOverARealIndex(t *testing.T) {
	root := isolate(t)
	writeFiles(t, root, map[string]string{
		"go.mod":            "module example.com/shop\n\ngo 1.22\n\nrequire example.com/unused v1.2.3\n",
		"main.go":           "package main\n\nimport \"example.com/shop/lib\"\n\nfunc main() { lib.Total(1, 2) }\n",
		"lib/lib.go":        "package lib\n\n// Total adds.\nfunc Total(a, b int) int { return a + b }\n",
		"docs/adr/0001.md":  "# 1. Use optimistic locking\n\nStatus: accepted\n\n## Decision\nWe lock optimistically.\n",
		"security/osv.json": `{"id":"GHSA-test","affected":[{"package":{"ecosystem":"Go","name":"example.com/unused"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"2.0.0"}]}]}]}`,
	})
	for _, args := range [][]string{{"init"}, {"index"}, {"resolve"}} {
		if out, err := run(t, root, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	g := &Global{Project: root, Quiet: true}
	cmd := NewRoot()
	cmd.SetArgs([]string{"--project", root, "--quiet", "version"})
	cmd.SetOut(io.Discard)
	if err := g.setup(cmd); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	srv, closeServer, err := openAgentServer(ctx, g, agentServerOptions{Profile: config.MCPFull})
	if err != nil {
		t.Fatal(err)
	}
	defer closeServer()
	inv, err := srv.NewInvoker(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = inv.Close() }()

	call := func(tool string, args map[string]any) (map[string]any, string, bool) {
		t.Helper()
		raw, _ := json.Marshal(args)
		res, err := inv.Call(ctx, tool, raw)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		var out map[string]any
		if len(res.Structured) > 0 {
			_ = json.Unmarshal(res.Structured, &out)
		}
		return out, res.Text, res.IsError
	}

	// deps and licenses see the declared module; nothing here wrote a file.
	out, text, isErr := call("deps", nil)
	if isErr || len(out["dependencies"].([]any)) == 0 || !strings.Contains(text, "example.com/unused") {
		t.Errorf("deps: err=%v out=%v\n%s", isErr, out, text)
	}
	out, _, isErr = call("licenses", nil)
	if isErr || out["licenses_read"] != true || out["total"].(float64) < 1 {
		t.Errorf("licenses: err=%v out=%v", isErr, out)
	} else if row := out["dependencies"].([]any)[0].(map[string]any); row["license"] == nil || row["source"] == nil || row["category"] == nil {
		t.Errorf("licenses row lacks license/source/category: %v", row)
	}
	if _, err := os.Stat(filepath.Join(root, "THIRD_PARTY.md")); err == nil {
		t.Error("licenses wrote THIRD_PARTY.md; a report tool must not write")
	}

	// adr: the record, then a query that matches nothing.
	out, text, isErr = call("adr", nil)
	if isErr || len(out["adrs"].([]any)) != 1 || !strings.Contains(text, "optimistic") {
		t.Errorf("adr: err=%v out=%v\n%s", isErr, out, text)
	}
	if out, _, isErr = call("adr", map[string]any{"query": "no such thing"}); isErr || len(out["adrs"].([]any)) != 0 {
		t.Errorf("adr with no match: err=%v out=%v", isErr, out)
	}

	// arch: none declared is a statement, not a failure.
	out, _, isErr = call("arch", nil)
	if isErr || out["architecture_declared"] != false {
		t.Errorf("arch: err=%v out=%v", isErr, out)
	}

	// vulns: an advisory inside the repository is read; one outside is refused.
	out, _, isErr = call("vulns", map[string]any{"advisories": []string{"security/osv.json"}})
	if isErr || out["checked"] == false {
		t.Errorf("vulns with an advisory: err=%v out=%v", isErr, out)
	}
	if _, _, isErr = call("vulns", map[string]any{"advisories": []string{"../osv.json"}}); !isErr {
		t.Error("vulns accepted a path outside the repository")
	}
	if out, _, _ = call("vulns", nil); out["checked"] != false {
		t.Errorf("vulns without advisories must say nothing was checked: %v", out)
	}

	// sbom, tour, lessons and policy answer with the commands' JSON.
	if out, _, isErr = call("sbom", nil); isErr || out["bomFormat"] != "CycloneDX" {
		t.Errorf("sbom: err=%v out=%v", isErr, out)
	}
	if out, _, isErr = call("tour", nil); isErr || out["stops"] == nil {
		t.Errorf("tour: err=%v out=%v", isErr, out)
	}
	if _, text, isErr = call("lessons", nil); isErr || !strings.Contains(text, "No lessons") {
		t.Errorf("lessons: err=%v\n%s", isErr, text)
	}
	if out, text, isErr = call("policy", nil); isErr || len(out) == 0 {
		t.Errorf("policy with no policy block: err=%v out=%v\n%s", isErr, out, text)
	}

	// structure over the same index.
	out, text, isErr = call("structure", map[string]any{"owners": false})
	if isErr || out["folders"] == nil || !strings.Contains(text, "lib") {
		t.Errorf("structure: err=%v out=%v\n%s", isErr, out, text)
	}
}
