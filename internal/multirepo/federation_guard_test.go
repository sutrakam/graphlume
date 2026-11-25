package multirepo

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/sutrakam/graphlume/internal/store"
)

func newTestWorkspace(t *testing.T) *WorkspaceManager {
	t.Helper()
	db, err := store.InitWorkspaceDB(context.Background(), filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewWorkspaceManager(db)
}

func svcFor(id string, run, seq int64) WorkspaceService {
	return WorkspaceService{
		ServiceID: id, RelPath: "services/" + id, Role: "service",
		DBPath: "/db/" + id + ".db", CommittedRunID: run, MutationSeq: seq,
	}
}

func contract(dir, kind, key, conf, source string) ExportedContract {
	return ExportedContract{Direction: dir, ContractKind: kind, CanonicalKey: key,
		Confidence: conf, Source: source, SymbolFQN: "s", FilePath: "f", Line: 1}
}

func mustSync(t *testing.T, m *WorkspaceManager, s WorkspaceService, cs ...ExportedContract) {
	t.Helper()
	if err := m.SyncService(context.Background(), s, cs); err != nil {
		t.Fatalf("sync %s: %v", s.ServiceID, err)
	}
}

func linksFrom(t *testing.T, m *WorkspaceManager, id string) LinkPage {
	t.Helper()
	p, err := m.FindDownstreamLinks(context.Background(), id, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWildcardDoesNotMatchUnrelatedRoutes(t *testing.T) {
	cases := []struct {
		call, route string
		want        bool
	}{
		{"GET /orders/:id", "GET /users/{id}", false},     // literal segment differs
		{"GET /:x", "GET /users", false},                  // call side is nothing but a wildcard
		{"POST /%s/%s", "POST /a/b", false},               // same
		{"GET /users/%s", "GET /users/{id}", true},        // literals line up
		{"GET /users/:id", "GET /users/me/orders", false}, // a wildcard is one segment, not a tail
		{"GET /users/v%d", "GET /users/v1", false},        // partial wildcard is a literal
		{"GET /a//b", "GET /a/{x}/b", false},              // never an empty segment
	}
	for _, c := range cases {
		got, _ := matchHTTPKeys(c.call, c.route)
		if got != c.want {
			t.Errorf("matchHTTPKeys(%q, %q) = %v, want %v", c.call, c.route, got, c.want)
		}
	}

	m := newTestWorkspace(t)
	mustSync(t, m, svcFor("users", 1, 0), contract("provides", "http_route", "GET /users/{id}", "high", "heuristic"))
	mustSync(t, m, svcFor("web", 1, 0),
		contract("consumes", "http_route", "GET /orders/:id", "high", "heuristic"),
		contract("consumes", "http_route", "GET /:x/:y", "high", "heuristic"))
	if p := linksFrom(t, m, "web"); len(p.Links) != 0 {
		t.Fatalf("unrelated routes linked: %+v", p.Links)
	}
}

func TestLinkConfidenceCaps(t *testing.T) {
	ctx := context.Background()
	m := newTestWorkspace(t)

	// Two compiler-proven contracts on a literal route with methods: exact.
	mustSync(t, m, svcFor("api", 1, 0),
		contract("provides", "http_route", "GET /v1/ping", "exact", "scip"),
		contract("provides", "http_route", "GET /v1/items/{id}", "exact", "compiler"),
		contract("provides", "http_route", "/v1/nomethod", "high", "heuristic"),
		contract("writes", "db_table", "orders", "high", "heuristic"),
		contract("reads", "db_table", "audit", "high", "heuristic"),
		contract("provides", "grpc_method", "pay.Pay/Charge", "medium", "heuristic"))
	mustSync(t, m, svcFor("client", 1, 0),
		contract("consumes", "http_route", "GET /v1/ping", "exact", "scip"),
		contract("consumes", "http_route", "GET /v1/items/%s", "exact", "compiler"),
		contract("consumes", "http_route", "GET /v1/nomethod", "high", "heuristic"),
		contract("reads", "db_table", "orders", "high", "heuristic"),
		contract("reads", "db_table", "audit", "high", "heuristic"),
		contract("consumes", "grpc_method", "pay.Pay/Charge", "high", "heuristic"))

	want := map[string]string{
		"exact_route_match":         "exact",  // both compiler evidence, literal, with methods
		"normalized_route_template": "medium", // template match is inference even between exact contracts
		"route_match_no_method":     "medium", // method-less matches any method
		"shared_table_name:orders":  "medium", // writer/reader pair
		"shared_table_name:audit":   "low",    // reader/reader
		"exact_proto_fqn":           "medium", // min(high, medium), not promoted to high
	}
	got := map[string]string{}
	for _, svc := range []string{"api", "client"} {
		p, err := m.FindDownstreamLinks(ctx, svc, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range p.Links {
			key := l.MatchRule
			if l.LinkKind == "shared_table" {
				var k string
				_ = m.db.QueryRowContext(ctx, `SELECT canonical_key FROM exported_contracts WHERE id = ?`, l.SrcContractID).Scan(&k)
				key += ":" + k
			}
			got[key] = l.Confidence
		}
	}
	for k, c := range want {
		if got[k] != c {
			t.Errorf("%s: confidence %q, want %q (all: %v)", k, got[k], c, got)
		}
	}
}

func TestHeuristicContractsCannotBeExact(t *testing.T) {
	m := newTestWorkspace(t)
	err := m.SyncService(context.Background(), svcFor("a", 1, 0),
		[]ExportedContract{contract("provides", "http_route", "GET /x", "exact", "heuristic")})
	if !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("err = %v, want ErrInvalidContract", err)
	}
	err = m.SyncService(context.Background(), svcFor("a", 1, 0),
		[]ExportedContract{contract("provides", "http_route", "GET /x", "certain", "scip")})
	if !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("unknown confidence: err = %v, want ErrInvalidContract", err)
	}

	// A legacy exact row without provenance is demoted when matched.
	a := contractRow{kind: "http_route", direction: "consumes", key: "GET /x", confidence: "exact", source: "heuristic"}
	b := contractRow{kind: "http_route", direction: "provides", key: "GET /x", confidence: "exact", source: "heuristic"}
	if _, conf, _, ok := matchContractPair(a, b); !ok || conf == "exact" {
		t.Fatalf("two heuristic exact rows linked as %q", conf)
	}
}

func TestSyncServiceValidation(t *testing.T) {
	m := newTestWorkspace(t)
	bad := []WorkspaceService{
		func() WorkspaceService { s := svcFor("", 1, 0); return s }(),
		func() WorkspaceService { s := svcFor("a", 1, 0); s.RelPath = "../escape"; return s }(),
		func() WorkspaceService { s := svcFor("a", 1, 0); s.RelPath = "/abs"; return s }(),
		func() WorkspaceService { s := svcFor("a", 1, 0); s.DBPath = "rel/db"; return s }(),
		func() WorkspaceService { s := svcFor("a", 1, 0); s.DBPath = "/db/../x.db"; return s }(),
		func() WorkspaceService { s := svcFor("a", 1, 0); s.Role = "weird"; return s }(),
	}
	for i, s := range bad {
		if err := m.SyncService(context.Background(), s, nil); !errors.Is(err, ErrInvalidService) {
			t.Errorf("case %d: err = %v, want ErrInvalidService", i, err)
		}
	}
}

func TestSyncServiceRefusesOlderState(t *testing.T) {
	ctx := context.Background()
	m := newTestWorkspace(t)
	mustSync(t, m, svcFor("a", 5, 3), contract("provides", "http_route", "GET /new", "high", "heuristic"))

	for _, old := range [][2]int64{{4, 99}, {5, 2}} {
		err := m.SyncService(ctx, svcFor("a", old[0], old[1]),
			[]ExportedContract{contract("provides", "http_route", "GET /old", "high", "heuristic")})
		if !errors.Is(err, ErrStaleSync) {
			t.Fatalf("run %d seq %d: err = %v, want ErrStaleSync", old[0], old[1], err)
		}
	}
	var n int
	_ = m.db.QueryRowContext(ctx, `SELECT count(*) FROM exported_contracts WHERE canonical_key = 'GET /new'`).Scan(&n)
	var run int64
	_ = m.db.QueryRowContext(ctx, `SELECT committed_run_id FROM workspace_services WHERE service_id = 'a'`).Scan(&run)
	if n != 1 || run != 5 {
		t.Fatalf("state regressed: new contracts=%d run=%d", n, run)
	}
	// Re-syncing the same state, and a newer one, both succeed.
	mustSync(t, m, svcFor("a", 5, 3), contract("provides", "http_route", "GET /new", "high", "heuristic"))
	mustSync(t, m, svcFor("a", 6, 0), contract("provides", "http_route", "GET /newer", "high", "heuristic"))
}

func TestFindLinksLimitAndFanoutCap(t *testing.T) {
	ctx := context.Background()
	m := newTestWorkspace(t)
	const providers = maxLinksPerContract + 5
	for i := 0; i < providers; i++ {
		id := fmt.Sprintf("p%03d", i)
		mustSync(t, m, svcFor(id, 1, 0), contract("provides", "http_route", "GET /hot/path", "high", "heuristic"))
	}
	mustSync(t, m, svcFor("caller", 1, 0), contract("consumes", "http_route", "GET /hot/path", "high", "heuristic"))

	p, err := m.FindDownstreamLinks(ctx, "caller", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Links) != 10 || !p.Truncated {
		t.Fatalf("limit: got %d links truncated=%v, want 10 and true", len(p.Links), p.Truncated)
	}
	if !p.FanoutTruncated {
		t.Error("sync-time fan-out cap was not recorded")
	}
	all, _ := m.FindDownstreamLinks(ctx, "caller", "", 1000)
	if len(all.Links) != maxLinksPerContract {
		t.Errorf("stored links = %d, want the cap %d", len(all.Links), maxLinksPerContract)
	}
}

func TestFindLinksReportsStaleServices(t *testing.T) {
	ctx := context.Background()
	m := newTestWorkspace(t)
	current := map[string]int64{"api": 1, "web": 1}
	m.WithRunLookup(func(_ context.Context, id string) (int64, bool) { r, ok := current[id]; return r, ok })

	mustSync(t, m, svcFor("api", 1, 0), contract("provides", "http_route", "GET /v1/ping", "high", "heuristic"))
	mustSync(t, m, svcFor("web", 1, 0), contract("consumes", "http_route", "GET /v1/ping", "high", "heuristic"))

	p, _ := m.FindDownstreamLinks(ctx, "web", "", 0)
	if len(p.Links) != 1 || p.Links[0].Stale || len(p.StaleServices) != 0 {
		t.Fatalf("fresh workspace reported stale: %+v", p)
	}

	current["api"] = 2 // api was re-indexed after its contracts were synced
	p, _ = m.FindDownstreamLinks(ctx, "web", "", 0)
	if !p.Links[0].Stale || len(p.StaleServices) != 1 || p.StaleServices[0] != "api" {
		t.Fatalf("re-indexed service not reported stale: %+v", p)
	}
}

// A rebuilt index restarts its run ids at 1. Its first run started after the
// old index's last one, so the sync is newer, not older; a late sync from the
// old index is still refused.
func TestSyncServiceAcceptsRebuiltIndex(t *testing.T) {
	ctx := context.Background()
	m := newTestWorkspace(t)
	old := svcFor("a", 57, 4)
	old.RunStartedUnix = 1000
	mustSync(t, m, old, contract("provides", "http_route", "GET /old", "high", "heuristic"))

	rebuilt := svcFor("a", 1, 0)
	rebuilt.RunStartedUnix = 2000
	mustSync(t, m, rebuilt, contract("provides", "http_route", "GET /rebuilt", "high", "heuristic"))

	late := svcFor("a", 58, 0)
	late.RunStartedUnix = 1500
	if err := m.SyncService(ctx, late, nil); !errors.Is(err, ErrStaleSync) {
		t.Fatalf("late sync from the replaced index: err = %v, want ErrStaleSync", err)
	}
	var run int64
	_ = m.db.QueryRowContext(ctx, `SELECT committed_run_id FROM workspace_services WHERE service_id = 'a'`).Scan(&run)
	if run != 1 {
		t.Fatalf("committed_run_id = %d, want the rebuilt index's 1", run)
	}
}

// A rebuilt index can come back at exactly the run id and mutation counter the
// workspace recorded for the index it replaced. Its start time differs, and
// that alone must mark the link stale.
func TestPositionLookupSeesRebuiltIndex(t *testing.T) {
	m := newTestWorkspace(t)
	p := svcFor("p", 1, 0)
	p.RunStartedUnix = 1000
	mustSync(t, m, p, contract("provides", "http_route", "GET /orders", "high", "heuristic"))
	c := svcFor("c", 1, 0)
	c.RunStartedUnix = 1000
	mustSync(t, m, c, contract("consumes", "http_route", "GET /orders", "high", "heuristic"))

	now := map[string]MemberPosition{
		"p": {RunStartedUnix: 1000, RunID: 1},
		"c": {RunStartedUnix: 1000, RunID: 1},
	}
	m.WithPositionLookup(func(_ context.Context, id string) (MemberPosition, bool) {
		pos, ok := now[id]
		return pos, ok
	})
	if page := linksFrom(t, m, "c"); len(page.Links) != 1 || page.Links[0].Stale {
		t.Fatalf("unchanged members reported stale: %+v", page)
	}
	now["p"] = MemberPosition{RunStartedUnix: 2000, RunID: 1}
	page := linksFrom(t, m, "c")
	if len(page.Links) != 1 || !page.Links[0].Stale || len(page.StaleServices) != 1 || page.StaleServices[0] != "p" {
		t.Fatalf("rebuilt provider not reported stale: %+v", page)
	}
}

// Two rebuilds in the same second share a start time; a differing content
// digest still marks the member stale, and an unknown digest does not.
func TestPositionLookupComparesDigest(t *testing.T) {
	m := newTestWorkspace(t)
	p := svcFor("p", 1, 0)
	p.RunStartedUnix, p.Digest = 1000, "aaa"
	mustSync(t, m, p, contract("provides", "http_route", "GET /orders", "high", "heuristic"))
	mustSync(t, m, svcFor("c", 1, 0), contract("consumes", "http_route", "GET /orders", "high", "heuristic"))

	cur := MemberPosition{RunStartedUnix: 1000, RunID: 1, Digest: "aaa"}
	m.WithPositionLookup(func(_ context.Context, id string) (MemberPosition, bool) {
		if id == "p" {
			return cur, true
		}
		return MemberPosition{RunID: 1}, true
	})
	if page := linksFrom(t, m, "c"); page.Links[0].Stale {
		t.Fatal("same digest reported stale")
	}
	cur.Digest = ""
	if page := linksFrom(t, m, "c"); page.Links[0].Stale {
		t.Fatal("unknown digest reported stale")
	}
	cur.Digest = "bbb"
	if page := linksFrom(t, m, "c"); !page.Links[0].Stale {
		t.Fatal("different content digest not reported stale")
	}
}
