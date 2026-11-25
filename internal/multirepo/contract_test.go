package multirepo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// ------------------------------------------------------------------ fixtures

func contracts(t *testing.T, members []Member, opts ...Option) *ContractReport {
	t.Helper()
	opts = append([]Option{
		WithClock(func() time.Time { return fixedClock }),
		WithRunningVersion(testVersion),
	}, opts...)
	f, err := New(opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rep, err := f.Contracts(context.Background(), members)
	if err != nil {
		t.Fatalf("Contracts: %v", err)
	}
	return rep
}

// call is one literal outbound call the indexer did not bind in its own
// repository.
func call(line int, method, path string) registry.ClientCallUsage {
	return registry.ClientCallUsage{
		CallID: int64(line), FilePath: "src/client.ts", Line: line,
		Method: method, Path: path, Client: "fetch", Caller: "load", Binding: "unbound",
	}
}

// dynamicCall is a call whose target was assembled at run time; prefix is the
// literal text before the first run-time part.
func dynamicCall(line int, method, prefix string) registry.ClientCallUsage {
	c := call(line, method, prefix)
	c.Dynamic = true
	return c
}

func route(id int64, signature string) registry.Endpoint {
	return registry.Endpoint{
		SymbolID: id, Signature: signature,
		FilePath: "internal/http/routes.go", Line: int(id),
		Handler: fmt.Sprintf("Handle%d", id), HandlerID: id + 1000,
		HandlerPath: "internal/http/handlers.go",
	}
}

// clientAndServer is the smallest system a contract can be checked across: one
// member that only calls, one that only answers.
func clientAndServer(calls []registry.ClientCallUsage, endpoints []registry.Endpoint) []Member {
	return []Member{
		{Name: "web", IndexPath: "/idx/web.db", RunID: 1, Source: &fakeSource{
			run: readyRun(1, "8f3c1ab9de", at(2*time.Hour)), calls: calls,
			languages: map[string]int{"typescript": 12},
		}},
		{Name: "api", IndexPath: "/idx/api.db", RunID: 1, Source: &fakeSource{
			run: readyRun(1, "a19d7e04bc", at(3*time.Hour)), endpoints: endpoints,
			languages: map[string]int{"go": 30},
		}},
	}
}

func findingFor(t *testing.T, rep *ContractReport, path string) ContractFinding {
	t.Helper()
	for _, fd := range rep.Findings {
		if fd.Call.Path == path {
			return fd
		}
	}
	t.Fatalf("no finding for the call to %q; findings: %+v", path, rep.Findings)
	return ContractFinding{}
}

// findingAt finds a finding by the line of its call, for fixtures where two
// calls share a path.
func findingAt(t *testing.T, rep *ContractReport, line int) ContractFinding {
	t.Helper()
	for _, fd := range rep.Findings {
		if fd.Call.Line == line {
			return fd
		}
	}
	t.Fatalf("no finding for the call on line %d; findings: %+v", line, rep.Findings)
	return ContractFinding{}
}

func uncalledSignatures(rep *ContractReport) []string {
	out := make([]string, 0, len(rep.UncalledRoutes))
	for _, r := range rep.UncalledRoutes {
		out = append(out, r.Member+": "+r.Endpoint)
	}
	return out
}

// flat collapses every run of whitespace, so an assertion on prose does not
// depend on where a line happened to fold.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

// ------------------------------------------------------- what IS reported

// TestContractsReportsEachKindOfDisagreement is the feature: four ways a call
// can fail to line up with the routes, each named, graded and located on both
// sides.
func TestContractsReportsEachKindOfDisagreement(t *testing.T) {
	rep := contracts(t, clientAndServer(
		[]registry.ClientCallUsage{
			call(10, "GET", "/api/orders"),                              // lines up
			call(20, "DELETE", "/api/orders"),                           // literal path, wrong verb
			call(30, "PUT", "/api/orders/42"),                           // parameterised path, wrong verb
			call(40, "GET", "/api/order"),                               // no such route
			call(50, "GET", "/api/orders/42/items"),                     // one path variable short
			call(60, "GET", "/gateway/health"),                          // only under an unseen prefix
			call(70, "POST", "https://collector.example.com/v1/traces"), // out of the system
		},
		[]registry.Endpoint{
			route(1, "GET /api/orders"),
			route(2, "POST /api/orders"),
			route(3, "GET /api/orders/{id}"),
			route(4, "GET /api/orders/{id}/items/{item}"),
			route(5, "GET /health"),
		},
	))

	cases := []struct {
		path       string
		kind       ContractKind
		confidence model.Confidence
		routes     []string
	}{
		{"/api/orders", ContractMethodMismatch, model.ConfidenceHigh,
			[]string{"GET /api/orders", "POST /api/orders"}},
		{"/api/orders/42", ContractMethodMismatch, model.ConfidenceMedium,
			[]string{"GET /api/orders/{id}"}},
		{"/api/order", ContractNoRoute, model.ConfidenceHigh, nil},
		{"/api/orders/42/items", ContractPathVariables, model.ConfidenceMedium,
			[]string{"GET /api/orders/{id}/items/{item}"}},
		{"/gateway/health", ContractBasePath, model.ConfidenceLow, []string{"GET /health"}},
		{"https://collector.example.com/v1/traces", ContractNoRoute, model.ConfidenceHigh, nil},
	}
	if len(rep.Findings) != len(cases) {
		t.Fatalf("want %d findings, got %d: %+v", len(cases), len(rep.Findings), rep.Findings)
	}
	for _, tc := range cases {
		fd := findingFor(t, rep, tc.path)
		if fd.Kind != tc.kind {
			t.Errorf("%s: kind = %s, want %s (%s)", tc.path, fd.Kind, tc.kind, fd.Detail)
		}
		if fd.Confidence != tc.confidence {
			t.Errorf("%s: confidence = %s, want %s", tc.path, fd.Confidence, tc.confidence)
		}
		if fd.Detail == "" {
			t.Errorf("%s: a finding must say what differs", tc.path)
		}

		// The call side is always located.
		if fd.Call.Member != "web" || fd.Call.File != "src/client.ts" || fd.Call.Line == 0 {
			t.Errorf("%s: the call lost its location: %+v", tc.path, fd.Call)
		}
		// And so is every route the finding is about.
		var got []string
		for _, rt := range fd.Routes {
			got = append(got, rt.Endpoint)
			if rt.Member != "api" || rt.File != "internal/http/routes.go" || rt.Line == 0 {
				t.Errorf("%s: route %q lost its location: %+v", tc.path, rt.Endpoint, rt)
			}
		}
		if strings.Join(got, "|") != strings.Join(tc.routes, "|") {
			t.Errorf("%s: routes = %v, want %v", tc.path, got, tc.routes)
		}
	}

	// The DELETE on a path that takes GET and POST is the one call in this
	// fixture that should make somebody open two files, and the message has to
	// say which verbs the path does take.
	if d := findingFor(t, rep, "/api/orders").Detail; !strings.Contains(d, "GET and POST") ||
		!strings.Contains(d, "DELETE") {
		t.Errorf("method mismatch does not name both sides' verbs: %s", d)
	}
	// A host is shown, never used.
	if fd := findingFor(t, rep, "https://collector.example.com/v1/traces"); fd.Call.Host != "collector.example.com" ||
		!strings.Contains(fd.Detail, "external") {
		t.Errorf("a call that names a host must say it may be leaving the system: %+v", fd)
	}
}

// TestContractsUncalledRoutesAreInformationalAndWeakest: a route nothing
// indexed reaches is listed, graded `low` every time, and kept out of the
// findings.
func TestContractsUncalledRoutesAreInformationalAndWeakest(t *testing.T) {
	rep := contracts(t, clientAndServer(
		[]registry.ClientCallUsage{call(10, "GET", "/api/orders")},
		[]registry.Endpoint{route(1, "GET /api/orders"), route(2, "DELETE /api/admin/purge")},
	))

	if got := uncalledSignatures(rep); len(got) != 1 || got[0] != "api: DELETE /api/admin/purge" {
		t.Fatalf("uncalled routes = %v, want only the purge route", got)
	}
	r := rep.UncalledRoutes[0]
	if r.Confidence != model.ConfidenceLow {
		t.Errorf("an uncalled route is graded %s; it is never more than low", r.Confidence)
	}
	if r.File != "internal/http/routes.go" || r.Line != 2 {
		t.Errorf("an uncalled route lost its location: %+v", r)
	}
	if len(rep.Findings) != 0 {
		t.Errorf("an uncalled route is an observation, not a finding: %+v", rep.Findings)
	}
	if rep.Summary.RoutesExamined != 2 || rep.Summary.RoutesUncalled != 1 {
		t.Errorf("route arithmetic: %+v", rep.Summary)
	}
}

// ---------------------------------------------------- what is NOT reported

// TestContractsDoesNotReportCallsThatLineUp is the list of things that look
// like a mismatch to a string comparison and are not one. Every row here is a
// finding this report must not make.
func TestContractsDoesNotReportCallsThatLineUp(t *testing.T) {
	cases := []struct {
		name  string
		call  registry.ClientCallUsage
		route string
	}{
		{"same literal route", call(1, "GET", "/api/orders"), "GET /api/orders"},
		{"brace variable", call(1, "GET", "/api/orders/42"), "GET /api/orders/{id}"},
		{"colon variable", call(1, "GET", "/api/orders/42"), "GET /api/orders/:id"},
		{"typed variable", call(1, "GET", "/api/orders/42"), "GET /api/orders/<int:id>"},
		{"constrained variable", call(1, "GET", "/api/orders/42"), `GET /api/orders/{id:\d+}`},
		{"two variables", call(1, "GET", "/api/t/acme/orders/42"), "GET /api/t/{tenant}/orders/{id}"},
		{"trailing slash on the call", call(1, "GET", "/api/orders/"), "GET /api/orders"},
		{"trailing slash on the route", call(1, "GET", "/api/orders"), "GET /api/orders/"},
		{"case", call(1, "GET", "/API/Orders"), "GET /api/orders"},
		{"query string", call(1, "GET", "/api/orders?page=2&size=10"), "GET /api/orders"},
		{"fragment", call(1, "GET", "/api/orders#top"), "GET /api/orders"},
		{"absolute URL", call(1, "GET", "https://orders.internal:8443/api/orders"), "GET /api/orders"},
		{"verb unread on the call", call(1, "", "/api/orders"), "POST /api/orders"},
		{"verb unstated on the route", call(1, "DELETE", "/api/orders"), "ANY /api/orders"},
		{"route with no verb at all", call(1, "DELETE", "/api/orders"), "/api/orders"},
		{"lower-case verb", call(1, "get", "/api/orders"), "GET /api/orders"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := contracts(t, clientAndServer(
				[]registry.ClientCallUsage{tc.call}, []registry.Endpoint{route(1, tc.route)}))

			if len(rep.Findings) != 0 {
				t.Errorf("reported a call that lines up: %+v", rep.Findings)
			}
			if len(rep.NotCompared) != 0 {
				t.Errorf("declined to compare a call that lines up: %+v", rep.NotCompared)
			}
			if rep.Summary.Matched != 1 {
				t.Errorf("the call was not counted as matched: %+v", rep.Summary)
			}
			if len(rep.UncalledRoutes) != 0 {
				t.Errorf("the route it reaches was listed as uncalled: %+v", rep.UncalledRoutes)
			}
		})
	}
}

// TestContractsAnotherRouteWithTheRightVerbIsNotAMismatch: DELETE /orders/7
// where one route answers GET on that path and another answers DELETE is a
// call that works. The mismatch is only a finding when NO route with the path
// takes the verb, in any member.
func TestContractsAnotherRouteWithTheRightVerbIsNotAMismatch(t *testing.T) {
	members := clientAndServer(
		[]registry.ClientCallUsage{
			call(1, "DELETE", "/api/orders/7"),
			// The literal route takes GET only; the parameterised one takes
			// DELETE, and every router here sends the request to it.
			call(2, "DELETE", "/api/orders/summary"),
		},
		[]registry.Endpoint{
			route(1, "GET /api/orders/{id}"),
			route(2, "GET /api/orders/summary"),
		},
	)
	members = append(members, Member{Name: "admin", IndexPath: "/idx/admin.db", RunID: 1,
		Source: &fakeSource{
			run:       readyRun(1, "c0ffee1234", at(time.Hour)),
			endpoints: []registry.Endpoint{route(9, "DELETE /api/orders/{id}")},
			languages: map[string]int{"go": 4},
		}})

	rep := contracts(t, members)
	if len(rep.Findings) != 0 {
		t.Errorf("a call answered by a route in another member was reported: %+v", rep.Findings)
	}
	if rep.Summary.Matched != 2 {
		t.Errorf("matched = %d, want 2", rep.Summary.Matched)
	}
}

// TestContractsACallAnsweredInItsOwnMemberIsNotReported: the indexer already
// bound it, with the framework's own precedence rules. It is counted, its
// route is not listed as uncalled, and nothing about it is re-decided here.
func TestContractsACallAnsweredInItsOwnMemberIsNotReported(t *testing.T) {
	bound := call(5, "GET", "/internal/metrics")
	bound.Binding, bound.EndpointID, bound.Endpoint = "bound", 7, "GET /internal/metrics"

	members := []Member{
		{Name: "monolith", IndexPath: "/idx/m.db", RunID: 1, Source: &fakeSource{
			run:       readyRun(1, "aaaaaaaaaa", at(time.Hour)),
			calls:     []registry.ClientCallUsage{bound},
			endpoints: []registry.Endpoint{route(7, "GET /internal/metrics")},
			languages: map[string]int{"typescript": 3, "go": 9},
		}},
		{Name: "other", IndexPath: "/idx/o.db", RunID: 1, Source: &fakeSource{
			run: readyRun(1, "bbbbbbbbbb", at(time.Hour)), languages: map[string]int{"go": 1},
		}},
	}
	rep := contracts(t, members)

	if len(rep.Findings) != 0 || len(rep.UncalledRoutes) != 0 {
		t.Errorf("a locally answered call produced findings %+v / uncalled %+v",
			rep.Findings, rep.UncalledRoutes)
	}
	if rep.Summary.Matched != 1 || rep.Summary.AnsweredInOwnMember != 1 {
		t.Errorf("summary = %+v, want one match answered in its own member", rep.Summary)
	}
}

// TestContractsAWrongVerbInsideOneMemberIsStillReported: a frontend and its
// backend indexed as ONE member are still a client and a server. The indexer
// leaves a wrong-verb call unbound, and that is exactly the call to show.
func TestContractsAWrongVerbInsideOneMemberIsStillReported(t *testing.T) {
	members := []Member{
		{Name: "monolith", IndexPath: "/idx/m.db", RunID: 1, Source: &fakeSource{
			run:       readyRun(1, "aaaaaaaaaa", at(time.Hour)),
			calls:     []registry.ClientCallUsage{call(5, "PATCH", "/api/profile")},
			endpoints: []registry.Endpoint{route(7, "PUT /api/profile")},
			languages: map[string]int{"typescript": 3, "go": 9},
		}},
		{Name: "other", IndexPath: "/idx/o.db", RunID: 1, Source: &fakeSource{
			run: readyRun(1, "bbbbbbbbbb", at(time.Hour)), languages: map[string]int{"go": 1},
		}},
	}
	rep := contracts(t, members)

	fd := findingFor(t, rep, "/api/profile")
	if fd.Kind != ContractMethodMismatch || len(fd.Routes) != 1 || fd.Routes[0].Member != "monolith" {
		t.Errorf("want a method mismatch against the member's own route, got %+v", fd)
	}
}

// TestContractsSetAsideCallsMadeFromTestFiles: a test requesting /api/fake
// from a server it started itself is not a client of this system. It is
// counted, said, and neither reported nor allowed to make a route look called.
func TestContractsSetAsideCallsMadeFromTestFiles(t *testing.T) {
	inTest := func(line int, method, path string) registry.ClientCallUsage {
		c := call(line, method, path)
		c.FilePath = "src/client.test.ts"
		return c
	}
	members := clientAndServer(
		[]registry.ClientCallUsage{
			call(10, "GET", "/api/orders"),
			inTest(20, "GET", "/api/fake"),         // would be a no_matching_route
			inTest(30, "DELETE", "/api/orders"),    // would be a method_mismatch
			inTest(40, "DELETE", "/api/admin/all"), // would make the route look called
		},
		[]registry.Endpoint{route(1, "GET /api/orders"), route(2, "DELETE /api/admin/all")},
	)
	isTest := func(p string) bool { return strings.Contains(p, ".test.") }

	rep := contracts(t, members, WithTestPaths(isTest))
	if len(rep.Findings) != 0 {
		t.Errorf("calls in test files were reported: %+v", rep.Findings)
	}
	if rep.Summary.CallsExamined != 1 || rep.Summary.CallsInTestFiles != 3 {
		t.Errorf("summary = %+v, want 1 examined and 3 set aside", rep.Summary)
	}
	if got := uncalledSignatures(rep); len(got) != 1 || got[0] != "api: DELETE /api/admin/all" {
		t.Errorf("a route only a test calls must stay uncalled, got %v", got)
	}

	var out bytes.Buffer
	if err := RenderContracts(&out, rep); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(flat(out.String()), "3 further calls in test files were set aside") {
		t.Errorf("the calls set aside are not mentioned:\n%s", out.String())
	}

	// With no predicate supplied nothing is set aside, and the same calls are
	// examined like any other.
	all := contracts(t, members)
	if all.Summary.CallsInTestFiles != 0 || all.Summary.CallsExamined != 4 || len(all.Findings) != 2 {
		t.Errorf("without a test-path predicate: %+v / %+v", all.Summary, all.Findings)
	}
}

// ------------------------------------------------------------- base paths

// TestContractsBaseURLFromConfigurationIsNotCompared: `${API_BASE}/orders`
// keeps NONE of its path in the index. The honest outcomes are a finding that
// says so or no finding at all; a "no matching route" would be a guess about a
// path nobody read.
func TestContractsBaseURLFromConfigurationIsNotCompared(t *testing.T) {
	rep := contracts(t, clientAndServer(
		[]registry.ClientCallUsage{
			dynamicCall(10, "GET", ""),                        // `${API_BASE}/orders`
			dynamicCall(20, "GET", "https://api.example.com"), // "https://api.example.com" + path
			dynamicCall(30, "GET", "/api/ord"),                // "/api/ord" + suffix
			call(40, "GET", "/"),                              // a target that read as the root
		},
		[]registry.Endpoint{route(1, "GET /api/orders")},
	))

	// "/api/ord" + suffix has ONE whole literal segment (/api), which starts
	// the route, so that one IS compared and matches by prefix. The other
	// three have no path to compare.
	if rep.Summary.NotCompared != 3 || len(rep.NotCompared) != 3 {
		t.Fatalf("not compared = %d (%+v), want 3", rep.Summary.NotCompared, rep.NotCompared)
	}
	if rep.Summary.MatchedByPrefix != 1 {
		t.Errorf("matched by prefix = %d, want 1", rep.Summary.MatchedByPrefix)
	}
	if len(rep.Findings) != 0 {
		t.Errorf("a path that was never indexed was reported as a finding: %+v", rep.Findings)
	}

	byLine := map[int]UncomparedCall{}
	for _, u := range rep.NotCompared {
		byLine[u.Call.Line] = u
	}
	if r := byLine[10].Reason; !strings.Contains(r, "base URL") || !strings.Contains(r, "configuration") {
		t.Errorf("an unread base URL must be named as one: %s", r)
	}
	if u := byLine[20]; u.Call.Host != "api.example.com" || !strings.Contains(u.Reason, "run time") {
		t.Errorf("a host-only literal prefix: %+v", u)
	}
	if byLine[10].Call.File == "" || byLine[10].Call.Line != 10 {
		t.Errorf("an uncompared call lost its location: %+v", byLine[10].Call)
	}
}

// TestContractsUncomparedCallsQualifyTheUncalledRoutes: a call whose path was
// never read may be the caller of any route, so the list of routes "no indexed
// call reaches" has to say that such calls exist; and a call that read as the
// root path must not leave the root route listed as uncalled.
func TestContractsUncomparedCallsQualifyTheUncalledRoutes(t *testing.T) {
	rep := contracts(t, clientAndServer(
		[]registry.ClientCallUsage{dynamicCall(10, "GET", ""), call(20, "GET", "/")},
		[]registry.Endpoint{route(1, "GET /"), route(2, "GET /api/orders")},
	))

	if got := uncalledSignatures(rep); len(got) != 1 || got[0] != "api: GET /api/orders" {
		t.Errorf("uncalled = %v, want only /api/orders: the root route may be what the root call reaches", got)
	}
	var out bytes.Buffer
	if err := RenderContracts(&out, rep); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(flat(out.String()), "2 calls could not be compared at all (see NOT COMPARED) and may reach any route listed here") {
		t.Errorf("the uncalled section does not mention the uncompared calls:\n%s", out.String())
	}
}

// TestContractsBasePathIsReportedWeakAndNeverAssumed covers both directions a
// prefix can be missing in, and the alignment that must be refused.
func TestContractsBasePathIsReportedWeakAndNeverAssumed(t *testing.T) {
	rep := contracts(t, clientAndServer(
		[]registry.ClientCallUsage{
			call(10, "GET", "/orders-service/api/health"), // service mounted under a prefix
			call(20, "GET", "/invoices/9"),                // client base URL supplies /api/v2
			call(30, "GET", "/nothing/7"),                 // shares only a path variable with a route
			call(40, "POST", "/gw/api/health"),            // prefix AND verb would both have to give
		},
		[]registry.Endpoint{
			route(1, "GET /api/health"),
			route(2, "GET /api/v2/invoices/{id}"),
			route(3, "GET /{slug}"),
		},
	))

	mounted := findingFor(t, rep, "/orders-service/api/health")
	if mounted.Kind != ContractBasePath || mounted.Confidence != model.ConfidenceLow {
		t.Errorf("mounted service: %+v", mounted)
	}
	if !strings.Contains(mounted.Detail, "/orders-service") || !strings.Contains(mounted.Detail, "gateway") {
		t.Errorf("the finding must name the prefix that would have to be true: %s", mounted.Detail)
	}

	based := findingFor(t, rep, "/invoices/9")
	if based.Kind != ContractBasePath || based.Confidence != model.ConfidenceLow {
		t.Errorf("client base URL: %+v", based)
	}
	if !strings.Contains(based.Detail, "/api/v2") || !strings.Contains(based.Detail, "base URL") {
		t.Errorf("the finding must name the base URL it could not read: %s", based.Detail)
	}

	// /{slug} is the tail of every path there is. Aligning on it would make
	// every unknown call a base-path lead to the same route.
	if fd := findingFor(t, rep, "/nothing/7"); fd.Kind != ContractNoRoute {
		t.Errorf("a path variable alone must not align two paths: %+v", fd)
	}

	both := findingFor(t, rep, "/gw/api/health")
	if both.Kind != ContractBasePath || !strings.Contains(both.Detail, "verb differs") {
		t.Errorf("a prefix-and-verb disagreement must say both: %+v", both)
	}

	// A route named under base_path_unverified is not then also "uncalled":
	// the report would be contradicting itself.
	for _, sig := range uncalledSignatures(rep) {
		if sig == "api: GET /api/health" || sig == "api: GET /api/v2/invoices/{id}" {
			t.Errorf("%s is listed as uncalled although a call may reach it", sig)
		}
	}
	if rep.Summary.BasePathUnverified != 3 {
		t.Errorf("base path count = %d, want 3", rep.Summary.BasePathUnverified)
	}
}

// ---------------------------------------------------------- path variables

func TestContractsPathVariableCount(t *testing.T) {
	rep := contracts(t, clientAndServer(
		[]registry.ClientCallUsage{
			call(10, "GET", "/api/users"),              // route needs an id
			call(20, "GET", "/api/teams/acme/42"),      // one segment more than the route
			call(30, "GET", "/api/projects/7/tasks/9"), // a sub-route nobody declared
			call(40, "POST", "/api/users"),             // wrong verb for the nearest route
			dynamicCall(50, "GET", "/api/widgets/"),    // appends a segment the route lacks
		},
		[]registry.Endpoint{
			route(1, "GET /api/users/{id}"),
			route(2, "GET /api/teams/{id}"),
			route(3, "GET /api/projects/{id}/tasks"),
			route(4, "GET /api/widgets"),
		},
	))

	short := findingAt(t, rep, 10)
	if short.Kind != ContractPathVariables || short.Confidence != model.ConfidenceMedium {
		t.Errorf("a call one variable short: %+v", short)
	}
	if !strings.Contains(short.Detail, "1 path variable") {
		t.Errorf("the finding must count the variables: %s", short.Detail)
	}

	long := findingFor(t, rep, "/api/teams/acme/42")
	if long.Kind != ContractPathVariables || long.Confidence != model.ConfidenceLow {
		t.Errorf("a call one segment long is weaker — it may be an undeclared sub-route: %+v", long)
	}

	if fd := findingFor(t, rep, "/api/projects/7/tasks/9"); fd.Kind != ContractNoRoute {
		t.Errorf("a route ending in a literal does not make the call's extra segment a "+
			"variable-count problem: %+v", fd)
	}

	dyn := findingFor(t, rep, "/api/widgets/")
	if dyn.Kind != ContractPathVariables || dyn.Confidence != model.ConfidenceLow {
		t.Errorf("a run-time segment appended to a route that ends there: %+v", dyn)
	}

	// The POST to /api/users has no route with its verb anywhere near it; the
	// GET route's variable count says nothing about a POST.
	if post := findingAt(t, rep, 40); post.Kind != ContractNoRoute {
		t.Errorf("a variable-count finding was made across different verbs: %+v", post)
	}
}

// ------------------------------------------------------- run-time targets

func TestContractsRunTimeTargetsAreOnlyEverLow(t *testing.T) {
	rep := contracts(t, clientAndServer(
		[]registry.ClientCallUsage{
			dynamicCall(10, "GET", "/api/orders/"),    // `/api/orders/${id}`
			dynamicCall(20, "GET", "/api/reports/"),   // nothing starts there
			dynamicCall(30, "DELETE", "/api/orders/"), // starts there, never with DELETE
		},
		[]registry.Endpoint{route(1, "GET /api/orders/{id}")},
	))

	if rep.Summary.MatchedByPrefix != 1 {
		t.Errorf("matched by prefix = %d, want 1", rep.Summary.MatchedByPrefix)
	}
	if len(rep.UncalledRoutes) != 0 {
		t.Errorf("a route a literal prefix leads to was listed as uncalled: %+v", rep.UncalledRoutes)
	}
	if len(rep.Findings) != 2 {
		t.Fatalf("want 2 findings, got %+v", rep.Findings)
	}
	for _, fd := range rep.Findings {
		if fd.Kind != ContractNoRoute || fd.Confidence != model.ConfidenceLow {
			t.Errorf("a finding resting on a literal prefix must be a low no_matching_route: %+v", fd)
		}
		if !strings.Contains(fd.Detail, "literal prefix") {
			t.Errorf("the finding must say only the prefix was read: %s", fd.Detail)
		}
	}
	var del ContractFinding
	for _, fd := range rep.Findings {
		if fd.Call.Method == "DELETE" {
			del = fd
		}
	}
	if !strings.Contains(del.Detail, "another verb") {
		t.Errorf("routes that start there with another verb must be mentioned: %s", del.Detail)
	}
}

// ------------------------------------------------------------- the ceiling

// TestContractsNeverClaimAboveHigh is the constraint the whole report is built
// under: only a compiler index makes `exact`, and none spans two repositories.
func TestContractsNeverClaimAboveHigh(t *testing.T) {
	rep := contracts(t, clientAndServer(
		[]registry.ClientCallUsage{
			call(10, "DELETE", "/api/orders"), call(20, "GET", "/api/order"),
			call(30, "GET", "/api/users"), call(40, "GET", "/gw/api/orders"),
			dynamicCall(50, "GET", "/api/none/"),
		},
		[]registry.Endpoint{
			route(1, "GET /api/orders"), route(2, "GET /api/users/{id}"), route(3, "PUT /unused"),
		},
	))
	if len(rep.Findings) == 0 || len(rep.UncalledRoutes) == 0 {
		t.Fatal("the fixture produced nothing to check")
	}
	for _, fd := range rep.Findings {
		if !fd.Confidence.Valid() || fd.Confidence.Rank() > model.ConfidenceHigh.Rank() {
			t.Errorf("finding graded %q: %+v", fd.Confidence, fd)
		}
	}
	for _, r := range rep.UncalledRoutes {
		if r.Confidence != model.ConfidenceLow {
			t.Errorf("uncalled route graded %q", r.Confidence)
		}
	}

	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"confidence":"exact"`) {
		t.Error("the JSON document contains an `exact` confidence")
	}
}

// --------------------------------------------- what was not read, said aloud

// TestContractsWeakenEveryFindingWhenAMemberIsMissing: with a member unread, a
// route it declares may answer any call reported here. Every finding loses a
// tier, says so, and the report names the member that cost it.
func TestContractsWeakenEveryFindingWhenAMemberIsMissing(t *testing.T) {
	members := clientAndServer(
		[]registry.ClientCallUsage{
			call(10, "DELETE", "/api/orders"), call(20, "GET", "/api/order"),
			call(30, "GET", "/gw/api/orders"), // base_path_unverified: low already
		},
		[]registry.Endpoint{route(1, "GET /api/orders")},
	)
	whole := contracts(t, members)
	for _, fd := range whole.Findings {
		if fd.Kind != ContractBasePath && (fd.Confidence != model.ConfidenceHigh || fd.Weakened) {
			t.Fatalf("with every member read, the two strong findings are high and unweakened: %+v", fd)
		}
	}
	if len(whole.Weakeners) != 0 || !whole.Complete() {
		t.Fatalf("a fully read system reported weakeners %v", whole.Weakeners)
	}

	members = append(members, Member{
		Name: "billing", IndexPath: "/idx/billing.db",
		Unavailable: "no index at /idx/billing.db. Build one in that checkout.",
	})
	rep := contracts(t, members)

	if rep.Complete() {
		t.Error("a report missing a member must not call itself complete")
	}
	if len(rep.UnavailableMembers) != 1 || rep.UnavailableMembers[0] != "billing" {
		t.Errorf("unavailable members = %v", rep.UnavailableMembers)
	}
	if len(rep.Weakeners) != 1 || !strings.Contains(rep.Weakeners[0], "billing") {
		t.Errorf("weakeners = %v, want one naming billing", rep.Weakeners)
	}
	if len(rep.Findings) != 3 {
		t.Fatalf("the readable members' findings were dropped: %+v", rep.Findings)
	}
	for _, fd := range rep.Findings {
		if fd.Kind == ContractBasePath {
			// Already at the floor: it stays low, and is not said to have
			// lost a tier it did not have.
			if fd.Confidence != model.ConfidenceLow || fd.Weakened {
				t.Errorf("a low finding was marked as having lost a tier: %+v", fd)
			}
			continue
		}
		if fd.Confidence != model.ConfidenceMedium || !fd.Weakened {
			t.Errorf("a finding kept its tier with a member unread: %+v", fd)
		}
	}

	var out bytes.Buffer
	if err := RenderContracts(&out, rep); err != nil {
		t.Fatal(err)
	}
	text := flat(out.String())
	for _, want := range []string{
		"BILLING WAS NOT READ", "no index at /idx/billing.db",
		"EVERY FINDING BELOW IS ONE TIER WEAKER", "INCOMPLETE", "floor",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered report omits %q:\n%s", want, out.String())
		}
	}
}

// TestContractsAStaleOrPartialIndexIsSaidAndCostsATier.
func TestContractsAStaleOrPartialIndexIsSaidAndCostsATier(t *testing.T) {
	t.Run("stale", func(t *testing.T) {
		members := clientAndServer(
			[]registry.ClientCallUsage{call(10, "GET", "/api/order")},
			[]registry.Endpoint{route(1, "GET /api/orders")},
		)
		members[1].Source.(*fakeSource).run = readyRun(1, "a19d7e04bc", at(40*24*time.Hour))
		rep := contracts(t, members)

		var api ContractMember
		for _, m := range rep.Members {
			if m.Name == "api" {
				api = m
			}
		}
		if !api.Stale {
			t.Error("a 40-day-old index is not marked stale")
		}
		if len(rep.Weakeners) != 1 || !strings.Contains(rep.Weakeners[0], "40 day") {
			t.Errorf("weakeners = %v, want the age stated", rep.Weakeners)
		}
		if fd := rep.Findings[0]; fd.Confidence != model.ConfidenceMedium || !fd.Weakened {
			t.Errorf("a finding against a stale route list kept its tier: %+v", fd)
		}

		// And the threshold is the operator's to move.
		fresh := contracts(t, members, WithStaleAfter(60*24*time.Hour))
		if len(fresh.Weakeners) != 0 || fresh.Findings[0].Confidence != model.ConfidenceHigh {
			t.Errorf("--stale-after was ignored: %v / %+v", fresh.Weakeners, fresh.Findings[0])
		}
	})

	t.Run("partial", func(t *testing.T) {
		members := clientAndServer(
			[]registry.ClientCallUsage{call(10, "GET", "/api/order")},
			[]registry.Endpoint{route(1, "GET /api/orders")},
		)
		run := readyRun(1, "a19d7e04bc", at(time.Hour))
		run.IndexState, run.FilesUnaccounted = model.IndexStatePartial, 4
		members[1].Source.(*fakeSource).run = run
		rep := contracts(t, members)

		if len(rep.Weakeners) != 1 || !strings.Contains(rep.Weakeners[0], "partial") {
			t.Errorf("weakeners = %v, want the partial index named", rep.Weakeners)
		}
		if rep.Findings[0].Confidence != model.ConfidenceMedium {
			t.Errorf("a finding against a partial route list kept its tier: %+v", rep.Findings[0])
		}
	})
}

// TestContractsSayWhichLanguagesTheyDidNotRead: a member with no outbound
// calls is either a member that makes none or a member written in a language
// calls are not extracted from, and only the tool can tell the reader which.
func TestContractsSayWhichLanguagesTheyDidNotRead(t *testing.T) {
	members := []Member{
		{Name: "billing", IndexPath: "/idx/billing.db", RunID: 1, Source: &fakeSource{
			run:       readyRun(1, "1111111111", at(time.Hour)),
			endpoints: []registry.Endpoint{route(1, "POST /api/invoices")},
			// Spring: routes ARE read from Java, outbound calls are not.
			languages: map[string]int{"java": 120, "yaml": 8, "xml": 3},
		}},
		{Name: "mobile-bff", IndexPath: "/idx/bff.db", RunID: 1, Source: &fakeSource{
			run: readyRun(1, "2222222222", at(time.Hour)),
			// Neither extraction reads Kotlin.
			languages: map[string]int{"kotlin": 40, "typescript": 5},
		}},
		{Name: "web", IndexPath: "/idx/web.db", RunID: 1, Source: &fakeSource{
			run:       readyRun(1, "3333333333", at(time.Hour)),
			calls:     []registry.ClientCallUsage{call(10, "GET", "/api/missing")},
			languages: map[string]int{"tsx": 60, "typescript": 20, "css": 9},
		}},
	}
	rep := contracts(t, members)

	by := map[string]ContractMember{}
	for _, m := range rep.Members {
		by[m.Name] = m
	}

	billing := by["billing"]
	if len(billing.CallsReadIn) != 0 || len(billing.CallsNotReadIn) != 1 ||
		billing.CallsNotReadIn[0] != (LanguageFiles{Language: "java", Files: 120}) {
		t.Errorf("billing calls: read %+v, not read %+v", billing.CallsReadIn, billing.CallsNotReadIn)
	}
	if len(billing.RoutesNotReadIn) != 0 {
		t.Errorf("routes ARE extracted from Java: %+v", billing.RoutesNotReadIn)
	}
	if w := strings.Join(billing.Warnings, " | "); !strings.Contains(w, "outbound calls are not extracted from java") ||
		!strings.Contains(w, "No language in this member has client-call extraction") {
		t.Errorf("billing's warnings do not say its calls are invisible: %s", w)
	}

	bff := by["mobile-bff"]
	if len(bff.CallsNotReadIn) != 1 || bff.CallsNotReadIn[0].Language != "kotlin" ||
		len(bff.RoutesNotReadIn) != 1 || bff.RoutesNotReadIn[0].Language != "kotlin" {
		t.Errorf("mobile-bff: %+v", bff)
	}
	if len(bff.CallsReadIn) != 1 || bff.CallsReadIn[0].Language != "typescript" {
		t.Errorf("mobile-bff's TypeScript IS read: %+v", bff.CallsReadIn)
	}

	web := by["web"]
	if len(web.CallsNotReadIn) != 0 || len(web.CallsReadIn) != 2 || web.CallsReadIn[0].Language != "tsx" {
		t.Errorf("web: read %+v, not read %+v", web.CallsReadIn, web.CallsNotReadIn)
	}

	// Kotlin routes being unread is a reason the "no matching route" below may
	// be wrong, so it costs the finding a tier and is named.
	if len(rep.Weakeners) != 1 || !strings.Contains(rep.Weakeners[0], "mobile-bff") ||
		!strings.Contains(rep.Weakeners[0], "kotlin") {
		t.Errorf("weakeners = %v", rep.Weakeners)
	}
	if fd := findingFor(t, rep, "/api/missing"); fd.Confidence != model.ConfidenceMedium || !fd.Weakened {
		t.Errorf("finding = %+v, want medium and weakened", fd)
	}

	var out bytes.Buffer
	if err := RenderContracts(&out, rep); err != nil {
		t.Fatal(err)
	}
	text := flat(out.String())
	for _, want := range []string{
		"CALLS NOT READ IN",
		"outbound calls are not extracted from java (120 files)",
		"routes are not extracted from kotlin (40 files)",
		"Calls made from billing and mobile-bff are partly or wholly unread",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered report omits %q:\n%s", want, out.String())
		}
	}
}

// ----------------------------------------------------------------- bounds

// TestContractsAreBoundedAndCountWhatTheyCut: a capped section keeps its
// strongest rows, says how many it dropped, and leaves the totals alone.
func TestContractsAreBoundedAndCountWhatTheyCut(t *testing.T) {
	var calls []registry.ClientCallUsage
	for i := 1; i <= 7; i++ {
		calls = append(calls, call(i, "GET", fmt.Sprintf("/api/missing/%d/x", i)))
	}
	// One low finding of the same kind, which must be the first to go.
	calls = append(calls, dynamicCall(99, "GET", "/api/elsewhere/"))

	var endpoints []registry.Endpoint
	for i := int64(1); i <= 5; i++ {
		endpoints = append(endpoints, route(i, fmt.Sprintf("GET /unused/%d", i)))
	}

	rep := contracts(t, clientAndServer(calls, endpoints), WithMaxContractRows(3))

	if len(rep.Findings) != 3 || rep.Truncated[string(ContractNoRoute)] != 5 {
		t.Errorf("findings kept %d, cut %d; want 3 and 5", len(rep.Findings), rep.Truncated[string(ContractNoRoute)])
	}
	for _, fd := range rep.Findings {
		if fd.Confidence != model.ConfidenceHigh {
			t.Errorf("the cap kept a %s finding and dropped a stronger one: %+v", fd.Confidence, fd)
		}
	}
	if len(rep.UncalledRoutes) != 3 || rep.Truncated["uncalled_routes"] != 2 {
		t.Errorf("uncalled kept %d, cut %d; want 3 and 2", len(rep.UncalledRoutes), rep.Truncated["uncalled_routes"])
	}
	if rep.Summary.NoMatchingRoute != 8 || rep.Summary.RoutesUncalled != 5 {
		t.Errorf("a cap changed the totals: %+v", rep.Summary)
	}

	var out bytes.Buffer
	if err := RenderContracts(&out, rep); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NO MATCHING ROUTE (8)", "5 more not shown", "2 more not shown"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("rendered report omits %q:\n%s", want, out.String())
		}
	}

	if _, err := New(WithMaxContractRows(0)); err == nil {
		t.Error("a non-positive row cap must be refused, not read as unlimited")
	}
}

// TestContractsNameAtMostAFewRoutesPerFinding: a call whose path a dozen
// routes share by another verb is one finding, not a dozen lines of it.
func TestContractsNameAtMostAFewRoutesPerFinding(t *testing.T) {
	var endpoints []registry.Endpoint
	for i, verb := range []string{"GET", "POST", "PUT", "PATCH"} {
		endpoints = append(endpoints, route(int64(i+1), verb+" /api/thing"))
	}
	rep := contracts(t, clientAndServer(
		[]registry.ClientCallUsage{call(1, "DELETE", "/api/thing")}, endpoints))

	fd := findingFor(t, rep, "/api/thing")
	if len(fd.Routes) != maxRoutesPerFinding || fd.MoreRoutes != 1 {
		t.Errorf("routes listed %d, more %d; want %d and 1", len(fd.Routes), fd.MoreRoutes, maxRoutesPerFinding)
	}
	if !strings.Contains(fd.Detail, "GET, PATCH, POST and PUT") {
		t.Errorf("the detail must still name every verb the path takes: %s", fd.Detail)
	}
}

// TestContractArithmeticCloses: every call examined lands in exactly one
// outcome, so the summary can be added up by the reader.
func TestContractArithmeticCloses(t *testing.T) {
	bound := call(1, "GET", "/self")
	bound.Binding, bound.EndpointID = "bound", 50

	members := clientAndServer(
		[]registry.ClientCallUsage{
			bound,
			call(2, "GET", "/api/orders"), call(3, "DELETE", "/api/orders"),
			call(4, "GET", "/api/order"), call(5, "GET", "/api/users"),
			call(6, "GET", "/gw/api/orders"), call(7, "GET", "/"),
			dynamicCall(8, "GET", ""), dynamicCall(9, "GET", "/api/users/"),
			dynamicCall(10, "GET", "/api/none/"),
		},
		[]registry.Endpoint{route(1, "GET /api/orders"), route(2, "GET /api/users/{id}")},
	)
	rep := contracts(t, members)
	s := rep.Summary

	sum := s.Matched + s.MatchedByPrefix + s.MethodMismatch + s.PathVariableMismatch +
		s.NoMatchingRoute + s.BasePathUnverified + s.NotCompared
	if s.CallsExamined != 10 || sum != s.CallsExamined {
		t.Errorf("calls examined %d, outcomes sum to %d: %+v", s.CallsExamined, sum, s)
	}
	if got := s.MethodMismatch + s.PathVariableMismatch + s.NoMatchingRoute + s.BasePathUnverified; got != len(rep.Findings) {
		t.Errorf("the summary counts %d findings and the list holds %d", got, len(rep.Findings))
	}
	if s.NotCompared != len(rep.NotCompared) || s.RoutesUncalled != len(rep.UncalledRoutes) {
		t.Errorf("uncapped lists disagree with their totals: %+v", s)
	}
}

// ------------------------------------------------------ shape of the report

// TestContractsAreDeterministic: the order the members were typed in is not
// information, and two runs over the same indexes must print the same bytes.
func TestContractsAreDeterministic(t *testing.T) {
	members := clientAndServer(
		[]registry.ClientCallUsage{
			call(30, "GET", "/zeta"), call(10, "DELETE", "/api/orders"),
			call(20, "GET", "/alpha"), call(40, "GET", "https://x.example.com/alpha"),
		},
		[]registry.Endpoint{route(2, "PUT /b"), route(1, "GET /api/orders"), route(3, "PUT /a")},
	)
	reversed := []Member{members[1], members[0]}

	var a, b bytes.Buffer
	if err := RenderContracts(&a, contracts(t, members)); err != nil {
		t.Fatal(err)
	}
	if err := RenderContracts(&b, contracts(t, reversed)); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Errorf("member order changed the report:\n--- a ---\n%s\n--- b ---\n%s", a.String(), b.String())
	}

	// Within a kind: strongest first, and a call that names a host after the
	// ones that do not, since it is the likeliest to be leaving on purpose.
	rep := contracts(t, members)
	var noRoute []string
	for _, fd := range rep.Findings {
		if fd.Kind == ContractNoRoute {
			noRoute = append(noRoute, fd.Call.Path)
		}
	}
	want := []string{"/alpha", "/zeta", "https://x.example.com/alpha"}
	if strings.Join(noRoute, "|") != strings.Join(want, "|") {
		t.Errorf("no_matching_route order = %v, want %v", noRoute, want)
	}
	if rep.Findings[0].Kind != ContractMethodMismatch {
		t.Errorf("findings with a route on the other side come first; got %s", rep.Findings[0].Kind)
	}
}

// TestRenderContractsPrintsBothSidesAndEveryEmptySection.
func TestRenderContractsPrintsBothSidesAndEveryEmptySection(t *testing.T) {
	rep := contracts(t, clientAndServer(
		[]registry.ClientCallUsage{call(14, "DELETE", "/api/orders/42")},
		[]registry.Endpoint{route(31, "GET /api/orders/{id}")},
	), WithExtractionCoverage([]string{"fetch", "axios"}, []string{"spring", "express"}))

	var out bytes.Buffer
	if err := RenderContracts(&out, rep); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"CONTRACTS: 2 member repositories, 2 readable",
		"CALLS EXAMINED: 1",
		"METHOD MISMATCH (1)",
		"[medium] web  DELETE /api/orders/42",
		"call   src/client.ts:14  in load  via fetch",
		"route  api  GET /api/orders/{id}  internal/http/routes.go:31  handler Handle31",
		// Sections with nothing in them are still printed, with their zero.
		"PATH-VARIABLE MISMATCH (0)",
		"NO MATCHING ROUTE (0)",
		"BASE PATH UNVERIFIED (0)",
		"NOT COMPARED (0)",
		"ROUTES NO INDEXED CALL REACHES (1) — informational",
		"WHAT THIS IS NOT",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered report omits %q:\n%s", want, text)
		}
	}
	for _, want := range []string{
		"No finding is ever `exact`",
		"Outbound calls: fetch, axios. Routes: spring, express.",
		"Hosts are not resolved",
		"query, header or body parameters",
	} {
		if !strings.Contains(flat(text), want) {
			t.Errorf("caveats omit %q:\n%s", want, text)
		}
	}

	// Members precede findings: what was not read has to be seen first.
	if strings.Index(text, "CONTRACTS:") > strings.Index(text, "METHOD MISMATCH") {
		t.Error("the member section must come before the findings")
	}

	// Without the coverage lists the report says it was not told, instead of
	// printing a list of its own.
	bare := contracts(t, clientAndServer(nil, nil))
	if !strings.Contains(strings.Join(bare.Caveats, " "), "was not told") {
		t.Errorf("caveats without coverage: %v", bare.Caveats)
	}
}

// TestContractsJSONHasNoNullLists: a consumer must not have to tell null from
// empty to read "nothing found".
func TestContractsJSONHasNoNullLists(t *testing.T) {
	rep := contracts(t, clientAndServer(nil, nil))
	raw, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), ":null") {
		t.Errorf("the empty report carries a null: %s", raw)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"members", "summary", "findings", "uncalled_routes", "not_compared",
		"weakeners", "unavailable_members", "provenance", "caveats",
	} {
		if _, ok := doc[key]; !ok {
			t.Errorf("the document omits %q", key)
		}
	}
}

// ---------------------------------------------------------------- refusals

func TestContractsRefuseWhatBuildRefuses(t *testing.T) {
	f, err := New()
	if err != nil {
		t.Fatal(err)
	}
	one := clientAndServer(nil, nil)[:1]
	if _, err := f.Contracts(context.Background(), one); err == nil ||
		!strings.Contains(err.Error(), "at least two members") {
		t.Errorf("one member: err = %v", err)
	}

	dup := clientAndServer(nil, nil)
	dup[1].Name = dup[0].Name
	if _, err := f.Contracts(context.Background(), dup); err == nil ||
		!strings.Contains(err.Error(), "must be distinct") {
		t.Errorf("duplicate names: err = %v", err)
	}
}

// TestContractsSurfaceABrokenIndex: an index that is present and answers a
// query with an error is broken, not absent, and must not be read as "this
// member has no languages".
func TestContractsSurfaceABrokenIndex(t *testing.T) {
	for _, method := range []string{"LanguageCounts", "ClientCallReport", "EndpointReport"} {
		members := clientAndServer(nil, nil)
		members[0].Source.(*fakeSource).failOn = method

		f, err := New()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Contracts(context.Background(), members); err == nil ||
			!strings.Contains(err.Error(), `"web"`) {
			t.Errorf("%s failing: err = %v, want an error naming the member", method, err)
		}
	}
}
