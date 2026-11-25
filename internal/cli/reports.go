package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	devmcp "github.com/sutrakam/graphlume/internal/mcp"
)

// reportRunner answers the report tools (policy, arch, deps, vulns, sbom, tour,
// adr, lessons, events) by running the command of the same name in this process.
//
// It exists so the tools and the commands cannot disagree: the tool does not
// reimplement a report, it runs the command and hands back what it printed. It
// is not a shell. It builds a fresh command tree, which holds no state from an
// earlier call, and refuses any command that is not on the list below; the
// words after the command come from the tool, which fixes the flags it passes.
type reportRunner struct {
	root string
	// slots bounds how many reports run at once: each opens the index and may
	// walk a diff, and a screen that asks for six at load must not start six.
	slots chan struct{}
}

// reportCommands are the commands a report may run, each with the subcommand
// that makes it read-only. `lesson add`, `policy explain` and everything else
// are absent. `licenses` is read-only only with --json (without it, it writes
// THIRD_PARTY.md and runs the Go toolchain), so allowedReport admits exactly
// `licenses --json` and nothing else of it.
var reportCommands = map[string][]string{
	"policy":   {"check"},
	"arch":     {"", "adr"},
	"deps":     {""},
	"vulns":    {""},
	"sbom":     {""},
	"tour":     {""},
	"lesson":   {"report"},
	"events":   {""},
	"licenses": {""},
}

// maxReportBytes bounds what one command may print into memory.
const maxReportBytes = 16 << 20

const reportSlots = 2

func newReportRunner(root string) *reportRunner {
	return &reportRunner{root: root, slots: make(chan struct{}, reportSlots)}
}

var _ devmcp.ReportRunner = (*reportRunner)(nil)

// Run implements mcp.ReportRunner.
func (r *reportRunner) Run(ctx context.Context, args ...string) (devmcp.ReportOutput, error) {
	if err := allowedReport(args); err != nil {
		return devmcp.ReportOutput{}, err
	}
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	case <-ctx.Done():
		return devmcp.ReportOutput{}, ctx.Err()
	}

	var out, errOut cappedBuffer
	out.max, errOut.max = maxReportBytes, 64<<10
	root := NewRoot()
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"--project", r.root, "--quiet"}, args...))

	err := root.ExecuteContext(ctx)
	res := devmcp.ReportOutput{Stdout: out.buf.Bytes(), Truncated: out.over}
	if err == nil {
		return res, nil
	}
	var ec *ExitCodeError
	if errors.As(err, &ec) && ec.Silent {
		// The command printed its report and exited with a verdict.
		res.ExitCode = ec.Code
		return res, nil
	}
	return devmcp.ReportOutput{}, errors.New(strings.TrimSpace(err.Error()))
}

// allowedReport checks the command words against reportCommands.
func allowedReport(args []string) error {
	if len(args) == 0 {
		return errors.New("no report command given")
	}
	subs, ok := reportCommands[args[0]]
	if !ok {
		return fmt.Errorf("%q is not a report command", args[0])
	}
	if args[0] == "licenses" && (len(args) != 2 || args[1] != "--json") {
		return errors.New(`"licenses" is a report command only as "licenses --json"`)
	}
	sub := ""
	if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		sub = args[1]
	}
	for _, s := range subs {
		if s == sub {
			return nil
		}
	}
	return fmt.Errorf("%q is not a report command", strings.TrimSpace(args[0]+" "+sub))
}

// cappedBuffer keeps the first max bytes written and discards the rest, so a
// runaway command cannot fill memory. Writes never fail: the command is not
// told its output was cut, and over says so afterwards.
type cappedBuffer struct {
	buf  bytes.Buffer
	max  int
	over bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	room := c.max - c.buf.Len()
	if room < len(p) {
		c.over = true
		p = p[:max(room, 0)]
	}
	c.buf.Write(p)
	return n, nil
}
