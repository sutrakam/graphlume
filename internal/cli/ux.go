package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
)

// Terminal & ANSI UX helpers for Homebrew-grade CLI presentation.

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiDim    = "\033[2m"
	ansiCyan   = "\033[36m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
	ansiClear  = "\033[2K\r"
)

// colorSupported reports whether w is an interactive terminal that accepts ANSI escapes.
func colorSupported(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	if f, ok := w.(*os.File); ok {
		return term.IsTerminal(int(f.Fd()))
	}
	return false
}

// UI provides styled terminal output.
type UI struct {
	w     io.Writer
	color bool
}

// NewUI creates a UI writer targeting w.
func NewUI(w io.Writer) *UI {
	return &UI{
		w:     w,
		color: colorSupported(w),
	}
}

// Header prints a Homebrew-style stage header: ==> Title
func (u *UI) Header(title string) {
	if u.color {
		_, _ = fmt.Fprintf(u.w, "%s==>%s %s%s%s\n", ansiCyan, ansiReset, ansiBold, title, ansiReset)
	} else {
		_, _ = fmt.Fprintf(u.w, "==> %s\n", title)
	}
}

// Success prints a green checkmark item: ✔ Text
func (u *UI) Success(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if u.color {
		_, _ = fmt.Fprintf(u.w, "%s✔%s %s\n", ansiGreen, ansiReset, msg)
	} else {
		_, _ = fmt.Fprintf(u.w, "✔ %s\n", msg)
	}
}

// Info prints a cyan info item: ℹ Text
func (u *UI) Info(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if u.color {
		_, _ = fmt.Fprintf(u.w, "%sℹ%s %s\n", ansiCyan, ansiReset, msg)
	} else {
		_, _ = fmt.Fprintf(u.w, "ℹ %s\n", msg)
	}
}

// Warn prints a yellow warning item: ▲ Text
func (u *UI) Warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if u.color {
		_, _ = fmt.Fprintf(u.w, "%s▲%s %s\n", ansiYellow, ansiReset, msg)
	} else {
		_, _ = fmt.Fprintf(u.w, "▲ %s\n", msg)
	}
}

// Dim wraps text in dim color if color is enabled.
func (u *UI) Dim(text string) string {
	if u.color {
		return ansiDim + text + ansiReset
	}
	return text
}

// Bold wraps text in bold if color is enabled.
func (u *UI) Bold(text string) string {
	if u.color {
		return ansiBold + text + ansiReset
	}
	return text
}

// progressBar renders an in-place dynamic progress indicator on interactive terminals.
type progressBar struct {
	w          io.Writer
	label      string
	disabled   bool
	isTerminal bool
	mu         sync.Mutex
	lastUpdate time.Time
	lastPct    int64
}

func newProgressBar(w io.Writer, label string, quiet bool) *progressBar {
	p := &progressBar{
		w:          w,
		label:      label,
		disabled:   quiet,
		isTerminal: colorSupported(w),
		lastPct:    -1,
	}
	return p
}

// Update renders progress for done/total files.
func (p *progressBar) Update(done, total int64) {
	if p.disabled || total <= 0 {
		return
	}

	pct := done * 100 / total
	now := time.Now()

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.isTerminal {
		// Throttle updates to at most once every 50ms unless finished
		if now.Sub(p.lastUpdate) < 50*time.Millisecond && done < total {
			return
		}
		p.lastUpdate = now

		barWidth := 20
		filled := int(float64(pct) / 100.0 * float64(barWidth))
		if filled > barWidth {
			filled = barWidth
		}
		bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

		_, _ = fmt.Fprintf(p.w, "%s  [%s] %3d%% (%d/%d files)", ansiClear, bar, pct, done, total)
	} else {
		// Non-interactive (CI/file redirection): log milestones on 25% boundaries
		if pct/25 > p.lastPct/25 {
			p.lastPct = pct
			_, _ = fmt.Fprintf(p.w, "  %s: %d%% (%d/%d files)\n", p.label, pct, done, total)
		}
	}
}

// Finish clears the progress line and prints the final completion summary.
func (p *progressBar) Finish(finalMessage string) {
	if p.disabled {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()

	ui := NewUI(p.w)
	if p.isTerminal {
		_, _ = fmt.Fprint(p.w, ansiClear)
	}
	ui.Success("%s", finalMessage)
}

// printInstallSummary provides a compact, enterprise-grade summary of agent installations.
func printInstallSummary(w io.Writer, results []installResult) {
	ui := NewUI(w)
	var active []string
	configuredCount := 0

	for _, r := range results {
		if r.Action == "updated" || r.Action == "created" || r.Action == "unchanged" {
			configuredCount++
			if len(active) < 5 {
				active = append(active, r.Client)
			}
		}
	}

	if configuredCount == 0 {
		return
	}

	sample := strings.Join(active, ", ")
	if configuredCount > len(active) {
		sample += fmt.Sprintf(", +%d more", configuredCount-len(active))
	}

	ui.Success("Configured %d coding agent integrations (%s)", configuredCount, sample)
	_, _ = fmt.Fprintf(w, "  %s\n", ui.Dim("(run with --verbose to view all configuration file paths)"))
}

// NextSteps prints an enterprise-grade next steps guide.
func (u *UI) NextSteps(project string) {
	_, _ = fmt.Fprintln(u.w)
	u.Header("Next Steps & Common Commands")
	_, _ = fmt.Fprintf(u.w, "  1. %-24s %s\n", u.Bold("graphlume ui"), "Open interactive local web portal in your browser (http://127.0.0.1:7777)")
	_, _ = fmt.Fprintf(u.w, "  2. %-24s %s\n", u.Bold("AI Coding Agent"), "Restart Cursor/Claude/VS Code and ask: "+u.Dim("\"What is the blast radius of changing <symbol>?\""))
	_, _ = fmt.Fprintf(u.w, "  3. %-24s %s\n", u.Bold("graphlume status"), "Inspect index health, entity counts, and compiler vs heuristic breakdown")
	_, _ = fmt.Fprintf(u.w, "  4. %-24s %s\n", u.Bold("graphlume blindspots"), "Detect unindexed files, budget timeouts, and code intelligence blind spots")
	_, _ = fmt.Fprintf(u.w, "  5. %-24s %s\n", u.Bold("graphlume bundle"), "Export sanitized diagnostic report (to attach to GitHub issue or email)")
	_, _ = fmt.Fprintln(u.w)
}
