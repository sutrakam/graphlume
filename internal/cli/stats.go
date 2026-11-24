package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	devmcp "github.com/sutrakam/graphlume/internal/mcp"

	"github.com/spf13/cobra"
)

// newStatsCmd summarises an audit log: what the agent tools were asked, how
// large the answers were and how often they failed.
//
// It is the person's view of the same accounting stats gives an
// agent for one live session. It needs no index and opens nothing but the file
// it is given, so it runs anywhere the log can be read.
func newStatsCmd(g *Global) *cobra.Command {
	var (
		since   string
		until   string
		slowest int
	)
	cmd := &cobra.Command{
		Use:   "stats [audit-log]",
		Short: "Summarise an audit log: calls, answer sizes, time and failures per tool",
		Long: `Summarises the calls recorded in an audit log: per tool, per transport and per
day, the slowest calls, and the failure rate.

  graphlume stats .graphlume/audit.jsonl
  graphlume stats --since 2026-11-01 --slowest 20
  graphlume stats --json audit.jsonl
  tail -n 500 audit.jsonl | graphlume stats -

With no file it reads the one named by $` + auditLogEnv + `. Record a log with
'graphlume serve --audit-log FILE', 'graphlume tool call ... --audit-log FILE',
or by setting that variable.

This reports what was used. It does not report a saving, because GraphLume
has no measurement that supports one.

EST. TOKENS is each answer's size in bytes divided by four, rounded up. It is
an estimate: no tokenizer is run and the true count depends on the model.

FILES THE ANSWERS NAMED, when the log carries it, is the size of the indexed
files the answers pointed at. It is an upper bound on what reading those files
whole would have cost, not a saving: an agent with search would not necessarily
have read them, and could have read parts. It is given per transport because
the counting differs: an MCP session counts a file once however many answers
name it, while each 'graphlume tool call' is a session of its own and counts
it again.

Only numbers, tool names and transports are printed. The arguments in the log,
which say what someone was working on, are never read into the summary. Days
are UTC. --since and --until take a date (2026-11-01) or an RFC 3339 time;
--since is inclusive and --until exclusive.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := devmcp.AuditSummaryOptions{Slowest: slowest}
			if slowest < 0 {
				return errors.New("--slowest must not be negative")
			}
			var err error
			if opts.Since, err = parseStatsTime("--since", since); err != nil {
				return err
			}
			if opts.Until, err = parseStatsTime("--until", until); err != nil {
				return err
			}
			if !opts.Since.IsZero() && !opts.Until.IsZero() && !opts.Until.After(opts.Since) {
				return errors.New("--until must be later than --since")
			}

			path := ""
			if len(args) == 1 {
				path = args[0]
			} else {
				path = strings.TrimSpace(os.Getenv(auditLogEnv))
			}
			if path == "" {
				return fmt.Errorf("no audit log named: pass a file, or set %s. "+
					"Record one with 'graphlume serve --audit-log FILE' or "+
					"'graphlume tool call <tool> --audit-log FILE'", auditLogEnv)
			}

			var in io.Reader
			if path == "-" {
				in, path = cmd.InOrStdin(), "standard input"
			} else {
				f, err := os.Open(path) //nolint:gosec // G304: a path the user typed on their own command line
				if err != nil {
					return fmt.Errorf("open audit log: %w", err)
				}
				defer func() { _ = f.Close() }()
				in = f
			}

			sum, err := devmcp.SummarizeAudit(in, opts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if g.JSON {
				return emitJSON(out, struct {
					File string `json:"file"`
					devmcp.AuditSummary
				}{File: path, AuditSummary: sum})
			}
			renderAuditSummary(out, path, sum)
			return nil
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "count only calls at or after this date or RFC 3339 time")
	cmd.Flags().StringVar(&until, "until", "", "count only calls before this date or RFC 3339 time")
	cmd.Flags().IntVar(&slowest, "slowest", devmcp.DefaultSlowestCalls,
		fmt.Sprintf("how many of the slowest calls to list (at most %d)", devmcp.MaxSlowestCalls))
	return cmd
}

// parseStatsTime reads a date or an RFC 3339 time. A bare date is midnight UTC,
// matching the UTC days the summary groups by.
func parseStatsTime(flag, value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("%s %q: want a date such as 2026-11-01 or an RFC 3339 time such as 2026-11-01T09:00:00Z", flag, value)
}

func renderAuditSummary(out io.Writer, path string, sum devmcp.AuditSummary) {
	fmt.Fprintf(out, "Audit log: %s\n", path)
	if sum.Calls == 0 {
		fmt.Fprintln(out, "No tool calls recorded.")
		renderAuditFootnotes(out, sum)
		return
	}

	fmt.Fprintf(out, "%s call(s) from %s to %s, %s failed (%s).\n",
		humanCount(sum.Calls), sum.First, sum.Last, humanCount(sum.Failures), percent(sum.FailureRate))
	fmt.Fprintf(out, "Answers: %s bytes of text, an estimated %s tokens. Time answering: %s ms.\n",
		humanCount(sum.ResultBytes), humanCount(sum.EstimatedResultTokens), humanCount(sum.DurationMS))

	renderAuditGroups(out, "BY TOOL", "TOOL", sum.Tools)
	renderAuditGroups(out, "BY TRANSPORT", "TRANSPORT", sum.Transports)
	renderAuditGroups(out, "BY DAY (UTC)", "DAY", sum.Days)

	if len(sum.Slowest) > 0 {
		fmt.Fprintln(out, "\nSLOWEST CALLS")
		tw := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
		fmt.Fprintln(tw, "TIME (UTC)\tTOOL\tTRANSPORT\tMS\tANSWER BYTES\tOUTCOME")
		for _, c := range sum.Slowest {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
				c.Time, c.Tool, c.Transport, humanCount(c.DurationMS), humanCount(c.ResultBytes), c.Outcome)
		}
		_ = tw.Flush()
	}

	if sum.CitedFiles != nil {
		fmt.Fprintln(out, "\nFILES THE ANSWERS NAMED")
		// One line per transport, because what "once" means differs: a
		// protocol session counts a file once however many answers name it,
		// while every shell call is a session of its own and counts it again.
		// A single total would add figures that are not the same kind.
		tw := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
		fmt.Fprintln(tw, "TRANSPORT\tFILES\tBYTES WHEN INDEXED\tEST. TOKENS\tA FILE IS COUNTED")
		for _, tr := range sum.Transports {
			if tr.CitedFiles == 0 {
				continue
			}
			counted := "once per session"
			if tr.Name == "cli" {
				counted = "once per call: each 'tool call' is its own session"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", tr.Name, humanCount(tr.CitedFiles),
				humanCount(tr.CitedFileBytes), humanCount(devmcp.EstimateTokens(tr.CitedFileBytes)), counted)
		}
		_ = tw.Flush()
		fmt.Fprintln(out, wrapText("This is the size of the indexed files the answers pointed at: an upper bound on "+
			"what reading those files whole would have cost. It is not a saving. An agent with search "+
			"would not necessarily have read them, and could have read parts.", 88))
	}
	renderAuditFootnotes(out, sum)
}

func renderAuditGroups(out io.Writer, title, column string, rows []devmcp.AuditGroup) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(out, "\n%s\n", title)
	tw := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	fmt.Fprintf(tw, "%s\tCALLS\tFAILED\tFAIL RATE\tANSWER BYTES\tEST. TOKENS\tTOTAL MS\tSLOWEST MS\n", column)
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Name, humanCount(r.Calls), humanCount(r.ToolErrors+r.ProtocolErrors), percent(r.FailureRate),
			humanCount(r.ResultBytes), humanCount(r.EstimatedResultTokens),
			humanCount(r.DurationMS), humanCount(r.SlowestMS))
	}
	_ = tw.Flush()
}

func renderAuditFootnotes(out io.Writer, sum devmcp.AuditSummary) {
	fmt.Fprintln(out)
	if sum.Calls > 0 {
		fmt.Fprintln(out, wrapText("Token figures are estimates: "+sum.TokenEstimate+". Times are the whole "+
			"milliseconds the log records, so a call faster than 1 ms counts as 0. This is usage; no "+
			"saving is reported because none has been measured.", 88))
	}
	if sum.OutsideRange > 0 {
		fmt.Fprintf(out, "%s call(s) fell outside --since/--until and were not counted.\n", humanCount(sum.OutsideRange))
	}
	if sum.SkippedLines > 0 {
		fmt.Fprintf(out, "%s line(s) were not audit events and were skipped.\n", humanCount(sum.SkippedLines))
	}
}

// percent renders a 0..1 rate with one decimal.
func percent(rate float64) string {
	return fmt.Sprintf("%.1f%%", 100*rate)
}
