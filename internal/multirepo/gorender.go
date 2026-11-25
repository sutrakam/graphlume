package multirepo

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
)

// renderGoModules is the Go-module section of the system report. Absent when
// no member defines a Go module, so a system without Go prints nothing about
// it; when present, every pair's counts are printed, the not-attempted ones
// included, because a link count with no denominator reads as a total.
func renderGoModules(b *strings.Builder, r *Report) {
	g := r.GoModules
	if g == nil {
		return
	}
	fns, methods := goLinkTotals(g)
	fmt.Fprintf(b, "\nGO MODULE LINKS (%d call%s linked: %d function, %d method)\n",
		fns+methods, plural(fns+methods, "", "s"), fns, methods)
	for _, m := range g.Modules {
		fmt.Fprintf(b, "  %s defines %s (%s)\n", m.Member, m.Module, m.Dir)
	}
	for _, n := range g.Notes {
		fmt.Fprintf(b, "%s\n", wrapIndented("  ! ", "    ", n))
	}
	if len(g.Pairs) == 0 {
		fmt.Fprintf(b, "%s\n", wrapIndented("  ", "  ",
			"No member calls into a Go module another member defines."))
	}
	for _, p := range g.Pairs {
		fmt.Fprintf(b, "\n  %s -> %s  %s\n", p.Importer, p.Provider, p.Module)
		fmt.Fprintf(b, "    function calls: linked %d, ambiguous %d, unmatched %d\n", p.Links, p.Ambiguous, p.Unmatched)
		fmt.Fprintf(b, "%s\n", wrapIndented("    ", "      ", fmt.Sprintf(
			"method calls: linked %d, ambiguous %d, not resolvable %d%s",
			p.MethodLinks, p.MethodAmbiguous, sumCounts(p.MethodNotResolvable), reasonBreakdown(p.MethodNotResolvable))))
		req := "not required by any go.mod"
		if len(p.RequiredVersions) > 0 {
			req = "requires " + strings.Join(p.RequiredVersions, ", ")
		}
		fmt.Fprintf(b, "    %s; %s indexed at %s\n", req, p.Provider, orNone(shortRev(p.ProviderRevision)))
		for _, w := range p.Warnings {
			fmt.Fprintf(b, "%s\n", wrapIndented("    ! ", "      ", w))
		}
	}

	// Function and method links are listed apart: a method link rests on one
	// more inference (the receiver's type) than a package-qualified one, and a
	// reader auditing the weaker kind should find them together.
	for _, kind := range []string{callKindFunction, callKindMethod} {
		heading := false
		for _, l := range g.Links {
			if l.CallKind != kind {
				continue
			}
			if !heading {
				fmt.Fprintf(b, "\n  %s calls linked:\n", kind)
				heading = true
			}
			fmt.Fprintf(b, "\n  [%s] %s -> %s\n", l.Confidence, l.FromMember, l.ToMember)
			fmt.Fprintf(b, "    %s  %s:%d\n", l.FromSymbol, l.FromFile, l.FromLine)
			fmt.Fprintf(b, "    calls %s  ->  %s (%s)  %s:%d\n", l.Call, l.ToSymbol, l.ToKind, l.ToFile, l.ToLine)
			for _, leg := range l.Legs {
				fmt.Fprintf(b, "      ~ %-26s %-6s %s\n", leg.Name, leg.Confidence, leg.Detail)
			}
			if l.Note != "" {
				fmt.Fprintf(b, "%s\n", wrapIndented("      ! ", "        ", l.Note))
			}
		}
	}

	if len(g.Ambiguous) > 0 {
		b.WriteString("\n  calls not linked, ambiguous:\n")
		for _, s := range g.Ambiguous {
			fmt.Fprintf(b, "    %s  %s  %s:%d\n", s.Member, s.Call, s.File, s.Line)
			if len(s.Candidates) == 0 && s.Reason != "" {
				fmt.Fprintf(b, "%s\n", wrapIndented("      ", "      ", s.Reason))
			}
			for _, c := range s.Candidates {
				fmt.Fprintf(b, "      could be %s: %s\n", s.Provider, c)
			}
		}
	}
	if len(g.Unmatched) > 0 {
		b.WriteString("\n  calls not linked, no such declaration:\n")
		for _, s := range g.Unmatched {
			fmt.Fprintf(b, "    %s  %s  %s:%d  — %s\n", s.Member, s.Call, s.File, s.Line, s.Reason)
		}
	}
	if len(g.MethodsNotResolved) > 0 {
		b.WriteString("\n  method calls not linked:\n")
		for _, s := range g.MethodsNotResolved {
			fmt.Fprintf(b, "    %s  %s  %s:%d  — %s\n", s.Member, s.Call, s.File, s.Line, s.Reason)
		}
	}
	keys := make([]string, 0, len(g.Truncated))
	for k := range g.Truncated {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "  ! %s: %d more not shown\n", k, g.Truncated[k])
	}
	if len(g.Caveats) > 0 {
		b.WriteString("\n")
	}
	for _, c := range g.Caveats {
		fmt.Fprintf(b, "%s\n", wrapIndented("  ~ ", "    ", c))
	}
}

// goLinkTotals are the calls linked over every pair: package-qualified, then
// method.
func goLinkTotals(g *GoModuleReport) (functions, methods int) {
	for _, p := range g.Pairs {
		functions += p.Links
		methods += p.MethodLinks
	}
	return functions, methods
}

func sumCounts(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// reasonBreakdown is " (2 reassigned, 1 no such method)", reasons in key
// order so two runs print the same line, or "" when nothing was counted.
func reasonBreakdown(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if v > 0 {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		label := reasonLabels[k]
		if label == "" {
			label = strings.ReplaceAll(k, "_", " ")
		}
		parts = append(parts, fmt.Sprintf("%d %s", m[k], label))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// RenderSystemCallers writes a system callers report as plain text.
func RenderSystemCallers(w io.Writer, r *SystemCallersReport) error {
	var b strings.Builder
	t := r.Target
	fmt.Fprintf(&b, "%s:%s (%s) at %s:%d\n\n", t.Member, t.Qualified, t.Kind, t.File, t.Line)
	if len(r.Callers) == 0 {
		b.WriteString("No callers found in any member.\n")
	} else {
		tw := tabwriter.NewWriter(&b, 0, 8, 2, ' ', 0)
		fmt.Fprintln(tw, "MEMBER\tCALLER\tEVIDENCE\tCONFIDENCE\tLEG")
		for _, c := range r.Callers {
			leg := c.Leg
			if c.Leg == LegLocal && c.Source != "" {
				leg += " (" + c.Source + ")"
			}
			if c.Call != "" {
				leg += " " + c.Call
			}
			fmt.Fprintf(tw, "%s\t%s\t%s:%d\t%s\t%s\n", c.Member, c.Caller, c.File, c.Line, c.Confidence, leg)
		}
		_ = tw.Flush()
		fmt.Fprintf(&b, "\n%d caller(s).\n", len(r.Callers))
	}
	if r.Truncated > 0 {
		fmt.Fprintf(&b, "! %d more not shown.%s\n", r.Truncated, suffixNote(r.TruncationNote))
	}
	renderTraceFooter(&b, r.Warnings, r.UnavailableMembers, r.Caveats)
	_, err := io.WriteString(w, b.String())
	return err
}

// RenderSystemImpact writes a system impact report as plain text.
func RenderSystemImpact(w io.Writer, r *SystemImpactReport) error {
	var b strings.Builder
	t := r.Target
	fmt.Fprintf(&b, "%s:%s (%s) at %s:%d\n", t.Member, t.Qualified, t.Kind, t.File, t.Line)
	floor := "none"
	if r.MinConfidence != "" {
		floor = string(r.MinConfidence)
	}
	fmt.Fprintf(&b, "depth %d, confidence floor %s, members reached: %s\n\n",
		r.MaxDepth, floor, strings.Join(r.Members, ", "))
	if len(r.Rows) == 0 {
		b.WriteString("No dependents found in any member.\n")
	} else {
		tw := tabwriter.NewWriter(&b, 0, 8, 2, ' ', 0)
		fmt.Fprintln(tw, "DEPTH\tMEMBER\tSYMBOL\tEVIDENCE\tCONFIDENCE\tVIA\tREACHES")
		for _, row := range r.Rows {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s:%d\t%s\t%s\t%s\n",
				row.Depth, row.Member, row.Symbol, row.File, row.Line, row.Confidence, row.Via, row.Reaches)
		}
		_ = tw.Flush()
		fmt.Fprintf(&b, "\n%d dependent(s) across %d member(s).\n", len(r.Rows), len(r.Members))
	}
	if r.TruncationNote != "" {
		fmt.Fprintf(&b, "%s\n", wrapIndented("! ", "  ", "Truncated: "+r.TruncationNote+"."))
	}
	renderTraceFooter(&b, r.Warnings, r.UnavailableMembers, r.Caveats)
	_, err := io.WriteString(w, b.String())
	return err
}

func renderTraceFooter(b *strings.Builder, warnings, unavailable, caveats []string) {
	for _, wn := range warnings {
		fmt.Fprintf(b, "%s\n", wrapIndented("! ", "  ", wn))
	}
	if len(unavailable) > 0 {
		fmt.Fprintf(b, "%s\n", wrapIndented("! ", "  ", fmt.Sprintf(
			"INCOMPLETE: %s could not be read, so nothing it calls appears above.",
			joinAnd(unavailable))))
	}
	b.WriteString("\n")
	for _, c := range caveats {
		fmt.Fprintf(b, "%s\n", wrapIndented("~ ", "  ", c))
	}
}

func suffixNote(s string) string {
	if s == "" {
		return ""
	}
	return " " + s + "."
}
