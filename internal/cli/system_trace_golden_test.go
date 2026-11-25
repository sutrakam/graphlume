package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSystemTraceOutputIsByteIdentical holds the text of `system callers` and
// `system impact`, including the ambiguity banner, the definition list and
// every refusal, to a golden file byte for byte.
//
// The symbol resolution behind both commands is shared with the estate MCP
// tool, so it lives in internal/multirepo. A refactor there that changes one
// space of what the CLI prints is a change to a published interface, and the
// substring checks elsewhere would not notice it.
//
// GRAPHLUME_UPDATE_GOLDEN=1 rewrites the file.
func TestSystemTraceOutputIsByteIdentical(t *testing.T) {
	ws, estate := goEstateFixture(t)

	cases := []struct {
		name string
		args []string
	}{
		{"callers lib:Do", []string{"system", "callers", "lib:Do"}},
		{"callers lib:Client.Send", []string{"system", "callers", "lib:Client.Send"}},
		{"callers lib:Platform (ambiguous)", []string{"system", "callers", "lib:Platform"}},
		{"impact lib:Do", []string{"system", "impact", "lib:Do"}},
		{"impact lib:Do depth 1 limit 2", []string{"system", "impact", "lib:Do", "--depth", "1", "--limit", "2"}},
		{"impact lib:Platform --path do_linux", []string{"system", "impact", "lib:Platform", "--path", "do_linux"}},
		{"error: unknown member", []string{"system", "callers", "nobody:Do"}},
		{"error: no member prefix", []string{"system", "callers", "Do"}},
		{"error: no such symbol", []string{"system", "callers", "lib:Nope"}},
		{"error: path matches nothing", []string{"system", "impact", "lib:Platform", "--path", "zzz"}},
		{"error: bad floor", []string{"system", "impact", "lib:Do", "--min-confidence", "sure"}},
	}

	var b strings.Builder
	for _, c := range cases {
		out, err := run(t, ws, append(c.args, "--members", estate)...)
		b.WriteString("=== " + c.name + "\n")
		b.WriteString(strings.ReplaceAll(out, ws, "<WS>"))
		if err != nil {
			b.WriteString("--- error\n" + strings.ReplaceAll(err.Error(), ws, "<WS>") + "\n")
		}
	}
	got := b.String()

	golden := filepath.Join("testdata", "system_trace.golden")
	if os.Getenv("GRAPHLUME_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (GRAPHLUME_UPDATE_GOLDEN=1 writes it): %v", err)
	}
	if got != string(want) {
		t.Errorf("system callers/impact output changed.\n--- got\n%s\n--- want\n%s", got, want)
	}
}
