package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// indexedFiles writes files into a fresh project and indexes it.
func indexedFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	root := isolate(t)
	for path, body := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, step := range []string{"init", "index", "resolve"} {
		if out, err := run(t, root, step); err != nil {
			t.Fatalf("%s: %v\n%s", step, err, out)
		}
	}
	return root
}

// TestTheCallersCommandSaysWhatTheAgentToolSays: a function passed by name has
// no caller in the graph. The agent tool leads INCOMPLETE and says so; the
// command printed a table and "No callers found".
func TestTheCallersCommandSaysWhatTheAgentToolSays(t *testing.T) {
	root := indexedFiles(t, map[string]string{
		"lib/app.js": `function respond(ctx) {
  return ctx.body
}
const handleResponse = () => respond(1)
function callback(p) {
  return p.then(handleResponse)
}
`,
	})

	tool, err := run(t, root, "tool", "call", "callers", "name=handleResponse")
	if err != nil {
		t.Fatalf("tool call: %v\n%s", err, tool)
	}
	toolLine := strings.SplitN(tool, "\n", 3)[1]
	if !strings.Contains(toolLine, "its name appears in 1 other place(s) that are not recorded as calls: it may be passed as a value") {
		t.Fatalf("the agent tool no longer says it, so this test proves nothing: %q", toolLine)
	}

	out, err := run(t, root, "callers", "handleResponse")
	if err != nil {
		t.Fatalf("callers: %v\n%s", err, out)
	}
	if !strings.HasPrefix(out, toolLine+"\n") {
		t.Errorf("the command does not open with the agent tool's first line:\nwant %q\ngot:\n%s", toolLine, out)
	}
}

// TestTheImplementationsCommandListsTheTypesThatReachTheTarget: the command
// printed the types that name the target, under a count of zero more.
func TestTheImplementationsCommandListsTheTypesThatReachTheTarget(t *testing.T) {
	root := indexedFiles(t, map[string]string{
		"a/Base.java": "package a;\npublic interface Base {\n  void run();\n}\n",
		"a/Mid.java":  "package a;\npublic class Mid implements Base {\n  public void run() {}\n}\n",
		"a/Leaf.java": "package a;\npublic class Leaf extends Mid {\n  public void run() {}\n}\n",
	})

	out, err := run(t, root, "implementations", "Base")
	if err != nil {
		t.Fatalf("implementations: %v\n%s", err, out)
	}
	for _, want := range []string{
		"2 implementing or extending it (1 directly, 1 through other types",
		"Implemented or extended by (2)",
		"depth 2, via Mid",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("implementations output is missing %q:\n%s", want, out)
		}
	}

	direct, err := run(t, root, "implementations", "Base", "--direct-only")
	if err != nil {
		t.Fatalf("implementations --direct-only: %v\n%s", err, direct)
	}
	if !strings.Contains(direct, "Implemented or extended by (1)") || strings.Contains(direct, "depth 2") {
		t.Errorf("--direct-only listed the deeper type:\n%s", direct)
	}
}
