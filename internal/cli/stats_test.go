package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	devmcp "github.com/sutrakam/graphlume/internal/mcp"
)

// flat collapses whitespace, so a sentence is found however it was wrapped.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

// recordedCalls makes real tool calls against the fixture with an audit log
// and returns the log's path: two successful calls and one the tool rejects.
func recordedCalls(t *testing.T) (root, logPath string) {
	t.Helper()
	root = queryFixture(t)
	logPath = filepath.Join(t.TempDir(), "audit.jsonl")
	for _, args := range [][]string{
		{"tool", "call", "callers", "name=Save", "--audit-log", logPath},
		{"tool", "call", "search", "query=MarketingReport", "--audit-log", logPath},
	} {
		if out, err := run(t, root, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	// A rejected call exits non-zero; it is still a recorded call.
	if _, err := run(t, root, "tool", "call", "callers", "name= ", "--audit-log", logPath); err == nil {
		t.Fatal("an empty name should be rejected; the test needs one failure")
	}
	return root, logPath
}

func TestStatsSummarisesARecordedLog(t *testing.T) {
	root, logPath := recordedCalls(t)

	out, err := run(t, root, "stats", logPath)
	if err != nil {
		t.Fatalf("stats: %v\n%s", err, out)
	}
	text := flat(out)
	for _, want := range []string{
		"3 call(s) from",
		"1 failed (33.3%)",
		"BY TOOL", "BY TRANSPORT", "BY DAY (UTC)", "SLOWEST CALLS",
		"callers 2 1 50.0%",
		"search 1 0 0.0%",
		"cli 3 1 33.3%",
		"EST. TOKENS",
		"Token figures are estimates: bytes / 4, rounded up",
		"no saving is reported because none has been measured",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("stats output lacks %q:\n%s", want, out)
		}
	}

	// The cited-files figure is labelled as a bound, with the sentence that
	// keeps it from being read as a saving.
	for _, want := range []string{
		"FILES THE ANSWERS NAMED",
		"an upper bound on what reading those files whole would have cost",
		"It is not a saving",
		"An agent with search would not necessarily have read them",
		// These calls came from the shell, where a repeat is counted again.
		"once per call: each 'tool call' is its own session",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("stats output lacks %q:\n%s", want, out)
		}
	}
	for _, banned := range []string{"saved", "savings", "fewer tokens", "reduction"} {
		if strings.Contains(strings.ToLower(text), banned) {
			t.Errorf("stats output contains %q, which reads as a saving claim:\n%s", banned, out)
		}
	}

	// The arguments say what someone was working on. They stay in the log.
	for _, private := range []string{"MarketingReport", "name=Save", `"Save"`, "arguments"} {
		if strings.Contains(out, private) {
			t.Errorf("stats printed %q from the call arguments:\n%s", private, out)
		}
	}
}

func TestStatsJSONMatchesTheLog(t *testing.T) {
	root, logPath := recordedCalls(t)

	// What the log itself adds up to, computed here from its lines.
	data, err := os.ReadFile(logPath) //nolint:gosec // test path under t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	var wantBytes, wantTokens, wantCitedBytes int64
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var ev devmcp.AuditEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("audit line is not JSON: %q", line)
		}
		wantBytes += ev.ResultBytes
		wantTokens += (ev.ResultBytes + 3) / 4
		wantCitedBytes += ev.CitedFileBytes
	}
	if wantCitedBytes == 0 {
		t.Fatal("no event carries a cited-file size; the fixture's answers name indexed files")
	}

	out, err := run(t, root, "--json", "stats", logPath)
	if err != nil {
		t.Fatalf("stats --json: %v\n%s", err, out)
	}
	var rep struct {
		File string `json:"file"`
		devmcp.AuditSummary
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("stats --json is not JSON: %v\n%s", err, out)
	}
	if rep.File != logPath || rep.Calls != 3 || rep.Failures != 1 {
		t.Errorf("file=%q calls=%d failures=%d", rep.File, rep.Calls, rep.Failures)
	}
	if rep.ResultBytes != wantBytes || rep.EstimatedResultTokens != wantTokens {
		t.Errorf("bytes=%d tokens=%d; the log's lines add up to %d and %d",
			rep.ResultBytes, rep.EstimatedResultTokens, wantBytes, wantTokens)
	}
	if rep.CitedFiles == nil || rep.CitedFiles.Bytes != wantCitedBytes {
		t.Errorf("cited_files = %+v, want %d bytes", rep.CitedFiles, wantCitedBytes)
	}
	if rep.TokenBasis != devmcp.BasisEstimated || rep.TokenEstimate == "" {
		t.Errorf("the JSON does not label its token figures as estimates: %q %q", rep.TokenBasis, rep.TokenEstimate)
	}
	if len(rep.Tools) != 2 || len(rep.Transports) != 1 || len(rep.Days) != 1 || len(rep.Slowest) != 3 {
		t.Errorf("tools=%d transports=%d days=%d slowest=%d", len(rep.Tools), len(rep.Transports), len(rep.Days), len(rep.Slowest))
	}
	if strings.Contains(out, "MarketingReport") {
		t.Errorf("the JSON summary carries call arguments:\n%s", out)
	}
}

func TestStatsReadsTheLogNamedByTheEnvironment(t *testing.T) {
	root, logPath := recordedCalls(t)
	t.Setenv(auditLogEnv, logPath)

	out, err := run(t, root, "stats")
	if err != nil {
		t.Fatalf("stats: %v\n%s", err, out)
	}
	if !strings.Contains(flat(out), "3 call(s) from") {
		t.Errorf("stats did not read $%s:\n%s", auditLogEnv, out)
	}
}

func TestStatsWithNoLogSaysHowToRecordOne(t *testing.T) {
	root := isolate(t)
	t.Setenv(auditLogEnv, "")

	_, err := run(t, root, "stats")
	if err == nil {
		t.Fatal("stats with no log named should fail")
	}
	for _, want := range []string{auditLogEnv, "--audit-log"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
	}

	if _, err := run(t, root, "stats", filepath.Join(root, "no-such-log.jsonl")); err == nil {
		t.Error("stats on a missing file should fail")
	}
}

func TestStatsReadsStandardInput(t *testing.T) {
	root := isolate(t)
	log := `{"time":"2026-11-12T09:00:00Z","transport":"mcp","tool":"callers","duration_ms":4,"outcome":"ok","result_bytes":1000}` + "\n" +
		"a line that is not an event\n"

	cmd := NewRoot()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(log))
	cmd.SetArgs([]string{"--project", root, "--quiet", "stats", "-"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("stats -: %v\n%s", err, out.String())
	}
	text := flat(out.String())
	for _, want := range []string{
		"Audit log: standard input",
		"1 call(s) from 2026-11-12T09:00:00Z",
		"an estimated 250 tokens",
		"1 line(s) were not audit events and were skipped",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	// An older log has no cited-file sizes, and the section is left out
	// rather than printed as zero.
	if strings.Contains(text, "FILES THE ANSWERS NAMED") {
		t.Errorf("a log with no cited-file sizes printed the section:\n%s", out.String())
	}
}

// A log of MCP sessions counts each file once per session, so the caveat about
// shell calls would be noise there; the bound itself is still stated.
func TestStatsCitedFilesNoteFitsTheTransport(t *testing.T) {
	root := isolate(t)
	logPath := filepath.Join(t.TempDir(), "audit.jsonl")
	line := `{"time":"2026-11-12T09:00:00Z","transport":"mcp","tool":"callers","duration_ms":4,"outcome":"ok","result_bytes":1000,"cited_files":2,"cited_file_bytes":9000}` + "\n"
	if err := os.WriteFile(logPath, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, root, "stats", logPath)
	if err != nil {
		t.Fatalf("stats: %v\n%s", err, out)
	}
	text := flat(out)
	if !strings.Contains(text, "mcp 2 9,000 2,250 once per session") ||
		!strings.Contains(text, "It is not a saving") {
		t.Errorf("the cited-files section is missing or unlabelled:\n%s", out)
	}
	if strings.Contains(text, "once per call") {
		t.Errorf("a log with no shell calls carries the shell caveat:\n%s", out)
	}
}

func TestStatsFiltersByDate(t *testing.T) {
	root := isolate(t)
	logPath := filepath.Join(t.TempDir(), "audit.jsonl")
	var log strings.Builder
	for _, day := range []string{"2026-11-01", "2026-11-02", "2026-11-03"} {
		log.WriteString(`{"time":"` + day + `T12:00:00Z","transport":"mcp","tool":"callers","duration_ms":1,"outcome":"ok"}` + "\n")
	}
	if err := os.WriteFile(logPath, []byte(log.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, root, "stats", logPath, "--since", "2026-11-02", "--until", "2026-11-03T00:00:00Z")
	if err != nil {
		t.Fatalf("stats: %v\n%s", err, out)
	}
	text := flat(out)
	if !strings.Contains(text, "1 call(s) from 2026-11-02T12:00:00Z") ||
		!strings.Contains(text, "2 call(s) fell outside --since/--until") {
		t.Errorf("the date range was not applied:\n%s", out)
	}

	for _, bad := range [][]string{
		{"stats", logPath, "--since", "last tuesday"},
		{"stats", logPath, "--since", "2026-11-03", "--until", "2026-11-02"},
		{"stats", logPath, "--slowest", "-1"},
	} {
		if _, err := run(t, root, bad...); err == nil {
			t.Errorf("%v should be rejected", bad)
		}
	}
}

func TestStatsOnAnEmptyLog(t *testing.T) {
	root := isolate(t)
	logPath := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, root, "stats", logPath)
	if err != nil {
		t.Fatalf("stats: %v\n%s", err, out)
	}
	if !strings.Contains(out, "No tool calls recorded.") {
		t.Errorf("an empty log should say so:\n%s", out)
	}
}

// The agent tool is reachable from the shell like every other, and says why a
// shell session has nothing in it.
func TestToolCallGetSessionStats(t *testing.T) {
	root := queryFixture(t)

	out, err := run(t, root, "tool", "call", "stats")
	if err != nil {
		t.Fatalf("stats: %v\n%s", err, out)
	}
	text := flat(out)
	for _, want := range []string{
		"This session has made no tool calls before this one.",
		"session: transport=cli calls=0",
		"tool_definitions: profile=full",
		"est_tokens_per_request=",
		"graphlume stats <file>",
		"This is usage, not a saving",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("answer lacks %q:\n%s", want, out)
		}
	}

	raw, err := run(t, root, "--json", "tool", "call", "stats")
	if err != nil {
		t.Fatalf("stats --json: %v\n%s", err, raw)
	}
	var rep struct {
		Result struct {
			SessionStats *devmcp.SessionStats `json:"session_stats"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	st := rep.Result.SessionStats
	if st == nil || st.Transport != "cli" || st.ToolDefinitions == nil || st.ToolDefinitions.EstimatedTokensPerRequest == 0 {
		t.Errorf("session_stats = %+v", st)
	}

	// On a profile that does not expose it, the tool is not there to call.
	if _, err := run(t, root, "tool", "call", "stats", "--profile", "standard"); err == nil {
		t.Error("stats is full-profile only; the standard profile should not have it")
	}
}

// The schema cost stats reports is the one `tool list` prints.
func TestSessionStatsDefinitionCostMatchesToolList(t *testing.T) {
	root := queryFixture(t)

	listed, err := run(t, root, "--json", "tool", "list")
	if err != nil {
		t.Fatalf("tool list: %v\n%s", err, listed)
	}
	var list struct {
		Total int `json:"total_estimated_tokens"`
	}
	if err := json.Unmarshal([]byte(listed), &list); err != nil {
		t.Fatal(err)
	}

	raw, err := run(t, root, "--json", "tool", "call", "stats")
	if err != nil {
		t.Fatalf("stats: %v\n%s", err, raw)
	}
	var rep struct {
		Result struct {
			SessionStats devmcp.SessionStats `json:"session_stats"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		t.Fatal(err)
	}
	if got := rep.Result.SessionStats.ToolDefinitions; got == nil || got.EstimatedTokensPerRequest != list.Total {
		t.Errorf("stats says %+v; 'tool list' says %d tokens", got, list.Total)
	}
}
