package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"
)

// ---------------------------------------------------------------- audit summary
//
// WHAT AN AUDIT LOG ADDS UP TO.
//
// stats answers for one live session and forgets it. The audit log
// is the record that outlives sessions, so the questions a person asks about a
// week of agent work ("which tools are used, how often do they fail, which
// calls are slow") are answered by reading it back.
//
// The summary is built from the numbers in the log and nothing else. It never
// reads the arguments: they say what someone was working on, and a summary a
// person pastes into a ticket should not carry that with it.
//
// A log grows for as long as it is left on, so it is read as a stream and kept
// as aggregates. Every dimension is bounded, including the ones whose keys come
// from the file: a log is a file on disk, and a file on disk can hold anything.

const (
	// maxAuditLineBytes bounds one line. A real event is a few kilobytes at
	// most (arguments are cut at auditArgBytes before they are written); a
	// longer line is not one, and is skipped without being held in memory.
	maxAuditLineBytes = 1 << 20

	// The most distinct keys kept per dimension. Past it, further keys are
	// counted together under auditOtherKey.
	maxAuditTools      = 256
	maxAuditTransports = 32
	// Ten years of days. A log covering more is summarised with --since.
	maxAuditDays = 3660

	// auditOtherKey collects the keys past a dimension's bound.
	auditOtherKey = "(other)"
	// auditInvalidTool collects events whose tool is not shaped like a tool
	// name. A call to a name the server never registered is logged as sent,
	// so a log can hold anything a client typed. A mistyped name that is
	// still snake case (get_callers) is kept as itself: which wrong names
	// agents reach for is worth seeing.
	auditInvalidTool = "(not a tool name)"

	// DefaultSlowestCalls is how many of the slowest calls a summary lists.
	DefaultSlowestCalls = 10
	// MaxSlowestCalls bounds that list.
	MaxSlowestCalls = 100
)

// AuditSummaryOptions narrows and shapes a summary.
type AuditSummaryOptions struct {
	// Since and Until bound the events counted, by their own timestamps:
	// Since is inclusive, Until exclusive. A zero value leaves that side open.
	Since, Until time.Time
	// Slowest is how many of the slowest calls to list. Zero means
	// DefaultSlowestCalls; it is capped at MaxSlowestCalls.
	Slowest int
}

// AuditSummary is an audit log reduced to totals.
type AuditSummary struct {
	Calls    int64 `json:"calls"`
	Failures int64 `json:"failures"`
	// FailureRate is Failures / Calls, from 0 to 1. Zero when there are no
	// calls.
	FailureRate float64 `json:"failure_rate"`

	// First and Last are the earliest and latest counted call (RFC 3339, UTC).
	First string `json:"first,omitempty"`
	Last  string `json:"last,omitempty"`

	ResultBytes           int64 `json:"result_bytes"`
	EstimatedResultTokens int64 `json:"estimated_result_tokens"`
	StructuredBytes       int64 `json:"structured_bytes"`
	// DurationMS sums the whole milliseconds the log records per call, so a
	// call faster than a millisecond adds nothing to it.
	DurationMS int64 `json:"duration_ms"`

	Tools      []AuditGroup `json:"tools"`
	Transports []AuditGroup `json:"transports"`
	// Days are UTC calendar days, oldest first.
	Days    []AuditGroup `json:"days"`
	Slowest []AuditCall  `json:"slowest"`

	// CitedFiles sums the cited-file figures the log carries. See CitedFiles
	// for what it is and is not; summed across sessions, a file named in two
	// sessions is counted in both.
	CitedFiles *CitedFiles `json:"cited_files,omitempty"`

	// SkippedLines are lines that were not audit events. OutsideRange are
	// events excluded by Since or Until.
	SkippedLines int64 `json:"skipped_lines"`
	OutsideRange int64 `json:"outside_range"`

	TokenBasis    Basis  `json:"token_basis"`
	TokenEstimate string `json:"token_estimate"`
}

// AuditGroup is the calls sharing one tool, one transport or one day.
type AuditGroup struct {
	Name                  string  `json:"name"`
	Calls                 int64   `json:"calls"`
	ToolErrors            int64   `json:"tool_errors"`
	ProtocolErrors        int64   `json:"protocol_errors"`
	FailureRate           float64 `json:"failure_rate"`
	ResultBytes           int64   `json:"result_bytes"`
	EstimatedResultTokens int64   `json:"estimated_result_tokens"`
	DurationMS            int64   `json:"duration_ms"`
	SlowestMS             int64   `json:"slowest_ms"`
	CitedFiles            int64   `json:"cited_files,omitempty"`
	CitedFileBytes        int64   `json:"cited_file_bytes,omitempty"`
}

// AuditCall is one call in the slowest list. It carries no arguments.
type AuditCall struct {
	Time        string `json:"time"`
	Tool        string `json:"tool"`
	Transport   string `json:"transport"`
	Outcome     string `json:"outcome"`
	DurationMS  int64  `json:"duration_ms"`
	ResultBytes int64  `json:"result_bytes"`
}

// auditGroups aggregates one dimension under a bound on its distinct keys.
type auditGroups struct {
	max   int
	byKey map[string]*AuditGroup
}

func newAuditGroups(limit int) *auditGroups {
	return &auditGroups{max: limit, byKey: make(map[string]*AuditGroup)}
}

func (g *auditGroups) add(key string, ev *AuditEvent, tokens int64) {
	row, ok := g.byKey[key]
	if !ok {
		// The two collecting rows are fixed keys, so they do not count
		// against the bound they exist to enforce.
		if len(g.byKey) >= g.max && key != auditInvalidTool && key != auditOtherKey {
			key = auditOtherKey
			row = g.byKey[key]
		}
		if row == nil {
			row = &AuditGroup{Name: key}
			g.byKey[key] = row
		}
	}
	row.Calls++
	switch ev.Outcome {
	case outcomeToolError:
		row.ToolErrors++
	case outcomeProtocolError:
		row.ProtocolErrors++
	}
	row.ResultBytes += ev.ResultBytes
	row.EstimatedResultTokens += tokens
	row.DurationMS += ev.DurationMS
	row.SlowestMS = max(row.SlowestMS, ev.DurationMS)
	row.CitedFiles += ev.CitedFiles
	row.CitedFileBytes += ev.CitedFileBytes
}

// rows returns the groups ordered by less, with failure rates filled in.
func (g *auditGroups) rows(less func(a, b AuditGroup) bool) []AuditGroup {
	out := make([]AuditGroup, 0, len(g.byKey))
	for _, row := range g.byKey {
		r := *row
		r.FailureRate = rate(r.ToolErrors+r.ProtocolErrors, r.Calls)
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

func rate(part, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole)
}

func byCallsThenName(a, b AuditGroup) bool {
	if a.Calls != b.Calls {
		return a.Calls > b.Calls
	}
	return a.Name < b.Name
}

func byName(a, b AuditGroup) bool { return a.Name < b.Name }

// plausibleToolName reports whether s is shaped like a tool name: short lower
// snake case. Looser than ValidToolName on purpose, which also demands a known
// verb; this only has to keep control characters and essays out of a table.
func plausibleToolName(s string) bool {
	if s == "" || len(s) > 64 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

// validTransport reports whether s is a front-end name as the server writes
// them: a short lower-case word.
func validTransport(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// SummarizeAudit reads an audit log and reduces it to totals per tool, per
// transport and per day, the slowest calls, and the failure rate.
//
// Lines that are not audit events are counted and skipped, never fatal: a log
// that was cut off mid-write, or that someone appended a note to, still has
// every other line in it.
func SummarizeAudit(r io.Reader, opts AuditSummaryOptions) (AuditSummary, error) {
	keep := opts.Slowest
	if keep <= 0 {
		keep = DefaultSlowestCalls
	}
	keep = min(keep, MaxSlowestCalls)

	sum := AuditSummary{TokenBasis: BasisTokenCount, TokenEstimate: EstimateMethod}
	tools := newAuditGroups(maxAuditTools)
	transports := newAuditGroups(maxAuditTransports)
	days := newAuditGroups(maxAuditDays)
	var (
		first, last time.Time
		slowest     []AuditCall
		citedFiles  int64
		citedBytes  int64
	)

	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, tooLong, err := readAuditLine(br)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return AuditSummary{}, fmt.Errorf("read audit log: %w", err)
		}
		if tooLong {
			sum.SkippedLines++
			continue
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		ev, when, ok := parseAuditLine(line)
		if !ok {
			sum.SkippedLines++
			continue
		}
		if (!opts.Since.IsZero() && when.Before(opts.Since)) || (!opts.Until.IsZero() && !when.Before(opts.Until)) {
			sum.OutsideRange++
			continue
		}

		tokens := EstimateTokens(ev.ResultBytes)
		sum.Calls++
		if ev.Outcome != outcomeOK {
			sum.Failures++
		}
		sum.ResultBytes += ev.ResultBytes
		sum.EstimatedResultTokens += tokens
		sum.StructuredBytes += ev.StructuredBytes
		sum.DurationMS += ev.DurationMS
		citedFiles += ev.CitedFiles
		citedBytes += ev.CitedFileBytes
		if first.IsZero() || when.Before(first) {
			first = when
		}
		if when.After(last) {
			last = when
		}

		tools.add(ev.Tool, &ev, tokens)
		transports.add(ev.Transport, &ev, tokens)
		days.add(when.Format("2006-01-02"), &ev, tokens)
		slowest = keepSlowest(slowest, keep, AuditCall{
			Time:        when.Format(time.RFC3339),
			Tool:        ev.Tool,
			Transport:   ev.Transport,
			Outcome:     ev.Outcome,
			DurationMS:  ev.DurationMS,
			ResultBytes: ev.ResultBytes,
		})
	}

	sum.FailureRate = rate(sum.Failures, sum.Calls)
	if !first.IsZero() {
		sum.First, sum.Last = first.Format(time.RFC3339), last.Format(time.RFC3339)
	}
	sum.Tools = tools.rows(byCallsThenName)
	sum.Transports = transports.rows(byCallsThenName)
	sum.Days = days.rows(byName)
	sum.Slowest = slowest
	if sum.Slowest == nil {
		sum.Slowest = []AuditCall{}
	}
	if citedFiles > 0 {
		sum.CitedFiles = &CitedFiles{
			Files:           citedFiles,
			Bytes:           citedBytes,
			EstimatedTokens: EstimateTokens(citedBytes),
			Meaning:         citedFilesSummedMeaning,
		}
	}
	return sum, nil
}

// parseAuditLine decodes one line and normalises what will be used as a key.
// ok is false for anything that is not an audit event this build understands.
func parseAuditLine(line []byte) (AuditEvent, time.Time, bool) {
	var ev AuditEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return ev, time.Time{}, false
	}
	when, err := time.Parse(time.RFC3339, ev.Time)
	if err != nil || ev.Tool == "" {
		return ev, time.Time{}, false
	}
	switch ev.Outcome {
	case outcomeOK, outcomeToolError, outcomeProtocolError:
	default:
		return ev, time.Time{}, false
	}
	if !plausibleToolName(ev.Tool) {
		ev.Tool = auditInvalidTool
	}
	if !validTransport(ev.Transport) {
		ev.Transport = auditOtherKey
	}
	// Sizes and durations are added up; a negative one is a damaged line and
	// must not be able to subtract from the totals.
	ev.ResultBytes = max(ev.ResultBytes, 0)
	ev.StructuredBytes = max(ev.StructuredBytes, 0)
	ev.DurationMS = max(ev.DurationMS, 0)
	ev.CitedFiles = max(ev.CitedFiles, 0)
	ev.CitedFileBytes = max(ev.CitedFileBytes, 0)
	return ev, when.UTC(), true
}

// keepSlowest inserts c into a list held slowest-first and at most keep long.
// Among equally slow calls the earlier one is listed first.
func keepSlowest(list []AuditCall, keep int, c AuditCall) []AuditCall {
	at := sort.Search(len(list), func(i int) bool {
		if list[i].DurationMS != c.DurationMS {
			return list[i].DurationMS < c.DurationMS
		}
		return list[i].Time > c.Time
	})
	if at >= keep {
		return list
	}
	list = append(list, AuditCall{})
	copy(list[at+1:], list[at:])
	list[at] = c
	if len(list) > keep {
		list = list[:keep]
	}
	return list
}

// readAuditLine reads one line of any length while holding at most
// maxAuditLineBytes of it. tooLong reports a line that was over the bound and
// has been discarded. The error is io.EOF once there is nothing left to read.
func readAuditLine(br *bufio.Reader) (line []byte, tooLong bool, err error) {
	read := false
	for {
		chunk, isPrefix, rerr := br.ReadLine()
		if rerr != nil {
			if errors.Is(rerr, io.EOF) && read {
				return line, tooLong, nil
			}
			return nil, false, rerr
		}
		read = true
		if !tooLong {
			if len(line)+len(chunk) > maxAuditLineBytes {
				tooLong, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		if !isPrefix {
			return line, tooLong, nil
		}
	}
}
