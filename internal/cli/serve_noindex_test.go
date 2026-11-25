package cli

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testGlobal is the state a command gets for a project, without running one.
func testGlobal(t *testing.T, root string) *Global {
	t.Helper()
	return &Global{cfg: mustConfig(t, root), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// A client that launches `graphlume serve` on a project nobody has indexed
// must not see the process die. It gets a server that lists its tools and
// answers every call with what to run and where, and that starts answering
// from the index once one exists, with no restart and without ever indexing
// on its own.
func TestServeWithoutAnIndexAnswersWithWhatToRun(t *testing.T) {
	root := isolate(t)
	writeFiles(t, root, map[string]string{
		"main.go": "package main\n\nfunc main() { helper() }\n\nfunc helper() {}\n",
	})
	g := testGlobal(t, root)
	ctx := context.Background()

	srv, stop, err := openPendingAgentServer(ctx, g, agentServerOptions{})
	if err != nil {
		t.Fatalf("a project with no index must still get a server: %v", err)
	}
	defer stop()
	if len(srv.Tools()) == 0 {
		t.Fatal("the server lists no tools")
	}
	inv, err := srv.NewInvoker(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = inv.Close() }()

	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range srv.Tools() {
		// Whatever the arguments, including none where some are required:
		// the answer a client needs is the same.
		res, err := inv.Call(ctx, tool, json.RawMessage(`{}`))
		if err != nil {
			t.Errorf("%s: %v", tool, err)
			continue
		}
		if res.IsError {
			t.Errorf("%s: a missing index is not a tool failure:\n%s", tool, res.Text)
		}
		for _, want := range []string{
			"no index yet", abs, "graphlume init", "graphlume index", "graphlume resolve",
			"does not need restarting", "does not start indexing on its own",
		} {
			if !strings.Contains(res.Text, want) {
				t.Errorf("%s: the answer does not say %q:\n%s", tool, want, res.Text)
			}
		}
		var env struct {
			IndexStatus string   `json:"index_status"`
			Notes       []string `json:"notes"`
		}
		if err := json.Unmarshal(res.Structured, &env); err != nil || env.IndexStatus != "absent" || len(env.Notes) == 0 {
			t.Errorf("%s: structured answer = %s (%v), want index_status absent with notes", tool, res.Structured, err)
		}
	}

	// Nothing was indexed and nothing was written to the project.
	if _, err := os.Stat(filepath.Join(root, IndexDir)); !os.IsNotExist(err) {
		t.Errorf("serving a project with no index created %s in it (stat: %v)", IndexDir, err)
	}

	// The user runs what the answer said to run. The SAME server now answers
	// from the index.
	for _, step := range []string{"init", "index", "resolve"} {
		if out, err := run(t, root, step); err != nil {
			t.Fatalf("%s: %v\n%s", step, err, out)
		}
	}
	res, err := inv.Call(ctx, "overview", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		IndexStatus string `json:"index_status"`
	}
	if err := json.Unmarshal(res.Structured, &env); err != nil || env.IndexStatus == "absent" || env.IndexStatus == "" {
		t.Errorf("after the index was built the running server still answers %q:\n%s", env.IndexStatus, res.Text)
	}
	if strings.Contains(res.Text, "no index yet") {
		t.Errorf("after the index was built the running server still says there is none:\n%s", res.Text)
	}
}

// Who gets the running server and who gets the exit message.
func TestServeWithoutAnIndexOnlyForALaunchedStdioServer(t *testing.T) {
	for _, tc := range []struct {
		name           string
		http, terminal bool
		want           bool
	}{
		{"launched by a client over a pipe", false, false, true},
		{"typed in a terminal", false, true, false},
		{"--http from a terminal", true, true, false},
		{"--http from a supervisor", true, false, false},
	} {
		if got := serveWithoutIndex(tc.http, tc.terminal); got != tc.want {
			t.Errorf("%s: serve anyway = %v, want %v", tc.name, got, tc.want)
		}
	}

	// And the command itself: --http on a project with no index still exits
	// with the message a person at a terminal can act on.
	root := isolate(t)
	out, err := run(t, root, "serve", "--http", "127.0.0.1:0")
	if err == nil || !strings.Contains(err.Error(), "no index at") || !strings.Contains(err.Error(), "graphlume init") {
		t.Errorf("serve --http without an index: err = %v\n%s", err, out)
	}
	// --list-tools needs no index to say which tools exist.
	out, err = run(t, root, "serve", "--list-tools")
	if err != nil || !strings.Contains(out, "overview") {
		t.Errorf("serve --list-tools without an index: %v\n%s", err, out)
	}
}

func TestCdTargetQuotesOnlyWhenNeeded(t *testing.T) {
	for in, want := range map[string]string{
		"/home/dev/app":         "/home/dev/app",
		"/Users/dev/my project": `"/Users/dev/my project"`,
		`C:\Users\dev\app`:      `C:\Users\dev\app`,
		`C:\Program Files\app`:  `"C:\Program Files\app"`,
	} {
		if got := cdTarget(in); got != want {
			t.Errorf("cdTarget(%q) = %q, want %q", in, got, want)
		}
	}
}
