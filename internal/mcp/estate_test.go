package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/multirepo"
	"github.com/sutrakam/graphlume/pkg/model"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeEstate answers from canned reports. The federation itself is tested in
// internal/multirepo and end to end over real indexes in internal/cli; these
// tests are about the tool: where it is registered, the envelope, paging, and
// the rules the envelope enforces on its own.
type fakeEstate struct {
	links   *multirepo.Report
	callers *multirepo.SystemCallersReport
	impact  *multirepo.SystemImpactReport
	pick    multirepo.SymbolPick
	// asked records the impact options the tool passed.
	asked multirepo.ImpactOptions
}

func (f *fakeEstate) Members() []multirepo.EstateMember {
	return []multirepo.EstateMember{{Name: "api"}, {Name: "app"}, {Name: "gone"}, {Name: "lib"}, {Name: "web"}}
}

func (f *fakeEstate) Unreadable(context.Context) map[string]string {
	return map[string]string{"gone": "no index at /estate/gone/.graphlume/index.db"}
}

func (f *fakeEstate) Links(context.Context) (*multirepo.Report, error) { return f.links, nil }

// pickOrRefuse resolves symbol against a member list the way the service
// does, so refusals are the real errors.
func (f *fakeEstate) pickOrRefuse(ctx context.Context, symbol string) error {
	members := []multirepo.Member{{Name: "gone", Unavailable: "no index at /estate/gone/.graphlume/index.db"}, {Name: "lib"}}
	if strings.HasPrefix(symbol, "lib:") {
		return nil
	}
	_, err := multirepo.PickSymbol(ctx, members, symbol, "", 50)
	return err
}

func (f *fakeEstate) Callers(ctx context.Context, symbol, _ string, _ int) (*multirepo.SystemCallersReport, multirepo.SymbolPick, error) {
	if err := f.pickOrRefuse(ctx, symbol); err != nil {
		return nil, multirepo.SymbolPick{}, err
	}
	return f.callers, f.pick, nil
}

func (f *fakeEstate) Impact(ctx context.Context, symbol, _ string, opts multirepo.ImpactOptions) (*multirepo.SystemImpactReport, multirepo.SymbolPick, error) {
	if err := f.pickOrRefuse(ctx, symbol); err != nil {
		return nil, multirepo.SymbolPick{}, err
	}
	f.asked = opts
	return f.impact, f.pick, nil
}

func newFakeEstate() *fakeEstate {
	target := multirepo.SymbolRef{Member: "lib", Name: "Do", Qualified: "Do", Kind: "function", File: "lib.go", Line: 4}
	warning := "web requires example.com/lib v1.4.2; lib's index records no commit"
	return &fakeEstate{
		links: &multirepo.Report{
			Members: []multirepo.MemberStatus{
				{Name: "api", Available: true}, {Name: "app", Available: true},
				{Name: "gone", Unavailable: "no index at /estate/gone/.graphlume/index.db"},
				{Name: "lib", Available: true}, {Name: "web", Available: true},
			},
			Edges: []multirepo.CrossRepoEdge{
				{FromMember: "web", FromSymbol: "useOrders", FromFile: "src/orders.ts", FromLine: 12,
					Method: "GET", Path: "/api/orders", ToMember: "api", ToEndpoint: "GET /api/orders",
					ToEndpointFile: "routes.go", ToEndpointLine: 31, ToHandler: "ListOrders",
					Match: "literal", Confidence: model.ConfidenceMedium},
				// A grade the federation never emits for a crossing, to show the
				// tool does not pass it on either.
				{FromMember: "web", FromSymbol: "useOrders", FromFile: "src/orders.ts", FromLine: 14,
					Method: "POST", Path: "/api/payments/42", ToMember: "api", ToEndpoint: "POST /api/payments/{id}",
					ToEndpointFile: "routes.go", ToEndpointLine: 32, ToHandler: "Pay",
					Match: "parameterised", Confidence: model.ConfidenceExact},
			},
			SharedTables: []multirepo.SharedTable{{
				Table:        "orders",
				Participants: []multirepo.TableParticipant{{Member: "api"}, {Member: "web"}},
				Writers:      []string{"api"}, Readers: []string{"web"}, Confidence: model.ConfidenceMedium,
			}},
			Unjoined: multirepo.Unjoined{UnavailableMembers: []string{"gone"}},
			GoModules: &multirepo.GoModuleReport{
				Pairs: []multirepo.GoModulePair{{Importer: "web", Provider: "lib", Module: "example.com/lib",
					Links: 1, Warnings: []string{warning}}},
				Links: []multirepo.GoLink{
					{FromMember: "app", FromSymbol: "run", FromFile: "main.go", FromLine: 12, Call: "lib.Do",
						CallKind: "function", ToMember: "lib", Module: "example.com/lib", ToSymbol: "Do",
						ToKind: "function", ToFile: "lib.go", ToLine: 4, Confidence: model.ConfidenceHigh},
					{FromMember: "app", FromSymbol: "run", FromFile: "main.go", FromLine: 10, Call: "c.Send",
						CallKind: "method", ToMember: "lib", Module: "example.com/lib", ToSymbol: "Client.Send",
						ToKind: "method", ToFile: "lib.go", ToLine: 10, Confidence: model.ConfidenceMedium},
				},
			},
		},
		callers: &multirepo.SystemCallersReport{
			Target: target,
			Callers: []multirepo.SystemCaller{
				{Member: "lib", Caller: "Wrap", File: "wrap.go", Line: 4, Confidence: model.ConfidenceExact, Leg: multirepo.LegLocal, Source: "compiler"},
				{Member: "app", Caller: "run", File: "main.go", Line: 12, Confidence: model.ConfidenceHigh, Leg: multirepo.LegGoModule, Call: "lib.Do"},
				{Member: "app", Caller: "viaAlias", File: "other.go", Line: 7, Confidence: model.ConfidenceHigh, Leg: multirepo.LegGoModule, Call: "l2.Do"},
				{Member: "web", Caller: "Fetch", File: "web.go", Line: 8, Confidence: model.ConfidenceMedium, Leg: multirepo.LegGoModule, Call: "lib.Do"},
			},
			Warnings:           []string{warning},
			UnavailableMembers: []string{"gone"},
		},
		impact: &multirepo.SystemImpactReport{
			Target: target, MaxDepth: 2, MinConfidence: model.ConfidenceMedium,
			Rows: []multirepo.SystemImpactRow{
				{Member: "app", Symbol: "run", File: "main.go", Line: 12, Depth: 1, Confidence: model.ConfidenceHigh, Via: multirepo.LegGoModule, Reaches: "lib:Do"},
				{Member: "lib", Symbol: "Wrap", File: "wrap.go", Line: 4, Depth: 1, Confidence: model.ConfidenceExact, Via: "CALLS", Reaches: "lib:Do"},
				{Member: "app", Symbol: "handler", File: "main.go", Line: 15, Depth: 2, Confidence: model.ConfidenceExact, Via: "CALLS", Reaches: "app:run"},
			},
			Members: []string{"lib", "app"}, Warnings: []string{warning}, UnavailableMembers: []string{"gone"},
		},
		pick: multirepo.SymbolPick{Target: target, Name: "Do"},
	}
}

func withEstate(e EstateService) fullOption { return func(d *Deps) { d.Estate = e } }

// estateEnvelope calls estate and decodes the structured answer.
func estateEnvelope(t *testing.T, sess *sdk.ClientSession, args map[string]any) (Response, string) {
	t.Helper()
	res := callTool(t, sess, "estate", args)
	text := resultText(res)
	if res.IsError {
		t.Fatalf("estate %v failed:\n%s", args, text)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var r Response
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return r, text
}

func TestEstateIsListedOnlyWhenAnEstateIsConfigured(t *testing.T) {
	has := func(srv *Server) bool {
		for _, n := range srv.Tools() {
			if n == "estate" {
				return true
			}
		}
		return false
	}
	if !has(newFullServer(t, seedChain, withEstate(newFakeEstate())).srv) {
		t.Error("full profile with an estate does not list estate")
	}
	if has(newFullServer(t, seedChain).srv) {
		t.Error("full profile without an estate lists estate")
	}
	for _, p := range []config.MCPProfile{config.MCPMinimal, config.MCPStandard, config.MCPCompact} {
		if has(newFullServer(t, seedChain, withProfile(p), withEstate(newFakeEstate())).srv) {
			t.Errorf("%s profile lists estate", p)
		}
	}
	for _, list := range [][]string{fullOnlyTools()} {
		for _, n := range list {
			if n == "estate" {
				t.Error("estate is in fullOnlyTools, which every full server registers; it belongs in conditionalTools")
			}
		}
	}
	m, st := ProfileTools()
	for _, n := range append(m, st...) {
		if n == "estate" {
			t.Errorf("estate is on a default profile")
		}
	}
}

func TestEstateLinksNameMembersAndNeverClaimExactAcrossThem(t *testing.T) {
	fs := newFullServer(t, seedChain, withEstate(newFakeEstate()))
	r, text := estateEnvelope(t, fs.sess, map[string]any{"action": "links"})

	if len(r.Items) != 5 {
		t.Fatalf("items = %d, want 2 HTTP + 2 Go + 1 table:\n%s", len(r.Items), text)
	}
	members := map[string]bool{"api": true, "app": true, "lib": true, "web": true}
	for _, it := range r.Items {
		if it.Confidence == "" {
			t.Errorf("%s %s carries no confidence", it.Kind, it.Name)
		}
		if it.Confidence == string(model.ConfidenceExact) {
			t.Errorf("%s %s crosses a repository boundary and is exact", it.Kind, it.Name)
		}
		m, _, ok := strings.Cut(it.Name, ":")
		named := ok
		for _, part := range strings.Split(m, ",") {
			named = named && members[part]
		}
		if !named {
			t.Errorf("row %q does not name its member", it.Name)
		}
	}
	kinds := []string{}
	for _, it := range r.Items {
		kinds = append(kinds, it.Kind+" "+it.Name)
	}
	want := "http_link web:useOrders,http_link web:useOrders,go_method_link app:run,go_function_link app:run,shared_table api,web:orders"
	if got := strings.Join(kinds, ","); got != want {
		t.Errorf("order =\n%s\nwant\n%s", got, want)
	}
	for _, want := range []string{
		"INCOMPLETE: 1 member(s) could not be read (gone)",
		"member gone: no index at /estate/gone/.graphlume/index.db",
		"web requires example.com/lib v1.4.2",
		"LOWER BOUND",
		"never `exact`",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("links answer omits %q:\n%s", want, text)
		}
	}

	again := resultText(callTool(t, fs.sess, "estate", map[string]any{"action": "links"}))
	if strip(again) != strip(text) {
		t.Errorf("two identical questions answered differently:\n%s\n---\n%s", text, again)
	}
}

// strip drops the provenance lines that carry a clock reading.
func strip(text string) string {
	var keep []string
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, "generated_at") || strings.Contains(l, "age") {
			continue
		}
		keep = append(keep, l)
	}
	return strings.Join(keep, "\n")
}

func TestEstateCallersPagesAndKeepsLocalGrades(t *testing.T) {
	fs := newFullServer(t, seedChain, withEstate(newFakeEstate()))
	r, text := estateEnvelope(t, fs.sess, map[string]any{"action": "callers", "symbol": "lib:Do", "limit": 2})
	if len(r.Items) != 2 || r.Page == nil || r.Page.Total != 4 || r.Page.NextOffset != 2 {
		t.Fatalf("page = %+v items %d:\n%s", r.Page, len(r.Items), text)
	}
	if r.Items[0].Name != "lib:Wrap" || r.Items[0].Confidence != "exact" {
		t.Errorf("a caller inside the symbol's own member keeps its recorded grade: %+v", r.Items[0])
	}
	if r.Items[1].Name != "app:run" || r.Items[1].Source != multirepo.LegGoModule {
		t.Errorf("second row = %+v", r.Items[1])
	}
	for _, want := range []string{"4 caller(s) of lib:Do", "3 across a repository boundary", "INCOMPLETE", "gone", "LOWER BOUND"} {
		if !strings.Contains(text, want) {
			t.Errorf("callers answer omits %q:\n%s", want, text)
		}
	}

	r, _ = estateEnvelope(t, fs.sess, map[string]any{"action": "callers", "symbol": "lib:Do", "offset": 2, "limit": 2})
	if len(r.Items) != 2 || r.Items[0].Name != "app:viaAlias" || r.Items[1].Name != "web:Fetch" {
		t.Errorf("second page = %+v", r.Items)
	}

	r, text = estateEnvelope(t, fs.sess, map[string]any{"action": "callers", "symbol": "lib:Do", "min_confidence": "high"})
	if len(r.Items) != 3 || !strings.Contains(text, "1 lower-confidence result(s) were omitted") {
		t.Errorf("floor high kept %d rows:\n%s", len(r.Items), text)
	}
}

func TestEstateImpactUsesTheCLIDefaultsAndCapsCrossings(t *testing.T) {
	fake := newFakeEstate()
	fs := newFullServer(t, seedChain, withEstate(fake))
	r, text := estateEnvelope(t, fs.sess, map[string]any{"action": "impact", "symbol": "lib:Do"})
	if fake.asked.MaxDepth != multirepo.DefaultSystemImpactDepth || fake.asked.MinConfidence != model.ConfidenceMedium ||
		fake.asked.MaxRows != multirepo.DefaultSystemImpactRows || len(fake.asked.Kinds) == 0 {
		t.Errorf("impact asked %+v; want the defaults of 'graphlume system impact'", fake.asked)
	}
	got := map[string]string{}
	for _, it := range r.Items {
		got[it.Name] = it.Confidence
	}
	// lib:Wrap is inside the symbol's member; app:handler is in another
	// member, reached across the boundary, so exact is not allowed there.
	if got["lib:Wrap"] != "exact" || got["app:handler"] != "high" || got["app:run"] != "high" {
		t.Errorf("confidences = %v", got)
	}
	if !strings.Contains(text, "members reached: lib, app") || !strings.Contains(text, "LOWER BOUND") {
		t.Errorf("impact answer:\n%s", text)
	}

	estateEnvelope(t, fs.sess, map[string]any{"action": "impact", "symbol": "lib:Do", "depth": 5, "min_confidence": "low"})
	if fake.asked.MaxDepth != 5 || fake.asked.MinConfidence != model.ConfidenceLow {
		t.Errorf("impact asked %+v", fake.asked)
	}

	for _, args := range []map[string]any{
		{"action": "impact", "symbol": "lib:Do", "depth": 9},
		{"action": "impact", "symbol": "lib:Do", "min_confidence": "pretty_sure"},
		{"action": "impact"},
		{"action": "sideways"},
		{},
		{"action": "callers", "symbol": "Do"},
		{"action": "callers", "symbol": "nobody:Do"},
	} {
		if res := callTool(t, fs.sess, "estate", args); !res.IsError {
			t.Errorf("%v was accepted:\n%s", args, resultText(res))
		}
	}
}

// A member that cannot be read is an answer about the estate, not a failure
// of the tool.
func TestEstateUnreadableMemberIsReportedNotFatal(t *testing.T) {
	fs := newFullServer(t, seedChain, withEstate(newFakeEstate()))
	res := callTool(t, fs.sess, "estate", map[string]any{"action": "callers", "symbol": "gone:Do"})
	text := resultText(res)
	if res.IsError {
		t.Fatalf("an unreadable member was reported as a tool failure:\n%s", text)
	}
	if !strings.Contains(text, `member "gone" cannot be read: no index at`) {
		t.Errorf("answer does not say why:\n%s", text)
	}
}

func TestEstateThroughTheFrontDoor(t *testing.T) {
	fs := newFullServer(t, seedChain, withProfile(config.MCPCompact), withEstate(newFakeEstate()))
	res := callTool(t, fs.sess, "graphlume", map[string]any{"action": "estate", "kind": "callers", "name": "lib:Do"})
	if text := resultText(res); res.IsError || !strings.Contains(text, "4 caller(s) of lib:Do") {
		t.Errorf("front door estate callers:\n%s", text)
	}
	res = callTool(t, fs.sess, "graphlume", map[string]any{"action": "estate"})
	if text := resultText(res); res.IsError || !strings.Contains(text, "HTTP route link(s)") {
		t.Errorf("front door estate defaults to links:\n%s", text)
	}

	bare := newFullServer(t, seedChain, withProfile(config.MCPCompact))
	if res := callTool(t, bare.sess, "graphlume", map[string]any{"action": "estate"}); !res.IsError ||
		!strings.Contains(resultText(res), "no estate is configured") {
		t.Errorf("front door without an estate:\n%s", resultText(res))
	}
}

// A token's allow-list is checked against ToolsOf before any server exists,
// and against a real server by Scoped.
func TestEstateCanBeNamedInAScope(t *testing.T) {
	found := false
	for _, n := range ToolsOf(config.MCPFull) {
		found = found || n == "estate"
	}
	if !found {
		t.Fatal("ToolsOf(full) does not name estate, so a token could not be scoped to it")
	}
	if !ValidToolName("estate") {
		t.Fatal("estate is not a valid tool name")
	}
	base := newFullServer(t, seedChain, withEstate(newFakeEstate())).srv
	scoped, err := base.Scoped(config.MCPFull, []string{"estate"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(scoped.Tools(), ","); got != "estate" {
		t.Errorf("scoped tools = %s", got)
	}
	if _, err := newFullServer(t, seedChain).srv.Scoped(config.MCPFull, []string{"estate"}); err == nil {
		t.Error("a scope naming estate on a server with no estate was accepted")
	}
}
