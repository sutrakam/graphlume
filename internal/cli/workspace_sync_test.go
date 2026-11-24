package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests run `workspace sync` and `workspace links` over two real,
// freshly indexed Go projects: an orders service that declares a route and a
// web client that calls it. Nothing is seeded, so the facts reach the workspace
// through the indexer, the registry reads, the member pool and the federation
// database, which is the path a user's command takes.

const ordersSource = `package main

import "net/http"

func listOrders(w http.ResponseWriter, r *http.Request) {}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/orders", listOrders)
	_ = http.ListenAndServe(":8080", mux)
}
`

const webSource = `package main

import "net/http"

func fetchOrders() error {
	_, err := http.Get("/api/orders")
	return err
}

func main() { _ = fetchOrders() }
`

// indexGoProject writes one Go file into dir and indexes it with the real
// commands.
func indexGoProject(t *testing.T, dir, source string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, dir, "init"); err != nil {
		t.Fatalf("init %s: %v\n%s", dir, err, out)
	}
	reindex(t, dir)
}

func reindex(t *testing.T, dir string) {
	t.Helper()
	if out, err := run(t, dir, "index"); err != nil {
		t.Fatalf("index %s: %v\n%s", dir, err, out)
	}
}

// workspaceFixture is a workspace directory holding the orders and web
// projects and an estate file naming both.
func workspaceFixture(t *testing.T) (ws string) {
	t.Helper()
	ws = isolate(t)
	indexGoProject(t, filepath.Join(ws, "orders"), ordersSource)
	indexGoProject(t, filepath.Join(ws, "web"), webSource)
	writeEstate(t, ws, "  - name: orders\n    index: orders\n  - name: web\n    index: web\n")
	return ws
}

func writeEstate(t *testing.T, ws, members string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(ws, "estate.yml"), []byte("members:\n"+members), 0o644); err != nil {
		t.Fatal(err)
	}
}

func syncWorkspace(t *testing.T, ws string, extra ...string) (string, error) {
	t.Helper()
	args := append([]string{"workspace", "sync", "--members", filepath.Join(ws, "estate.yml")}, extra...)
	return run(t, ws, args...)
}

func TestWorkspaceSyncLinksARouteCallAcrossMembers(t *testing.T) {
	ws := workspaceFixture(t)

	out, err := syncWorkspace(t, ws)
	if err != nil {
		t.Fatalf("workspace sync: %v\n%s", err, out)
	}
	for _, want := range []string{"orders", "web", "2 synced, 0 already up to date, 0 failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("sync output omits %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(ws, IndexDir, "workspace.db")); err != nil {
		t.Errorf("workspace database not written under the workspace root: %v", err)
	}

	out, err = run(t, ws, "workspace", "links", "web", "--json")
	if err != nil {
		t.Fatalf("workspace links: %v\n%s", err, out)
	}
	var res linksResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("links --json is not JSON: %v\n%s", err, out)
	}
	if len(res.Links) != 1 {
		t.Fatalf("want one link from web to orders, got %+v", res.Links)
	}
	l := res.Links[0]
	if l.From != "web" || l.To != "orders" || l.Kind != "http_call" || l.Key != "GET /api/orders" {
		t.Errorf("link = %+v", l)
	}
	// A route matched across two repositories is a heuristic and never exact.
	if l.Confidence == "exact" || l.Confidence == "" {
		t.Errorf("confidence = %q, a cross-member heuristic match must not be exact", l.Confidence)
	}
	if l.Stale || len(res.StaleServices) != 0 {
		t.Errorf("freshly synced link reported stale: %+v / %v", l, res.StaleServices)
	}

	// The text table and the other direction.
	out, err = run(t, ws, "workspace", "links", "web")
	if err != nil || !strings.Contains(out, "web") || !strings.Contains(out, "orders") ||
		!strings.Contains(out, "GET /api/orders") || !strings.Contains(out, "CONFIDENCE") {
		t.Errorf("links table: %v\n%s", err, out)
	}
	out, err = run(t, ws, "workspace", "links", "orders", "--upstream", "--json")
	if err != nil {
		t.Fatalf("links --upstream: %v\n%s", err, out)
	}
	res = linksResult{}
	if err := json.Unmarshal([]byte(out), &res); err != nil || len(res.Links) != 1 || res.Links[0].From != "web" {
		t.Errorf("upstream of orders = %+v (%v)", res.Links, err)
	}
	// Downstream of the provider is empty, and a non-matching key filters out.
	out, err = run(t, ws, "workspace", "links", "web", "--key", "GET /nope")
	if err != nil || !strings.Contains(out, "No links recorded") {
		t.Errorf("links --key miss: %v\n%s", err, out)
	}
}

func TestWorkspaceLinksReportsStaleAfterAMemberIsReindexed(t *testing.T) {
	ws := workspaceFixture(t)
	if out, err := syncWorkspace(t, ws); err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}

	// An index of unchanged files keeps its run, so the member changes first:
	// the new run replaces the one the workspace recorded.
	changed := strings.Replace(ordersSource, `mux.HandleFunc("GET /api/orders", listOrders)`,
		"mux.HandleFunc(\"GET /api/orders\", listOrders)\n\tmux.HandleFunc(\"GET /api/health\", listOrders)", 1)
	if err := os.WriteFile(filepath.Join(ws, "orders", "main.go"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	reindex(t, filepath.Join(ws, "orders"))

	out, err := run(t, ws, "workspace", "links", "web", "--json")
	if err != nil {
		t.Fatalf("links: %v\n%s", err, out)
	}
	var res linksResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Links) != 1 || !res.Links[0].Stale {
		t.Fatalf("link must be stale after orders was re-indexed: %+v", res.Links)
	}
	if len(res.StaleServices) != 1 || res.StaleServices[0] != "orders" {
		t.Errorf("stale services = %v, want [orders]", res.StaleServices)
	}
	text, _ := run(t, ws, "workspace", "links", "web")
	if !strings.Contains(text, "stale") || !strings.Contains(text, "workspace sync") {
		t.Errorf("text output does not tell the user to re-sync:\n%s", text)
	}

	// Re-syncing records the new run, and the staleness clears.
	if out, err := syncWorkspace(t, ws); err != nil {
		t.Fatalf("re-sync: %v\n%s", err, out)
	}
	out, _ = run(t, ws, "workspace", "links", "web", "--json")
	res = linksResult{}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Links) != 1 || res.Links[0].Stale || len(res.StaleServices) != 0 {
		t.Errorf("links still stale after re-sync: %+v %v", res.Links, res.StaleServices)
	}
}

func TestWorkspaceSyncReportsAFailedMemberAndContinues(t *testing.T) {
	ws := workspaceFixture(t)

	// Outside the workspace root, so it cannot be stored relative to it. A
	// project that was initialised but never indexed has no committed run.
	outside := t.TempDir()
	indexGoProject(t, outside, ordersSource)
	empty := filepath.Join(ws, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, empty, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	writeEstate(t, ws, "  - name: orders\n    index: orders\n  - name: web\n    index: web\n"+
		"  - name: elsewhere\n    index: "+outside+"\n  - name: empty\n    index: empty\n")

	out, err := syncWorkspace(t, ws, "--json")
	var ec *ExitCodeError
	if !errors.As(err, &ec) || ec.Code == 0 {
		t.Fatalf("a failed member must give a non-zero exit, got %v\n%s", err, out)
	}
	var res syncResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("sync --json is not JSON: %v\n%s", err, out)
	}
	if res.Synced != 2 || res.Failed != 2 {
		t.Fatalf("synced=%d failed=%d, want 2 and 2: %+v", res.Synced, res.Failed, res.Members)
	}
	reasons := map[string]string{}
	for _, m := range res.Members {
		reasons[m.Service] = m.Reason
	}
	if !strings.Contains(reasons["elsewhere"], "outside the workspace root") {
		t.Errorf("elsewhere reason = %q", reasons["elsewhere"])
	}
	if !strings.Contains(reasons["empty"], "no committed run") {
		t.Errorf("empty reason = %q", reasons["empty"])
	}

	// The members that did sync are linked as usual.
	out, err = run(t, ws, "workspace", "links", "web")
	if err != nil || !strings.Contains(out, "orders") {
		t.Errorf("links after a partial sync: %v\n%s", err, out)
	}
}

func TestWorkspaceSyncWithOneMemberAndTheOlderSyncIsUpToDate(t *testing.T) {
	ws := workspaceFixture(t)
	args := []string{"workspace", "sync", "--member", "orders=" + filepath.Join(ws, "orders")}
	out, err := run(t, ws, args...)
	if err != nil || !strings.Contains(out, "1 synced") {
		t.Fatalf("single-member sync: %v\n%s", err, out)
	}
	// Syncing the same state again is idempotent rather than an error.
	if out, err = run(t, ws, args...); err != nil {
		t.Fatalf("repeat sync: %v\n%s", err, out)
	}
}

func TestWorkspaceLinksWithoutADatabaseSaysToSync(t *testing.T) {
	ws := isolate(t)
	out, err := run(t, ws, "workspace", "links", "web")
	if err == nil || !strings.Contains(err.Error(), "graphlume workspace sync") {
		t.Fatalf("want guidance to run workspace sync, got %v\n%s", err, out)
	}
}

func TestWorkspaceLinksNamesKnownServicesForAnUnknownOne(t *testing.T) {
	ws := workspaceFixture(t)
	if out, err := syncWorkspace(t, ws); err != nil {
		t.Fatalf("sync: %v\n%s", err, out)
	}
	_, err := run(t, ws, "workspace", "links", "nope")
	if err == nil || !strings.Contains(err.Error(), "orders, web") {
		t.Fatalf("unknown service error should list known ones: %v", err)
	}
}

func TestWorkspaceSyncWithNoMembersExplainsHowToNameThem(t *testing.T) {
	ws := isolate(t)
	_, err := run(t, ws, "workspace", "sync")
	if err == nil || !strings.Contains(err.Error(), "--members") {
		t.Fatalf("want guidance on naming members, got %v", err)
	}
}

// With no member flags the services come from workspace detection, and each is
// read from the index under its own directory.
func TestWorkspaceSyncFromDetectedManifestServices(t *testing.T) {
	ws := isolate(t)
	indexGoProject(t, filepath.Join(ws, "services", "orders"), ordersSource)
	indexGoProject(t, filepath.Join(ws, "services", "web"), webSource)
	manifest := "services:\n  - name: orders\n    path: services/orders\n  - name: web\n    path: services/web\n    role: gateway\n"
	if err := os.MkdirAll(filepath.Join(ws, IndexDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, IndexDir, "workspace.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, ws, "workspace", "sync")
	if err != nil || !strings.Contains(out, "2 synced") {
		t.Fatalf("detected sync: %v\n%s", err, out)
	}
	out, err = run(t, ws, "workspace", "links", "web")
	if err != nil || !strings.Contains(out, "orders") {
		t.Errorf("links after detected sync: %v\n%s", err, out)
	}
}

func TestCallKeyReducesAURLToMethodAndPath(t *testing.T) {
	for _, c := range []struct{ method, path, want string }{
		{"get", "/api/orders", "GET /api/orders"},
		{"", "/api/orders", "/api/orders"},
		{"POST", "https://orders.internal:8080/v1/items?x=1#f", "POST /v1/items"},
		{"GET", "https://orders.internal", "GET /"},
		{"GET", "api/orders", "GET /api/orders"},
	} {
		if got := callKey(c.method, c.path); got != c.want {
			t.Errorf("callKey(%q,%q) = %q, want %q", c.method, c.path, got, c.want)
		}
	}
}
