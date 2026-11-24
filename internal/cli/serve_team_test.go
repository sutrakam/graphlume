package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sutrakam/graphlume/internal/config"
	devmcp "github.com/sutrakam/graphlume/internal/mcp"
	"github.com/sutrakam/graphlume/internal/restapi"
)

// These tests are written from the attacker's side of a shared server: a
// person holding a narrow token, an expired one, or none, trying each way in.
// They run the real handler stack over a real index, as serve_rest_test.go
// does, so what is asserted is what a deployment does.

// teamFixture is a shared server with one token of each kind.
type teamFixture struct {
	url    string
	tokens map[string]string // by name: wide, narrow, twin, single, expired
	audit  string            // audit log path
	clock  *atomic.Int64     // unix seconds the token set believes it is
}

func (f *teamFixture) bearer(name string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + f.tokens[name]}
}

func teamTestServer(t *testing.T, allowedHosts ...string) *teamFixture {
	t.Helper()
	root := queryFixture(t)

	g := &Global{Project: root, Quiet: true}
	cmd := NewRoot()
	cmd.SetArgs([]string{"--project", root, "--quiet", "version"})
	cmd.SetOut(io.Discard)
	if err := g.setup(cmd); err != nil {
		t.Fatalf("configure: %v", err)
	}

	f := &teamFixture{
		tokens: map[string]string{},
		audit:  filepath.Join(t.TempDir(), "audit.jsonl"),
		clock:  &atomic.Int64{},
	}
	f.clock.Store(time.Now().Unix())

	var lines []string
	add := func(name, profile, expires string, tools ...string) {
		f.tokens[name] = mustToken(t)
		lines = append(lines, entryLine(t, name, f.tokens[name], profile, expires, tools...))
	}
	add("wide", "full", "")
	add("narrow", "minimal", "")
	add("twin", "minimal", "") // the same scope as narrow, a different token
	add("single", "standard", "", "callers")
	add("expired", "full", "2001-01-01")
	add("brief", "full", time.Unix(f.clock.Load(), 0).Add(time.Hour).UTC().Format(time.RFC3339))

	tokens, _, err := loadTokensFile(writeTokensFile(t, 0o600, lines...))
	if err != nil {
		t.Fatalf("load tokens: %v", err)
	}
	tokens.now = func() time.Time { return time.Unix(f.clock.Load(), 0) }

	// The base server is left on the default (minimal) profile on purpose: a
	// token's scope must come from the tokens file and not from the profile
	// the process happened to start with.
	ctx := context.Background()
	base, closeServer, err := openAgentServer(ctx, g, agentServerOptions{AuditPath: f.audit})
	if err != nil {
		t.Fatalf("open agent server: %v", err)
	}
	t.Cleanup(closeServer)

	team, closeTeam, err := buildTeamAccess(ctx, base, tokens, restapi.Info{Project: "fixture", Version: "test"})
	if err != nil {
		t.Fatalf("build team access: %v", err)
	}
	t.Cleanup(closeTeam)

	h := newHTTPHandler(base.SDK(), nil, httpServeOptions{Team: team, AllowedHosts: allowedHosts},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	f.url = ts.URL
	return f
}

// restTools lists the tool names a token sees over REST.
func restTools(t *testing.T, f *teamFixture, name string) []string {
	t.Helper()
	status, body := restDo(t, http.MethodGet, f.url+restapi.BasePath+"/tools", "", f.bearer(name))
	if status != http.StatusOK {
		t.Fatalf("%s: GET tools = %d\n%s", name, status, body)
	}
	var listed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

func mcpTools(t *testing.T, f *teamFixture, name string) []string {
	t.Helper()
	cs, err := connect(t, f.url+mcpPath, f.bearer(name))
	if err != nil {
		t.Fatalf("%s: connect: %v", name, err)
	}
	defer func() { _ = cs.Close() }()
	names := listedTools(t, cs)
	sort.Strings(names)
	return names
}

// withoutTools is names, sorted, less the ones dropped.
func withoutTools(names []string, drop ...string) []string {
	skip := map[string]bool{}
	for _, d := range drop {
		skip[d] = true
	}
	var out []string
	for _, n := range names {
		if !skip[n] {
			out = append(out, n)
		}
	}
	return sortedCopy(out)
}

func sortedCopy(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}

// Each token sees its own scope, the same one on both protocols.
func TestTeamTokensSeeTheirOwnScopeOnBothProtocols(t *testing.T) {
	f := teamTestServer(t)

	want := map[string][]string{
		// The fixture configures no estate, so the one full-profile tool
		// that needs one is not registered. The report tools need only the
		// runner every server built here gets.
		"wide":   withoutTools(devmcp.ToolsOf(config.MCPFull), "estate"),
		"narrow": sortedCopy(devmcp.ToolsOf(config.MCPMinimal)),
		"twin":   sortedCopy(devmcp.ToolsOf(config.MCPMinimal)),
		"single": {"callers"},
	}
	for name, tools := range want {
		if got := restTools(t, f, name); strings.Join(got, ",") != strings.Join(tools, ",") {
			t.Errorf("%s over REST sees %v, want %v", name, got, tools)
		}
		if got := mcpTools(t, f, name); strings.Join(got, ",") != strings.Join(tools, ",") {
			t.Errorf("%s over MCP sees %v, want %v", name, got, tools)
		}
	}
	if len(want["wide"]) <= len(want["narrow"]) {
		t.Fatal("the fixture's scopes do not differ; the test proves nothing")
	}

	// In scope: the call works, on both protocols.
	status, body := restDo(t, http.MethodPost, f.url+restapi.BasePath+"/tools/callers", `{"name":"Save"}`, f.bearer("single"))
	if status != http.StatusOK || !strings.Contains(body, "caller(s) of Save") {
		t.Errorf("single: callers over REST = %d\n%s", status, body)
	}
	cs, err := connect(t, f.url+mcpPath, f.bearer("single"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	if _, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "callers", Arguments: map[string]any{"name": "Save"}}); err != nil {
		t.Errorf("single: callers over MCP: %v", err)
	}
}

// Every route that could show or run a tool outside the scope, tried with a
// real tool the token may not use and with a name that was never a tool. The
// two must be answered identically, or the difference is the tool list.
func TestTeamScopeCannotBeEscaped(t *testing.T) {
	f := teamTestServer(t)
	const outOfScope, invented = "hotspots", "list_unicorns"

	// The wide token can use it, so it is a real tool and the fixture works.
	if status, body := restDo(t, http.MethodPost, f.url+restapi.BasePath+"/tools/"+outOfScope, `{}`, f.bearer("wide")); status != http.StatusOK {
		t.Fatalf("wide: %s = %d\n%s", outOfScope, status, body)
	}

	for _, who := range []string{"narrow", "single"} {
		auth := f.bearer(who)
		same := func(what, method, suffix, body string) {
			t.Helper()
			s1, b1 := restDo(t, method, f.url+restapi.BasePath+"/tools/"+outOfScope+suffix, body, auth)
			s2, b2 := restDo(t, method, f.url+restapi.BasePath+"/tools/"+invented+suffix, body, auth)
			if s1 != http.StatusNotFound || s1 != s2 || strings.ReplaceAll(b1, outOfScope, "X") != strings.ReplaceAll(b2, invented, "X") {
				t.Errorf("%s: %s answers an out-of-scope tool differently from an unknown one:\n  %d %s\n  %d %s",
					who, what, s1, strings.TrimSpace(b1), s2, strings.TrimSpace(b2))
			}
		}
		same("REST describe", http.MethodGet, "", "")
		same("REST call", http.MethodPost, "", `{}`)
		same("REST call with arguments", http.MethodPost, "", `{"limit":5}`)
		// Spellings a lax router might normalise into the real name.
		same("REST call, trailing slash", http.MethodPost, "/", `{}`)

		for _, variant := range []string{"LIST_HOTSPOTS", "hotspots%00", "%6cist_hotspots", "callers/../hotspots", "hotspots?x=1"} {
			status, body := restDo(t, http.MethodPost, f.url+restapi.BasePath+"/tools/"+variant, `{}`, auth)
			if status == http.StatusOK && strings.Contains(body, `"tool":"hotspots"`) {
				t.Errorf("%s: %q reached hotspots:\n%s", who, variant, body)
			}
		}

		// Nothing derived from the tool list mentions it.
		for _, route := range []string{"", "/tools", "/openapi.json"} {
			status, body := restDo(t, http.MethodGet, f.url+restapi.BasePath+route, "", auth)
			if status != http.StatusOK {
				t.Fatalf("%s: GET %s = %d", who, route, status)
			}
			if strings.Contains(body, outOfScope) {
				t.Errorf("%s: GET %s%s names %s", who, restapi.BasePath, route, outOfScope)
			}
		}

		// MCP: not listed, not callable, and the refusal is the unknown-tool one.
		cs, err := connect(t, f.url+mcpPath, auth)
		if err != nil {
			t.Fatalf("%s: connect: %v", who, err)
		}
		for _, name := range listedTools(t, cs) {
			if name == outOfScope {
				t.Errorf("%s: tools/list names %s", who, outOfScope)
			}
		}
		if strings.Contains(cs.InitializeResult().Instructions, outOfScope) {
			t.Errorf("%s: the session instructions name %s", who, outOfScope)
		}
		_, errScope := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: outOfScope, Arguments: map[string]any{}})
		_, errUnknown := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: invented, Arguments: map[string]any{}})
		if errScope == nil || errUnknown == nil {
			t.Errorf("%s: tools/call must fail for both: %v / %v", who, errScope, errUnknown)
		} else if strings.ReplaceAll(errScope.Error(), outOfScope, "X") != strings.ReplaceAll(errUnknown.Error(), invented, "X") {
			t.Errorf("%s: tools/call answers differently:\n  %v\n  %v", who, errScope, errUnknown)
		}
		// A client cannot talk its way into a wider scope through the request.
		if _, err := cs.CallTool(context.Background(), &sdk.CallToolParams{
			Meta:      sdk.Meta{"token_name": "wide", "profile": "full", "scope": "full"},
			Name:      outOfScope,
			Arguments: map[string]any{"profile": "full"},
		}); err == nil {
			t.Errorf("%s: %s became callable with a claim in _meta", who, outOfScope)
		}
		_ = cs.Close()
	}

	// The index document tells a token its own profile, not the widest one.
	if _, body := restDo(t, http.MethodGet, f.url+restapi.BasePath, "", f.bearer("narrow")); !strings.Contains(body, `"profile":"minimal"`) {
		t.Errorf("narrow: index document = %s", body)
	}
}

// A token is read from the Authorization header and nowhere else.
func TestTeamAcceptsATokenOnlyAsABearerHeader(t *testing.T) {
	f := teamTestServer(t)
	tok := f.tokens["wide"]
	q := url.QueryEscape(tok)
	tools := f.url + restapi.BasePath + "/tools"

	cases := []struct {
		name    string
		method  string
		url     string
		body    string
		headers map[string]string
	}{
		{"query: token", http.MethodGet, tools + "?token=" + q, "", nil},
		{"query: access_token", http.MethodGet, tools + "?access_token=" + q, "", nil},
		{"query: authorization", http.MethodGet, tools + "?authorization=Bearer+" + q, "", nil},
		{"query on /mcp", http.MethodPost, f.url + mcpPath + "?token=" + q, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, map[string]string{"Content-Type": "application/json"}},
		{"path segment on /mcp", http.MethodPost, f.url + mcpPath + "/" + tok, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, map[string]string{"Content-Type": "application/json"}},
		{"path segment on /api/v1", http.MethodGet, f.url + restapi.BasePath + "/" + tok, "", nil},
		{"fragment-like path", http.MethodGet, tools + ";token=" + q, "", nil},
		{"cookie", http.MethodGet, tools, "", map[string]string{"Cookie": "token=" + tok + "; Authorization=Bearer " + tok}},
		{"x-api-key", http.MethodGet, tools, "", map[string]string{"X-Api-Key": tok, "X-Auth-Token": tok}},
		{"proxy-authorization", http.MethodGet, tools, "", map[string]string{"Proxy-Authorization": "Bearer " + tok}},
		{"basic, token as password", http.MethodGet, tools, "", map[string]string{"Authorization": "Basic " + tok}},
		{"token scheme", http.MethodGet, tools, "", map[string]string{"Authorization": "Token " + tok}},
		{"bare token", http.MethodGet, tools, "", map[string]string{"Authorization": tok}},
		{"form body", http.MethodPost, f.url + restapi.BasePath + "/tools/callers", "token=" + q, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}},
		{"json body", http.MethodPost, f.url + restapi.BasePath + "/tools/callers", `{"name":"Save","token":"` + tok + `"}`, nil},
		{"two tokens in one header", http.MethodGet, tools, "", map[string]string{"Authorization": "Bearer " + f.tokens["narrow"] + " " + tok}},
		{"the hash as the token", http.MethodGet, tools, "", map[string]string{"Authorization": "Bearer " + hashToken(tok)}},
		{"the token's name", http.MethodGet, tools, "", map[string]string{"Authorization": "Bearer wide"}},
		{"empty bearer", http.MethodGet, tools, "", map[string]string{"Authorization": "Bearer "}},
		{"the single-server test token", http.MethodGet, tools, "", map[string]string{"Authorization": "Bearer " + testToken}},
	}
	for _, c := range cases {
		if status, body := restDo(t, c.method, c.url, c.body, c.headers); status != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401\n%s", c.name, status, body)
		}
	}
	if status, _ := restDo(t, http.MethodGet, tools, "", f.bearer("wide")); status != http.StatusOK {
		t.Fatalf("the control request failed: %d", status)
	}
	if _, err := connect(t, f.url+mcpPath+"/"+tok, nil); err == nil {
		t.Error("an MCP client connected with the token in the path")
	}
}

// GRAPHLUME_MCP_TOKEN is the single-token server's credential. Beside a
// tokens file it would be an unscoped way around every scope in the file.
func TestTeamIgnoresTheSingleTokenFromTheEnvironment(t *testing.T) {
	t.Setenv(mcpTokenEnv, testToken)
	f := teamTestServer(t)
	for _, target := range []string{f.url + restapi.BasePath + "/tools", f.url + mcpPath + "/" + testToken} {
		if status, _ := restDo(t, http.MethodGet, target, "", map[string]string{"Authorization": "Bearer " + testToken}); status != http.StatusUnauthorized {
			t.Errorf("%s: the environment token was accepted (status %d)", strings.TrimPrefix(target, f.url), status)
		}
	}
}

// An expired token and a token that never existed get one answer, byte for
// byte. A different answer for "expired" confirms a stolen token was real.
func TestTeamExpiredAndUnknownTokensGetTheSameAnswer(t *testing.T) {
	f := teamTestServer(t)

	answer := func(target, method string, headers map[string]string) string {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), method, target, strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		resp.Header.Del("Date")
		var head bytes.Buffer
		_ = resp.Header.Write(&head)
		return resp.Status + "\n" + head.String() + "\n" + string(body)
	}

	unknown := map[string]string{"Authorization": "Bearer " + mustToken(t)}
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, restapi.BasePath + "/tools"},
		{http.MethodPost, restapi.BasePath + "/tools/callers"},
		{http.MethodPost, mcpPath},
	} {
		expired := answer(f.url+route.path, route.method, f.bearer("expired"))
		if !strings.HasPrefix(expired, "401 ") {
			t.Fatalf("%s %s with an expired token: %s", route.method, route.path, expired)
		}
		for name, other := range map[string]string{
			"an unknown token": answer(f.url+route.path, route.method, unknown),
			"no token":         answer(f.url+route.path, route.method, nil),
		} {
			if other != expired {
				t.Errorf("%s %s: an expired token is answered differently from %s:\n--- expired\n%s\n--- other\n%s",
					route.method, route.path, name, expired, other)
			}
		}
		if strings.Contains(strings.ToLower(expired), "expir") {
			t.Errorf("%s %s: the answer says the token expired:\n%s", route.method, route.path, expired)
		}
	}
}

// Expiry is checked on every request, so a session opened in time does not
// outlive its token.
func TestTeamTokenStopsWorkingWhenItExpiresMidSession(t *testing.T) {
	f := teamTestServer(t)
	cs, err := connect(t, f.url+mcpPath, f.bearer("brief"))
	if err != nil {
		t.Fatalf("connect before expiry: %v", err)
	}
	defer func() { _ = cs.Close() }()
	if _, err := cs.ListTools(context.Background(), nil); err != nil {
		t.Fatalf("list tools before expiry: %v", err)
	}
	if status, _ := restDo(t, http.MethodGet, f.url+restapi.BasePath+"/tools", "", f.bearer("brief")); status != http.StatusOK {
		t.Fatalf("REST before expiry: %d", status)
	}

	f.clock.Add(2 * 60 * 60) // two hours later

	if _, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "callers", Arguments: map[string]any{"name": "Save"}}); err == nil {
		t.Error("an open MCP session kept working after its token expired")
	}
	if status, _ := restDo(t, http.MethodGet, f.url+restapi.BasePath+"/tools", "", f.bearer("brief")); status != http.StatusUnauthorized {
		t.Errorf("REST after expiry: %d, want 401", status)
	}
	if status, _ := restDo(t, http.MethodGet, f.url+restapi.BasePath+"/tools", "", f.bearer("wide")); status != http.StatusOK {
		t.Errorf("a token with no expiry stopped working: %d", status)
	}
}

// mcpPost sends one JSON-RPC message to /mcp and returns the status and the
// session id the server answered with.
func mcpPost(t *testing.T, f *teamFixture, token, session, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, f.url+mcpPath, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+f.tokens[token])
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, resp.Header.Get("Mcp-Session-Id")
}

// A session belongs to the token that opened it. Without that, a narrow token
// that learned a wide token's session id (from a proxy log, a shared terminal)
// would inherit the wide scope by quoting it.
func TestTeamSessionCannotBeContinuedByAnotherToken(t *testing.T) {
	f := teamTestServer(t)
	const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	const list = `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	const call = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"hotspots","arguments":{}}}`

	status, session := mcpPost(t, f, "wide", "", initialize)
	if status != http.StatusOK || session == "" {
		t.Fatalf("initialize as wide: status %d, session %q", status, session)
	}
	if status, _ := mcpPost(t, f, "wide", session, list); status != http.StatusOK {
		t.Fatalf("wide could not use its own session: %d", status)
	}
	for _, thief := range []string{"narrow", "single", "brief"} {
		for _, body := range []string{list, call} {
			if status, _ := mcpPost(t, f, thief, session, body); status != http.StatusForbidden {
				t.Errorf("%s continued wide's session: status %d, want 403", thief, status)
			}
		}
	}
	// Two tokens with the same scope are still two identities.
	status, narrowSession := mcpPost(t, f, "narrow", "", initialize)
	if status != http.StatusOK || narrowSession == "" {
		t.Fatalf("initialize as narrow: %d", status)
	}
	if status, _ := mcpPost(t, f, "twin", narrowSession, list); status != http.StatusForbidden {
		t.Errorf("twin continued narrow's session: status %d, want 403", status)
	}
	// And ending someone else's session is refused too.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodDelete, f.url+mcpPath, nil)
	req.Header.Set("Authorization", "Bearer "+f.tokens["narrow"])
	req.Header.Set("Mcp-Session-Id", session)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("narrow deleted wide's session: status %d, want 403", resp.StatusCode)
	}
}

// The checks that came before tokens still come before tokens.
func TestTeamKeepsTheHostAndOriginChecks(t *testing.T) {
	f := teamTestServer(t, "code.example.internal")
	target := f.url + restapi.BasePath + "/tools"
	auth := "Bearer " + f.tokens["wide"]

	do := func(host, origin string) int {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", auth)
		if host != "" {
			req.Host = host
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if got := do("attacker.example", ""); got != http.StatusForbidden {
		t.Errorf("a foreign Host with a valid token: %d, want 403", got)
	}
	if got := do("", "https://attacker.example"); got != http.StatusForbidden {
		t.Errorf("a foreign Origin with a valid token: %d, want 403", got)
	}
	if got := do("code.example.internal", "https://code.example.internal"); got != http.StatusOK {
		t.Errorf("the allowed proxy host: %d, want 200", got)
	}
	if status, body := restDo(t, http.MethodGet, f.url+healthPath, "", nil); status != http.StatusOK || strings.TrimSpace(body) != `{"status":"ok"}` {
		t.Errorf("healthz = %d %q", status, body)
	}
	if status, _ := restDo(t, http.MethodGet, f.url+"/admin", "", f.bearer("wide")); status != http.StatusNotFound {
		t.Errorf("an unknown path: %d, want 404", status)
	}
}

// The audit log says which token asked, on both protocols, and holds nothing
// that could be used as a credential.
func TestTeamAuditLogNamesTheTokenAndNeverHoldsIt(t *testing.T) {
	f := teamTestServer(t)

	if status, body := restDo(t, http.MethodPost, f.url+restapi.BasePath+"/tools/callers", `{"name":"Save"}`, f.bearer("single")); status != http.StatusOK {
		t.Fatalf("REST call: %d\n%s", status, body)
	}
	// A refused call belongs on the record, on both protocols: it is the one
	// a reviewer looks for.
	if status, _ := restDo(t, http.MethodPost, f.url+restapi.BasePath+"/tools/hotspots", `{}`, f.bearer("single")); status != http.StatusNotFound {
		t.Fatalf("single called hotspots over REST: %d", status)
	}
	cs, err := connect(t, f.url+mcpPath, f.bearer("narrow"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "callers", Arguments: map[string]any{"name": "Save"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "hotspots", Arguments: map[string]any{}}); err == nil {
		t.Fatal("narrow called hotspots")
	}
	_ = cs.Close()

	raw, err := os.ReadFile(f.audit)
	if err != nil {
		t.Fatal(err)
	}
	type event struct {
		Transport string `json:"transport"`
		TokenName string `json:"token_name"`
		Tool      string `json:"tool"`
		Outcome   string `json:"outcome"`
	}
	var got []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var ev event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("audit line is not JSON: %q", line)
		}
		got = append(got, ev.Transport+" "+ev.TokenName+" "+ev.Tool+" "+ev.Outcome)
	}
	want := []string{
		"rest single callers ok",
		"rest single hotspots protocol_error",
		"mcp narrow callers ok",
		"mcp narrow hotspots protocol_error",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("audit log:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for name, tok := range f.tokens {
		if strings.Contains(string(raw), tok) {
			t.Errorf("the audit log holds the %s token", name)
		}
		if strings.Contains(string(raw), hashToken(tok)) {
			t.Errorf("the audit log holds the hash of the %s token", name)
		}
	}
}

// serveCtx runs `graphlume serve ...` until it returns, or until it has
// printed its last start-up line and can be cancelled. It reports what was
// printed and how the command ended.
func serveCtx(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := NewRoot()
	var out syncBuffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"--project", root, "--quiet", "serve"}, args...))

	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()

	// A deadline, not a fixed sleep: the machine running this may be busy.
	deadline := time.After(60 * time.Second)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			return out.String(), err
		case <-tick.C:
			if strings.Contains(out.String(), "Send a token as") {
				cancel()
				return out.String(), <-done
			}
		case <-deadline:
			t.Fatalf("serve neither failed nor started within a minute:\n%s", out.String())
		}
	}
}

// A tokens file that cannot be read exactly must stop the server. Starting
// anyway, with the single token or with none, is how a typo becomes an open
// index.
func TestServeRefusesToStartOnABadTokensFile(t *testing.T) {
	root := queryFixture(t)
	t.Setenv(mcpTokenEnv, testToken) // present, to prove it is not a fallback

	missing := filepath.Join(t.TempDir(), "absent.jsonl")
	for name, path := range map[string]string{
		"missing":       missing,
		"empty":         writeTokensFile(t, 0o600, ""),
		"malformed":     writeTokensFile(t, 0o600, `{"name":"ci","sha256":"nope","profile":"minimal"}`),
		"half written":  writeTokensFile(t, 0o600, entryLine(t, "ci", mustToken(t), "minimal", ""), `{"name":"second","sha2`),
		"unknown scope": writeTokensFile(t, 0o600, entryLine(t, "ci", mustToken(t), "everything", "")),
	} {
		out, err := serveCtx(t, root, "--http", "127.0.0.1:0", "--tokens-file", path)
		if err == nil {
			t.Errorf("%s: the server started\n%s", name, out)
		}
		if strings.Contains(out, "MCP over HTTP:") {
			t.Errorf("%s: the server announced an endpoint before failing:\n%s", name, out)
		}
	}

	if out, err := serveCtx(t, root, "--tokens-file", missing); err == nil || !strings.Contains(err.Error(), "--http") {
		t.Errorf("--tokens-file without --http: err = %v\n%s", err, out)
	}
}

// A shared server follows the existing rule for listening beyond loopback and
// says what that rule protects: tokens cross the network in clear text.
func TestServeTeamFollowsTheLoopbackRule(t *testing.T) {
	root := queryFixture(t)
	tok := mustToken(t)
	path := writeTokensFile(t, 0o600, entryLine(t, "ci", tok, "minimal", ""))

	for _, addr := range []string{"0.0.0.0:0", ":0"} {
		out, err := serveCtx(t, root, "--http", addr, "--tokens-file", path)
		if err == nil {
			t.Fatalf("%s without --allow-remote: the server started\n%s", addr, out)
		}
		if !strings.Contains(err.Error(), "--allow-remote") || !strings.Contains(err.Error(), "TLS") {
			t.Errorf("%s: the refusal should name --allow-remote and TLS: %v", addr, err)
		}
	}

	// With the flag the server starts, and says what the flag accepted. The
	// warning is checked without listening on every interface from a test.
	for _, addr := range []string{"0.0.0.0:7788", ":7788", "192.168.1.5:7788", "[::]:7788", "example.internal:7788"} {
		if w := teamListenWarning(addr); !strings.Contains(w, "clear text") || !strings.Contains(w, "TLS") {
			t.Errorf("%s: warning = %q, want one about tokens in clear text", addr, w)
		}
	}
	for _, addr := range []string{"127.0.0.1:7788", "localhost:7788", "[::1]:7788"} {
		if w := teamListenWarning(addr); w != "" {
			t.Errorf("%s is loopback, but warned: %s", addr, w)
		}
	}
}

// Start-up prints who has access and with what scope. It must not print
// anything that works as, or helps guess, a credential.
func TestServeTeamStartupNamesTokensButShowsNoSecret(t *testing.T) {
	root := queryFixture(t)
	t.Setenv(mcpTokenEnv, testToken)
	ci, reviewer := mustToken(t), mustToken(t)
	path := writeTokensFile(t, 0o600,
		entryLine(t, "ci", ci, "minimal", "", "callers"),
		entryLine(t, "reviewer", reviewer, "full", "2099-12-31"))

	out, err := serveCtx(t, root, "--http", "127.0.0.1:0", "--tokens-file", path)
	if err != nil {
		t.Fatalf("serve: %v\n%s", err, out)
	}
	for _, want := range []string{
		"MCP over HTTP: http://127.0.0.1:", "REST API:", "2 named token(s)", "read once at start",
		"ci: role viewer, profile minimal, only callers, no expiry",
		"reviewer: role viewer, profile full, expires 2100-01-01T00:00:00Z",
		mcpTokenEnv + " is set and ignored",
		"path form is off",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("start-up output lacks %q:\n%s", want, out)
		}
	}
	for name, secret := range map[string]string{
		"a token": ci, "another token": reviewer, "a hash": hashToken(ci), "another hash": hashToken(reviewer), "the environment token": testToken,
	} {
		if strings.Contains(out, secret) {
			t.Errorf("start-up output shows %s:\n%s", name, out)
		}
	}
	// No single token is generated beside the file's.
	if _, err := os.Stat(filepath.Join(root, IndexDir, "mcp-token")); err == nil {
		t.Error("a single token was generated and written to .graphlume/mcp-token")
	}
}

// Without --tokens-file nothing here applies: one token, header or path.
func TestSingleTokenServerIsUnchangedWithoutATokensFile(t *testing.T) {
	ts := restTestServer(t)
	if status, _ := restDo(t, http.MethodGet, ts.URL+restapi.BasePath+"/tools", "", bearer); status != http.StatusOK {
		t.Errorf("bearer token: %d", status)
	}
	cs, err := connect(t, ts.URL+mcpPath+"/"+testToken, nil)
	if err != nil {
		t.Fatalf("the path form stopped working on a single-token server: %v", err)
	}
	_ = cs.Close()
	if status, _ := restDo(t, http.MethodGet, ts.URL+restapi.BasePath+"/tools", "",
		map[string]string{"Authorization": "Bearer " + mustToken(t)}); status != http.StatusUnauthorized {
		t.Errorf("a team-style token was accepted by a single-token server: %d", status)
	}
}
