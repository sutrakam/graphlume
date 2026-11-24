package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sutrakam/graphlume/internal/config"
)

// connectTo opens an in-memory MCP session against srv.
func connectTo(t *testing.T, srv *Server) *sdk.ClientSession {
	t.Helper()
	clientT, serverT := sdk.NewInMemoryTransports()
	go func() { _ = srv.Run(context.Background(), serverT) }()
	sess, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil).
		Connect(context.Background(), clientT, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func toolNames(t *testing.T, sess *sdk.ClientSession) []string {
	t.Helper()
	res, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

func sorted(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}

// A scope is the profile, or the profile narrowed to an allow-list, and the
// wide server it was cut from does not show through.
func TestScopedServerExposesExactlyItsScope(t *testing.T) {
	full := newFullServer(t, seedChain).srv

	minimal, err := full.Scoped(config.MCPMinimal, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := toolNames(t, connectTo(t, minimal)), sorted(ToolsOf(config.MCPMinimal)); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("minimal scope lists %v, want %v", got, want)
	}

	narrow, err := full.Scoped(config.MCPStandard, []string{"callers", "impact"})
	if err != nil {
		t.Fatal(err)
	}
	if got := toolNames(t, connectTo(t, narrow)); strings.Join(got, ",") != "callers,impact" {
		t.Errorf("allow-list scope lists %v, want exactly impact and callers", got)
	}

	// The scope does not depend on the server it was made from: a wide scope
	// can be cut from a narrow server.
	wide, err := minimal.Scoped(config.MCPFull, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(toolNames(t, connectTo(t, wide))), len(full.Tools()); got != want {
		t.Errorf("full scope cut from a minimal server lists %d tools, want %d", got, want)
	}
}

// The property the design exists for: a tool outside the scope is answered
// exactly like a tool that was never written. If the two answers differed in
// any byte, calling names would enumerate the tools a token is being denied.
func TestOutOfScopeToolIsIndistinguishableFromAnUnknownOne(t *testing.T) {
	full := newFullServer(t, seedChain).srv
	scoped, err := full.Scoped(config.MCPMinimal, nil)
	if err != nil {
		t.Fatal(err)
	}
	sess := connectTo(t, scoped)
	ctx := context.Background()

	const outOfScope, invented = "hotspots", "list_unicorns"
	if !full.hasTool(outOfScope) {
		t.Fatalf("%s must be a real tool for this test to mean anything", outOfScope)
	}
	_, errScope := sess.CallTool(ctx, &sdk.CallToolParams{Name: outOfScope, Arguments: map[string]any{}})
	_, errUnknown := sess.CallTool(ctx, &sdk.CallToolParams{Name: invented, Arguments: map[string]any{}})
	if errScope == nil || errUnknown == nil {
		t.Fatalf("both calls must fail: out of scope = %v, invented = %v", errScope, errUnknown)
	}
	if got, want := strings.ReplaceAll(errScope.Error(), outOfScope, "X"), strings.ReplaceAll(errUnknown.Error(), invented, "X"); got != want {
		t.Errorf("an out-of-scope tool answers differently from an unknown one:\n  out of scope: %s\n  unknown:      %s", errScope, errUnknown)
	}

	// The same holds for the in-process front end the REST API uses.
	inv, err := scoped.NewInvokerAs(ctx, "rest", "narrow")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = inv.Close() }()
	for _, name := range []string{outOfScope, invented} {
		if _, err := inv.Call(ctx, name, nil); !errors.Is(err, ErrUnknownTool) {
			t.Errorf("Invoker.Call(%s) = %v, want ErrUnknownTool", name, err)
		}
	}
	tools, err := inv.Tools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		if tool.Name == outOfScope {
			t.Errorf("the scoped invoker lists %s", outOfScope)
		}
	}
}

// An allow-list removes tools from a profile. It must never add one.
func TestAllowListCannotReachPastTheProfile(t *testing.T) {
	full := newFullServer(t, seedChain).srv

	if _, err := full.Scoped(config.MCPMinimal, []string{"callers", "hotspots"}); err == nil {
		t.Error("a minimal scope accepted hotspots, which only the full profile exposes")
	}
	if _, err := full.Scoped(config.MCPMinimal, []string{"find_calers"}); err == nil {
		t.Error("a misspelt tool name was accepted; it would have narrowed the scope to nothing, silently")
	}
	if _, err := full.Scoped("admin", nil); err == nil {
		t.Error("an unknown profile was accepted")
	}
	if got := toolFilter(config.MCPMinimal, []string{"hotspots"}); got == nil || len(got) != 0 {
		t.Errorf("toolFilter(minimal, [hotspots]) = %v, want an empty, non-nil set", got)
	}
}

// ToolsOf is what `serve create-token` checks a tool name against before any
// server exists, so it has to agree with what a server registers.
func TestToolsOfMatchesWhatAServerRegisters(t *testing.T) {
	for _, p := range []config.MCPProfile{config.MCPMinimal, config.MCPStandard, config.MCPFull} {
		// With an estate and a report runner, so the tools that register only
		// when their dependency is configured are registered too.
		srv := newFullServer(t, seedChain, withProfile(p), withEstate(newFakeEstate()), withReports(&fakeReports{})).srv
		if got, want := sorted(ToolsOf(p)), sorted(srv.Tools()); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: ToolsOf = %v, a server registers %v", p, got, want)
		}
	}
}

// Instructions and prompts are derived from the tool list, so they are two
// more places a scope could show a tool it withholds.
func TestScopedGuidanceNamesOnlyToolsInScope(t *testing.T) {
	full := newFullServer(t, seedChain).srv
	allow := []string{"search", "definition"}
	scoped, err := full.Scoped(config.MCPStandard, allow)
	if err != nil {
		t.Fatal(err)
	}
	sess := connectTo(t, scoped)

	inScope := map[string]bool{"search": true, "definition": true}
	guidance := sess.InitializeResult().Instructions
	if !strings.Contains(guidance, "definition") {
		t.Errorf("instructions should say how to use a tool the scope has:\n%s", guidance)
	}
	if len(guidance) > maxInstructionBytes {
		t.Errorf("scoped instructions are %d bytes, over the budget", len(guidance))
	}
	for _, name := range full.Tools() {
		if !inScope[name] && strings.Contains(guidance, name) {
			t.Errorf("instructions name %s, which this scope cannot call:\n%s", name, guidance)
		}
	}

	prompts, err := sess.ListPrompts(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range prompts.Prompts {
		t.Errorf("prompt %s is offered, but every prompt needs a tool this scope lacks", p.Name)
	}

	// A profile's text is the fixed one: scoping must not have reworded it.
	const lookups = "Start a task with context; look up one thing with symbol or definition; " +
		"use callers before changing a signature."
	for _, p := range []config.MCPProfile{config.MCPMinimal, config.MCPStandard, config.MCPFull} {
		if !strings.Contains(serverInstructions(p), lookups) {
			t.Errorf("%s: the profile's instructions changed:\n%s", p, serverInstructions(p))
		}
	}
	if len(scoped.Prompts()) != 0 || len(full.Prompts()) != 3 {
		t.Errorf("prompts: scoped has %d (want 0), an unscoped server has %d (want 3)", len(scoped.Prompts()), len(full.Prompts()))
	}
}

// The audit log names the token behind a call, and nothing a client sends can
// name a different one.
func TestAuditRecordsTheTokenNameAndNothingAClientClaims(t *testing.T) {
	var buf bytes.Buffer
	full := newFullServer(t, seedPaymentGraph, withAudit(NewAuditLog(&buf)))
	scoped, err := full.srv.Scoped(config.MCPMinimal, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	inv, err := scoped.NewInvokerAs(ctx, "rest", "ci-bot")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inv.Call(ctx, "search", json.RawMessage(`{"query":"handleCallback"}`)); err != nil {
		t.Fatal(err)
	}
	// A refused call through the front end is on the record too, under the
	// same name, with an oversized tool name cut.
	if _, err := inv.Call(ctx, "hotspots", json.RawMessage(`{"limit":3}`)); !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("hotspots through a minimal invoker: %v", err)
	}
	if _, err := inv.Call(ctx, strings.Repeat("x", 5000), nil); !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("an invented tool: %v", err)
	}
	_ = inv.Close()

	// A protocol client with no token identity, trying to borrow one through
	// the request's own metadata.
	sess := connectTo(t, scoped)
	if _, err := sess.CallTool(ctx, &sdk.CallToolParams{
		Meta:      sdk.Meta{"token_name": "admin", "graphlume/token": "admin"},
		Name:      "search",
		Arguments: map[string]any{"query": "handleCallback"},
	}); err != nil {
		t.Fatal(err)
	}
	// And a refused call, which should be on the record too.
	if _, err := sess.CallTool(ctx, &sdk.CallToolParams{Name: "hotspots", Arguments: map[string]any{}}); err == nil {
		t.Fatal("hotspots was callable on a minimal scope")
	}

	var events []AuditEvent
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var ev AuditEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("audit line is not JSON: %q: %v", line, err)
		}
		events = append(events, ev)
	}
	if len(events) != 5 {
		t.Fatalf("recorded %d events, want 5 (the scoped server must share the audit log):\n%s", len(events), buf.String())
	}
	if events[0].TokenName != "ci-bot" || events[0].Transport != "rest" || events[0].Outcome != "ok" {
		t.Errorf("REST call recorded as %+v, want token_name ci-bot over rest", events[0])
	}
	if ev := events[1]; ev.TokenName != "ci-bot" || ev.Transport != "rest" || ev.Tool != "hotspots" ||
		ev.Outcome != "protocol_error" || !strings.Contains(string(ev.Arguments), "limit") {
		t.Errorf("refused REST call recorded as %+v, want hotspots / protocol_error under ci-bot", ev)
	}
	if len(events[2].Tool) != auditToolBytes {
		t.Errorf("an unmatched tool name was recorded at %d bytes, want it cut to %d", len(events[2].Tool), auditToolBytes)
	}
	if events[3].TokenName != "" {
		t.Errorf("a client named its own token through _meta: recorded %q", events[3].TokenName)
	}
	if events[4].Tool != "hotspots" || events[4].Outcome != "protocol_error" {
		t.Errorf("the refused call was recorded as %+v, want hotspots / protocol_error", events[4])
	}
}

// Without named tokens the record is what it always was: no token_name key.
func TestAuditOmitsTheTokenNameWithoutNamedTokens(t *testing.T) {
	var buf bytes.Buffer
	fs := newFullServer(t, seedChain, withAudit(NewAuditLog(&buf)))
	callTool(t, fs.sess, "search", map[string]any{"query": "entry"})
	if strings.Contains(buf.String(), "token_name") {
		t.Errorf("an unscoped server wrote a token_name:\n%s", buf.String())
	}
}
