package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// auditLine is one log line as the server writes it.
func auditLine(t *testing.T, ev AuditEvent) string {
	t.Helper()
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func groupNamed(rows []AuditGroup, name string) (AuditGroup, bool) {
	for _, r := range rows {
		if r.Name == name {
			return r, true
		}
	}
	return AuditGroup{}, false
}

func TestSummarizeAuditTotalsByToolTransportAndDay(t *testing.T) {
	var log strings.Builder
	for _, ev := range []AuditEvent{
		{Time: "2026-11-10T09:00:00Z", Transport: "mcp", Tool: "callers", DurationMS: 4, Outcome: "ok", ResultBytes: 1001, StructuredBytes: 1200, CitedFiles: 2, CitedFileBytes: 5000},
		{Time: "2026-11-10T09:01:00Z", Transport: "mcp", Tool: "callers", DurationMS: 9, Outcome: "tool_error", ResultBytes: 120},
		{Time: "2026-11-11T23:59:59Z", Transport: "cli", Tool: "hotspots", DurationMS: 3300, Outcome: "ok", ResultBytes: 2400, CitedFiles: 1, CitedFileBytes: 700},
		{Time: "2026-11-12T00:00:00Z", Transport: "rest", Tool: "search", DurationMS: 2, Outcome: "ok", ResultBytes: 4},
		{Time: "2026-11-12T08:00:00Z", Transport: "mcp", Tool: "no_such_tool", DurationMS: 0, Outcome: "protocol_error"},
	} {
		log.WriteString(auditLine(t, ev))
	}

	sum, err := SummarizeAudit(strings.NewReader(log.String()), AuditSummaryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Calls != 5 || sum.Failures != 2 || sum.FailureRate != 0.4 {
		t.Errorf("calls=%d failures=%d rate=%v, want 5, 2 and 0.4", sum.Calls, sum.Failures, sum.FailureRate)
	}
	if sum.ResultBytes != 3525 || sum.StructuredBytes != 1200 || sum.DurationMS != 3315 {
		t.Errorf("bytes=%d structured=%d ms=%d, want 3525, 1200, 3315", sum.ResultBytes, sum.StructuredBytes, sum.DurationMS)
	}
	// Estimated per call and summed: 251 + 30 + 600 + 1 + 0.
	if sum.EstimatedResultTokens != 882 {
		t.Errorf("estimated tokens = %d, want 882", sum.EstimatedResultTokens)
	}
	if sum.First != "2026-11-10T09:00:00Z" || sum.Last != "2026-11-12T08:00:00Z" {
		t.Errorf("first=%q last=%q", sum.First, sum.Last)
	}

	callers, ok := groupNamed(sum.Tools, "callers")
	if !ok || callers.Calls != 2 || callers.ToolErrors != 1 || callers.FailureRate != 0.5 ||
		callers.ResultBytes != 1121 || callers.DurationMS != 13 || callers.SlowestMS != 9 {
		t.Errorf("callers = %+v", callers)
	}
	if sum.Tools[0].Name != "callers" {
		t.Errorf("the most called tool should be first, got %q", sum.Tools[0].Name)
	}
	// Whatever name a failed call sent, no_such_tool here, is well formed and
	// is kept; its failure is a protocol error.
	if unknown, ok := groupNamed(sum.Tools, "(not a tool name)"); ok {
		t.Errorf("a well-formed name was lumped as invalid: %+v", unknown)
	}

	mcp, _ := groupNamed(sum.Transports, "mcp")
	if mcp.Calls != 3 || mcp.ProtocolErrors != 1 || mcp.ToolErrors != 1 || mcp.CitedFileBytes != 5000 {
		t.Errorf("mcp transport = %+v", mcp)
	}
	if cli, _ := groupNamed(sum.Transports, "cli"); cli.Calls != 1 || cli.CitedFiles != 1 {
		t.Errorf("cli transport = %+v", cli)
	}

	var days []string
	for _, d := range sum.Days {
		days = append(days, fmt.Sprintf("%s=%d", d.Name, d.Calls))
	}
	if got := strings.Join(days, " "); got != "2026-11-10=2 2026-11-11=1 2026-11-12=2" {
		t.Errorf("days = %q; they are UTC calendar days, oldest first", got)
	}

	if len(sum.Slowest) != 5 || sum.Slowest[0].Tool != "hotspots" || sum.Slowest[0].DurationMS != 3300 ||
		sum.Slowest[1].DurationMS != 9 {
		t.Errorf("slowest = %+v", sum.Slowest)
	}
	if sum.CitedFiles == nil || sum.CitedFiles.Files != 3 || sum.CitedFiles.Bytes != 5700 || sum.CitedFiles.EstimatedTokens != 1425 {
		t.Errorf("cited files = %+v", sum.CitedFiles)
	}
	// The figure says what it is inside the JSON that carries it, where no
	// footnote reaches.
	if raw, _ := json.Marshal(sum.CitedFiles); !strings.Contains(string(raw), "upper bound on reading those files whole, not a saving") ||
		!strings.Contains(string(raw), "each 'graphlume tool call' is a session of its own") {
		t.Errorf("the cited-files JSON does not say what it is: %s", raw)
	}
	if sum.TokenBasis != BasisEstimated || sum.TokenEstimate != EstimateMethod {
		t.Errorf("token figures are not labelled as estimates: %q %q", sum.TokenBasis, sum.TokenEstimate)
	}
}

// The summary a server's own log produces must agree with what the server
// reported for the same calls while the session was live.
func TestAuditSummaryAgreesWithTheSessionReport(t *testing.T) {
	var log bytes.Buffer
	fs := newFullServer(t, seedChain, withAudit(NewAuditLog(&log)))

	callTool(t, fs.sess, "callers", map[string]any{"name": "leaf"})
	callTool(t, fs.sess, "callers", map[string]any{"name": " "})
	callTool(t, fs.sess, "search", map[string]any{"query": "leaf"})
	callTool(t, fs.sess, "impact", map[string]any{"symbol": "leaf"})
	st, _ := sessionStatsOf(t, fs.sess)

	// The report call is itself in the log; leave it out to compare like with like.
	var kept strings.Builder
	for _, line := range strings.SplitAfter(log.String(), "\n") {
		if !strings.Contains(line, `"stats"`) {
			kept.WriteString(line)
		}
	}
	sum, err := SummarizeAudit(strings.NewReader(kept.String()), AuditSummaryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Calls != st.Calls || sum.Failures != st.Failures || sum.ResultBytes != st.ResultBytes ||
		sum.EstimatedResultTokens != st.EstimatedResultTokens || sum.StructuredBytes != st.StructuredBytes {
		t.Errorf("the log sums to calls=%d failures=%d bytes=%d tokens=%d structured=%d;\nthe session reported %d, %d, %d, %d, %d",
			sum.Calls, sum.Failures, sum.ResultBytes, sum.EstimatedResultTokens, sum.StructuredBytes,
			st.Calls, st.Failures, st.ResultBytes, st.EstimatedResultTokens, st.StructuredBytes)
	}
	if st.CitedFiles == nil || sum.CitedFiles == nil ||
		sum.CitedFiles.Files != st.CitedFiles.Files || sum.CitedFiles.Bytes != st.CitedFiles.Bytes ||
		sum.CitedFiles.EstimatedTokens != st.CitedFiles.EstimatedTokens {
		t.Errorf("cited files: log %+v, session %+v", sum.CitedFiles, st.CitedFiles)
	}
	for _, u := range st.Tools {
		row, ok := groupNamed(sum.Tools, u.Tool)
		if !ok || row.Calls != u.Calls || row.ResultBytes != u.ResultBytes || row.EstimatedResultTokens != u.EstimatedResultTokens {
			t.Errorf("%s: log %+v, session %+v", u.Tool, row, u)
		}
	}
}

// A log is a file someone left on for months. Lines that are not events are
// counted and skipped; none of them stops the summary.
func TestSummarizeAuditSkipsWhatIsNotAnEvent(t *testing.T) {
	good := auditLine(t, AuditEvent{Time: "2026-11-12T09:00:00Z", Transport: "cli", Tool: "callers", Outcome: "ok", ResultBytes: 10})
	log := good +
		"\n" + // blank: ignored, not counted as damage
		"not json at all\n" +
		`{"time":"yesterday","transport":"cli","tool":"callers","outcome":"ok"}` + "\n" +
		`{"time":"2026-11-12T09:00:00Z","transport":"cli","tool":"","outcome":"ok"}` + "\n" +
		`{"time":"2026-11-12T09:00:00Z","transport":"cli","tool":"callers","outcome":"exploded"}` + "\n" +
		`{"time":"2026-11-12T09:00:00Z","transport":"cli","tool":"callers","outcome":"ok","padding":"` +
		strings.Repeat("x", maxAuditLineBytes) + `"}` + "\r\n" +
		strings.TrimSuffix(good, "\n") // a last line with no newline still counts

	sum, err := SummarizeAudit(strings.NewReader(log), AuditSummaryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Calls != 2 {
		t.Errorf("counted %d call(s), want the 2 real events", sum.Calls)
	}
	if sum.SkippedLines != 5 {
		t.Errorf("skipped %d line(s), want 5", sum.SkippedLines)
	}
}

func TestSummarizeAuditOfAnEmptyLog(t *testing.T) {
	sum, err := SummarizeAudit(strings.NewReader(""), AuditSummaryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Calls != 0 || sum.FailureRate != 0 || sum.First != "" || sum.CitedFiles != nil {
		t.Errorf("empty log summarised as %+v", sum)
	}
	// Arrays, not null: a script reading the JSON should not need a nil check.
	raw, _ := json.Marshal(sum)
	for _, key := range []string{`"tools":[]`, `"transports":[]`, `"days":[]`, `"slowest":[]`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("empty summary JSON lacks %s: %s", key, raw)
		}
	}
}

func TestSummarizeAuditHonoursTheTimeRange(t *testing.T) {
	var log strings.Builder
	for _, at := range []string{"2026-11-09T23:59:59Z", "2026-11-10T00:00:00Z", "2026-11-10T12:00:00+05:30", "2026-11-11T00:00:00Z"} {
		log.WriteString(auditLine(t, AuditEvent{Time: at, Transport: "cli", Tool: "callers", Outcome: "ok"}))
	}
	since := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	sum, err := SummarizeAudit(strings.NewReader(log.String()), AuditSummaryOptions{Since: since, Until: since.Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Calls != 2 || sum.OutsideRange != 2 {
		t.Errorf("calls=%d outside=%d, want 2 inside [since, until) and 2 outside", sum.Calls, sum.OutsideRange)
	}
	// A time written with an offset is counted on its UTC day.
	if len(sum.Days) != 1 || sum.Days[0].Name != "2026-11-10" {
		t.Errorf("days = %+v", sum.Days)
	}
}

// The arguments say what someone was working on. A summary is the thing people
// paste elsewhere, so it must carry none of them.
func TestAuditSummaryNeverCarriesArgumentsOrSessions(t *testing.T) {
	log := auditLine(t, AuditEvent{
		Time: "2026-11-12T09:00:00Z", Session: "sess-7f3a", Transport: "mcp", Tool: "context",
		Arguments: json.RawMessage(`{"task":"rotate the billing signing key"}`), DurationMS: 40, Outcome: "ok", ResultBytes: 900,
	})
	sum, err := SummarizeAudit(strings.NewReader(log), AuditSummaryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(sum)
	for _, private := range []string{"billing", "signing", "sess-7f3a", "arguments"} {
		if strings.Contains(string(raw), private) {
			t.Errorf("the summary contains %q: %s", private, raw)
		}
	}
}

// Keys come from the file, so every dimension is bounded however many
// distinct ones the file holds.
func TestSummarizeAuditBoundsEveryDimension(t *testing.T) {
	var log strings.Builder
	letters := func(i int) string {
		return string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+(i/676)%26))
	}
	const events = 2000
	for i := 0; i < events; i++ {
		log.WriteString(auditLine(t, AuditEvent{
			Time:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute).Format(time.RFC3339),
			Transport: "t" + letters(i),
			Tool:      "find_" + letters(i),
			Outcome:   "ok", DurationMS: int64(i),
		}))
	}
	// Names that are not tool names at all share one row.
	for _, bad := range []string{"DROP TABLE", "\x1b[31mred", strings.Repeat("z", 500)} {
		log.WriteString(auditLine(t, AuditEvent{Time: "2026-01-01T00:00:00Z", Transport: "UPPER case", Tool: bad, Outcome: "protocol_error"}))
	}

	sum, err := SummarizeAudit(strings.NewReader(log.String()), AuditSummaryOptions{Slowest: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Calls != events+3 {
		t.Fatalf("counted %d calls, want %d", sum.Calls, events+3)
	}
	// The bound, plus the two rows that collect what it turned away.
	if len(sum.Tools) > maxAuditTools+2 {
		t.Errorf("%d tool rows, over the bound of %d plus the two collecting rows", len(sum.Tools), maxAuditTools)
	}
	if len(sum.Transports) > maxAuditTransports+2 {
		t.Errorf("%d transport rows, over the bound of %d plus the collecting rows", len(sum.Transports), maxAuditTransports)
	}
	if other, ok := groupNamed(sum.Tools, auditOtherKey); !ok || other.Calls == 0 {
		t.Errorf("the tools past the bound were not collected under %q", auditOtherKey)
	}
	if len(sum.Slowest) != MaxSlowestCalls {
		t.Errorf("%d slowest calls listed, want the cap of %d", len(sum.Slowest), MaxSlowestCalls)
	}
	if sum.Slowest[0].DurationMS != events-1 {
		t.Errorf("slowest call is %d ms, want %d", sum.Slowest[0].DurationMS, events-1)
	}
	for i := 1; i < len(sum.Slowest); i++ {
		if sum.Slowest[i].DurationMS > sum.Slowest[i-1].DurationMS {
			t.Fatalf("the slowest list is out of order at %d", i)
		}
	}
	bad, ok := groupNamed(sum.Tools, auditInvalidTool)
	if !ok || bad.Calls != 3 {
		t.Errorf("malformed names = %+v, want 3 calls in one row", bad)
	}
	// Every call is in exactly one row of each dimension.
	for name, rows := range map[string][]AuditGroup{"tools": sum.Tools, "transports": sum.Transports, "days": sum.Days} {
		var total int64
		for _, r := range rows {
			total += r.Calls
		}
		if total != sum.Calls {
			t.Errorf("%s rows hold %d calls, the summary %d", name, total, sum.Calls)
		}
	}
	raw, _ := json.Marshal(sum)
	if strings.Contains(string(raw), "DROP TABLE") || strings.Contains(string(raw), `\u001b`) {
		t.Errorf("a malformed name reached the summary: %s", raw[:200])
	}
}

// A damaged line must not be able to subtract from the totals.
func TestSummarizeAuditIgnoresNegativeFigures(t *testing.T) {
	log := `{"time":"2026-11-12T09:00:00Z","transport":"cli","tool":"callers","outcome":"ok","result_bytes":-500,"duration_ms":-9,"cited_file_bytes":-1}` + "\n" +
		auditLine(t, AuditEvent{Time: "2026-11-12T09:00:01Z", Transport: "cli", Tool: "callers", Outcome: "ok", ResultBytes: 40, DurationMS: 3})
	sum, err := SummarizeAudit(strings.NewReader(log), AuditSummaryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.ResultBytes != 40 || sum.DurationMS != 3 || sum.CitedFiles != nil {
		t.Errorf("bytes=%d ms=%d cited=%+v, want 40, 3 and none", sum.ResultBytes, sum.DurationMS, sum.CitedFiles)
	}
}
