package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sutrakam/graphlume/internal/config"
)

// fakeReports answers a report by the first words of its command and records
// every command it was asked to run.
type fakeReports struct {
	mu    sync.Mutex
	calls [][]string
	out   map[string]ReportOutput // key: command words joined by a space, flags left out
	err   map[string]error
}

func (f *fakeReports) Run(_ context.Context, args ...string) (ReportOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), args...))
	var words []string
	for _, a := range args {
		if a == "--" || strings.HasPrefix(a, "-") {
			break
		}
		words = append(words, a)
	}
	key := strings.Join(words, " ")
	json := len(args) > 0 && containsStr(args, "--json") || containsStr(args, "--format=json") || args[0] == "sbom"
	if e, ok := f.err[key]; ok {
		return ReportOutput{}, e
	}
	if o, ok := f.out[key+"|json"]; ok && json {
		return o, nil
	}
	if o, ok := f.out[key]; ok {
		return o, nil
	}
	return ReportOutput{Stdout: []byte(`{}`)}, nil
}

func (f *fakeReports) last() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return nil
	}
	return f.calls[len(f.calls)-1]
}

func (f *fakeReports) jsonCall(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if len(c) > 0 && c[0] == prefix && (containsStr(c, "--json") || containsStr(c, "--format=json")) {
			return c
		}
	}
	return nil
}

func containsStr(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func withReports(r ReportRunner) fullOption { return func(d *Deps) { d.Reports = r } }

func withRepoRoot(root string) fullOption { return func(d *Deps) { d.Config.RepoRoot = root } }

func reportResult(t *testing.T, fs fullServer, tool string, args map[string]any) (map[string]any, string, bool) {
	t.Helper()
	res := callTool(t, fs.sess, tool, args)
	var out map[string]any
	if res.StructuredContent != nil {
		raw, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(raw, &out)
	}
	return out, resultText(res), res.IsError
}

func TestReportToolsAreAbsentWithoutARunner(t *testing.T) {
	fs := newFullServer(t, seedChain)
	have := map[string]bool{}
	for _, n := range fs.srv.Tools() {
		have[n] = true
	}
	for _, name := range reportTools() {
		if have[name] {
			t.Errorf("%s is registered although no report runner is configured", name)
		}
	}
	if !have["structure"] {
		t.Error("structure needs no runner and must be registered")
	}
}

func TestReportToolsAreInTheFullProfileAndOnlyThere(t *testing.T) {
	fs := newFullServer(t, seedChain, withReports(&fakeReports{}))
	for _, name := range reportTools() {
		if !containsStr(fs.srv.Tools(), name) {
			t.Errorf("full profile with a runner does not expose %s", name)
		}
		if !containsStr(ToolsOf(config.MCPFull), name) {
			t.Errorf("ToolsOf(full) does not name %s", name)
		}
		if containsStr(ToolsOf(config.MCPStandard), name) {
			t.Errorf("standard names %s; it is a size its users chose", name)
		}
	}
	std := newFullServer(t, seedChain, withProfile(config.MCPStandard), withReports(&fakeReports{}))
	for _, name := range reportTools() {
		if containsStr(std.srv.Tools(), name) {
			t.Errorf("standard profile exposes %s", name)
		}
	}
}

func TestPolicyReturnsTheCommandsJSONAndAcceptsAVerdictExit(t *testing.T) {
	f := &fakeReports{out: map[string]ReportOutput{
		"policy check|json": {Stdout: []byte(`{"evaluated":["a","b"],"errors":2,"warnings":1,"passed":false,"violations":[]}`), ExitCode: 2},
		"policy check":      {Stdout: []byte("Policy check, main..HEAD\n\nERROR rule\n"), ExitCode: 2},
	}}
	fs := newFullServer(t, seedChain, withReports(f))
	out, text, isErr := reportResult(t, fs, "policy", map[string]any{"base": "-origin/main", "head": "HEAD~1"})
	if isErr {
		t.Fatalf("a policy that failed its rules is a finding, not a tool failure:\n%s", text)
	}
	if out["passed"] != false || out["errors"] != float64(2) {
		t.Errorf("structured result is not the command's JSON: %v", out)
	}
	if !strings.Contains(text, "Policy FAILED: 2 error(s)") || !strings.Contains(text, "ERROR rule") {
		t.Errorf("text:\n%s", text)
	}
	if !strings.Contains(text, "=== SOURCE EVIDENCE ===") {
		t.Errorf("repository content is not labelled as such:\n%s", text)
	}
	call := f.jsonCall("policy")
	if call == nil || !containsStr(call, "--base=-origin/main") || !containsStr(call, "--head=HEAD~1") || !containsStr(call, "--format=json") {
		t.Errorf("command = %v; a value starting with a dash must travel as --flag=value", call)
	}
}

func TestAReportCommandThatFailsIsAToolFailureWithItsMessage(t *testing.T) {
	f := &fakeReports{err: map[string]error{"policy check": errors.New("policy check: /x is not a git repository")}}
	fs := newFullServer(t, seedChain, withReports(f))
	_, text, isErr := reportResult(t, fs, "policy", map[string]any{})
	if !isErr || !strings.Contains(text, "is not a git repository") {
		t.Errorf("isErr=%v text:\n%s", isErr, text)
	}
}

func TestAnExitWithoutAReportIsAFailure(t *testing.T) {
	f := &fakeReports{out: map[string]ReportOutput{"deps|json": {Stdout: []byte("boom"), ExitCode: 1}}}
	fs := newFullServer(t, seedChain, withReports(f))
	if _, _, isErr := reportResult(t, fs, "deps", map[string]any{}); !isErr {
		t.Error("exit 1 with no report must fail")
	}
	f = &fakeReports{out: map[string]ReportOutput{"deps|json": {Stdout: []byte("not json")}}}
	fs = newFullServer(t, seedChain, withReports(f))
	if _, _, isErr := reportResult(t, fs, "deps", map[string]any{}); !isErr {
		t.Error("output that is not a JSON object must fail")
	}
}

func TestReportArgumentsAreValidated(t *testing.T) {
	fs := newFullServer(t, seedChain, withReports(&fakeReports{}))
	for name, c := range map[string]struct {
		tool string
		args map[string]any
	}{
		"bad confidence": {"arch", map[string]any{"min_confidence": "hgih"}},
		"negative top":   {"lessons", map[string]any{"top": -1}},
		"negative depth": {"tour", map[string]any{"depth": -1}},
		"bad tour floor": {"tour", map[string]any{"min_confidence": "x"}},
		"negative limit": {"vulns", map[string]any{"limit": -3}},
	} {
		if _, _, isErr := reportResult(t, fs, c.tool, c.args); !isErr {
			t.Errorf("%s: want an error result", name)
		}
	}
}

func TestVulnsOnlyReadsAdvisoriesInsideTheRepository(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sec"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sec", "osv.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "osv.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	f := &fakeReports{}
	fs := newFullServer(t, seedChain, withReports(f), withRepoRoot(root))
	for name, p := range map[string]string{
		"absolute":        filepath.Join(outside, "osv.json"),
		"dot-dot":         "../" + filepath.Base(outside) + "/osv.json",
		"symlink escape":  "link/osv.json",
		"missing":         "sec/none.json",
		"empty":           " ",
		"flag lookalike":  "--advisories=/etc/passwd",
		"home":            "~/osv.json",
		"absolute inside": filepath.Join(root, "sec", "osv.json"),
	} {
		if _, text, isErr := reportResult(t, fs, "vulns", map[string]any{"advisories": []string{p}}); !isErr {
			t.Errorf("%s: %q was accepted:\n%s", name, p, text)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("a refused path still ran a command: %v", f.calls)
	}

	if _, text, isErr := reportResult(t, fs, "vulns", map[string]any{"advisories": []string{"sec/osv.json"}, "ignore": []string{"GHSA-1", "--x"}}); isErr {
		t.Fatalf("a path inside the repository was refused:\n%s", text)
	}
	call := f.jsonCall("vulns")
	want, _ := filepath.EvalSymlinks(filepath.Join(root, "sec", "osv.json"))
	var gotAdv, gotIgnore []string
	for _, a := range call {
		if v, ok := strings.CutPrefix(a, "--advisories="); ok {
			gotAdv = append(gotAdv, v)
		}
		if v, ok := strings.CutPrefix(a, "--ignore="); ok {
			gotIgnore = append(gotIgnore, v)
		}
	}
	if len(gotAdv) != 1 || !pathsEqual(gotAdv[0], want) {
		t.Errorf("advisories passed = %v, want %s", gotAdv, want)
	}
	if len(gotIgnore) != 2 || gotIgnore[1] != "--x" {
		t.Errorf("ignored ids = %v; each must be a flag value", gotIgnore)
	}
	for _, a := range call {
		if a == "--fail-on" || strings.HasPrefix(a, "--fail-on") || strings.HasPrefix(a, "--out") {
			t.Errorf("command carries %q", a)
		}
	}
}

func pathsEqual(a, b string) bool {
	ra, _ := filepath.EvalSymlinks(a)
	rb, _ := filepath.EvalSymlinks(b)
	return ra == rb
}

func TestADRQueryAfterDoubleDashAndNoMatchIsAnAnswer(t *testing.T) {
	f := &fakeReports{err: map[string]error{"arch adr": errors.New(`no ADR matches "zzz"; 3 record(s) indexed`)}}
	fs := newFullServer(t, seedChain, withReports(f))
	out, text, isErr := reportResult(t, fs, "adr", map[string]any{"query": "-zzz"})
	if isErr {
		t.Fatalf("a query that matches nothing is an answer:\n%s", text)
	}
	if adrs, _ := out["adrs"].([]any); len(adrs) != 0 {
		t.Errorf("adrs = %v", out["adrs"])
	}
	call := f.jsonCall("arch")
	if len(call) < 2 || call[len(call)-2] != "--" || call[len(call)-1] != "-zzz" {
		t.Errorf("command = %v; the query must follow --", call)
	}
}

func TestLicensesRunsTheReadOnlyCommandAndPassesItsJSONThrough(t *testing.T) {
	report := `{"index_revision":1,"total":2,"by_ecosystem":{"go":1,"npm":1},"licenses_read":true,
	  "summary":{"by_category":{"permissive":1,"weak_copyleft":0,"strong_copyleft":0,"other":0,"unknown":1},
	             "by_source":{"declared-metadata":0,"license-file":1,"unknown":1},"by_confidence":{"exact":1}},
	  "dependencies":[
	    {"name":"golang.org/x/sys","version":"v0.1.0","ecosystem":"go","direct":false,"manifest":"go.mod","line":9,
	     "license":"BSD-3-Clause","source":"license-file","path":"~/go/pkg/mod/golang.org/x/sys@v0.1.0/LICENSE","confidence":"exact","category":"permissive"},
	    {"name":"lodash","version":"4.17.15","ecosystem":"npm","direct":true,"manifest":"web/package.json","line":4,
	     "license":"unknown","source":"unknown","category":"unknown","reason":"not installed under node_modules"}],
	  "elapsed_ms":3,"budget_spent":false,"note":"Nothing is downloaded."}`
	f := &fakeReports{out: map[string]ReportOutput{"licenses|json": {Stdout: []byte(report)}}}
	fs := newFullServer(t, seedChain, withReports(f))
	out, text, isErr := reportResult(t, fs, "licenses", map[string]any{})
	if isErr {
		t.Fatalf("licenses failed:\n%s", text)
	}
	if out["total"] != float64(2) || out["licenses_read"] != true {
		t.Errorf("result = %v", out)
	}
	rows, _ := out["dependencies"].([]any)
	if len(rows) != 2 {
		t.Fatalf("rows = %v", rows)
	}
	first := rows[0].(map[string]any)
	if first["license"] != "BSD-3-Clause" || first["source"] != "license-file" || first["confidence"] != "exact" {
		t.Errorf("first row = %v", first)
	}
	for _, want := range []string{"1 licence(s) found on this machine, 1 unknown", "permissive 1", "license-file 1",
		"BSD-3-Clause (license-file, exact, ~/go/pkg/mod", "not installed under node_modules"} {
		if !strings.Contains(text, want) {
			t.Errorf("the text lacks %q:\n%s", want, text)
		}
	}
	if len(f.calls) != 1 || strings.Join(f.calls[0], " ") != "licenses --json" {
		t.Errorf("licenses ran %v; it must run exactly `licenses --json`, the form that writes nothing", f.calls)
	}
}

func TestSBOMResultIsTheWholeDocument(t *testing.T) {
	doc := `{"bomFormat":"CycloneDX","specVersion":"1.5","components":[{"name":"a","version":"1","purl":"pkg:x/a@1"},{"name":"b","purl":"pkg:x/b"}]}`
	f := &fakeReports{out: map[string]ReportOutput{"sbom": {Stdout: []byte(doc)}}}
	fs := newFullServer(t, seedChain, withReports(f))
	out, text, _ := reportResult(t, fs, "sbom", map[string]any{"name": "shop"})
	if out["bomFormat"] != "CycloneDX" {
		t.Errorf("result = %v", out)
	}
	if !strings.Contains(text, "pkg:x/b") || !strings.Contains(text, "2 declared") {
		t.Errorf("text:\n%s", text)
	}
	if c := f.last(); !containsStr(c, "--name=shop") {
		t.Errorf("command = %v", c)
	}
}

func TestAReportOverTheSizeBoundIsRefusedWithGuidance(t *testing.T) {
	f := &fakeReports{out: map[string]ReportOutput{"sbom": {Stdout: []byte(`{"x":1}`), Truncated: true}}}
	fs := newFullServer(t, seedChain, withReports(f))
	_, text, isErr := reportResult(t, fs, "sbom", map[string]any{})
	if !isErr || !strings.Contains(text, "command line") {
		t.Errorf("isErr=%v text:\n%s", isErr, text)
	}
}

func TestReportTextIsBounded(t *testing.T) {
	big := strings.Repeat("x", 3*reportTextBytes)
	f := &fakeReports{out: map[string]ReportOutput{
		"tour|json": {Stdout: []byte(`{"stops":[{"entry":"` + big + `","where":"a.go:1","steps":[]}]}`)},
	}}
	fs := newFullServer(t, seedChain, withReports(f))
	_, text, _ := reportResult(t, fs, "tour", map[string]any{"limit": 500, "depth": 99})
	if len(text) > reportTextBytes+2048 {
		t.Errorf("text is %d bytes", len(text))
	}
	if !strings.Contains(text, "more byte(s)") {
		t.Error("the cut is not announced")
	}
	call := f.jsonCall("tour")
	if !containsStr(call, "--limit=50") || !containsStr(call, "--depth=6") {
		t.Errorf("bounds not applied: %v", call)
	}
}

// The tour's text is drawn from its JSON: one walk of the index per answer,
// not two, laid out as the command lays it out.
func TestTourRunsTheCommandOnceAndRendersItsText(t *testing.T) {
	f := &fakeReports{out: map[string]ReportOutput{
		"tour|json": {Stdout: []byte(`{"index_revision":1,"stops":[{"entry":"Serve","where":"cmd/main.go:10",` +
			`"routes":["GET /a"],"steps":[{"name":"Load","depth":1,"confidence":"high","called_at":"cmd/main.go:12"},` +
			`{"name":"Read","depth":2,"confidence":"exact","called_at":"load.go:4"}]}]}`)},
	}}
	fs := newFullServer(t, seedChain, withReports(f))
	_, text, isErr := reportResult(t, fs, "tour", map[string]any{})
	if isErr {
		t.Fatalf("tour failed:\n%s", text)
	}
	want := "1. Serve  (cmd/main.go:10)\n   serves GET /a\n   -> Load  [high, called at cmd/main.go:12]\n     -> Read  [exact, called at load.go:4]\n"
	if !strings.Contains(text, want) {
		t.Errorf("text does not lay the tour out as the command does:\n%s", text)
	}
	if n := len(f.calls); n != 1 {
		t.Errorf("tour ran %d commands, want 1: %v", n, f.calls)
	}
}

func TestEventsWrapsTheCommandsArrayAndFilters(t *testing.T) {
	f := &fakeReports{out: map[string]ReportOutput{
		"events|json": {Stdout: []byte(`[
  {"symbol_id":1,"broker":"kafka","topic":"orders.created","fqn":"event:kafka:orders.created",
   "producers":[{"symbol_id":2,"symbol_name":"Publish","symbol_fqn":"p.Publish","file_path":"p.go","line":3,"confidence":"high"}],
   "consumers":[{"symbol_id":3,"symbol_name":"Handle","symbol_fqn":"c.Handle","file_path":"c.go","line":7,"confidence":"high"}]},
  {"symbol_id":4,"broker":"rabbitmq","topic":"emails","fqn":"event:rabbitmq:emails"}
]`)},
	}}
	fs := newFullServer(t, seedChain, withReports(f))
	out, text, isErr := reportResult(t, fs, "events", map[string]any{})
	if isErr {
		t.Fatalf("events failed:\n%s", text)
	}
	if out["total"] != float64(2) || out["matched"] != float64(2) {
		t.Errorf("total/matched: %v %v", out["total"], out["matched"])
	}
	topics, _ := out["topics"].([]any)
	if len(topics) != 2 {
		t.Fatalf("topics: %v", out["topics"])
	}
	first, _ := topics[0].(map[string]any)
	if prods, _ := first["producers"].([]any); len(prods) != 1 {
		t.Errorf("a topic keeps the command's producers: %v", first)
	}
	if !strings.Contains(text, "orders.created") || !strings.Contains(text, "producers: 1") {
		t.Errorf("text:\n%s", text)
	}
	if c := f.last(); !containsStr(c, "--json") || c[0] != "events" {
		t.Errorf("command = %v", c)
	}

	out, text, _ = reportResult(t, fs, "events", map[string]any{"broker": "RabbitMQ"})
	if out["matched"] != float64(1) || !strings.Contains(text, "emails") || strings.Contains(text, "orders.created") {
		t.Errorf("broker filter: %v\n%s", out["matched"], text)
	}
	out, _, _ = reportResult(t, fs, "events", map[string]any{"topic": "ORDERS"})
	if out["matched"] != float64(1) {
		t.Errorf("topic filter matched %v", out["matched"])
	}
}

func TestEventsWithNoTopicsSaysSo(t *testing.T) {
	f := &fakeReports{out: map[string]ReportOutput{"events|json": {Stdout: []byte("null\n")}}}
	fs := newFullServer(t, seedChain, withReports(f))
	out, text, isErr := reportResult(t, fs, "events", map[string]any{})
	if isErr || out["total"] != float64(0) || !strings.Contains(text, "No event bus") {
		t.Errorf("isErr=%v out=%v\n%s", isErr, out, text)
	}
}
