package multirepo

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// RenderContracts writes a contract report as plain text.
//
// The section order follows [Render] and for the same reason: members and what
// was not read come first, because a finding that says "no route matches" is
// worth exactly as much as the list of routes it was compared against, and a
// reader has to see that list's holes before the finding. The observation that
// a route has no indexed caller comes last and is headed as informational, so
// it cannot be mistaken for one of the findings above it.
func RenderContracts(w io.Writer, r *ContractReport) error {
	var b strings.Builder

	renderContractMembers(&b, r)
	renderContractProvenance(&b, r)
	renderContractSummary(&b, r)
	for _, kind := range contractKindOrder() {
		renderContractFindings(&b, r, kind)
	}
	renderNotCompared(&b, r)
	renderUncalled(&b, r)
	renderContractCaveats(&b, r)

	_, err := io.WriteString(w, b.String())
	return err
}

func renderContractMembers(b *strings.Builder, r *ContractReport) {
	available := 0
	for _, m := range r.Members {
		if m.Available {
			available++
		}
	}
	fmt.Fprintf(b, "CONTRACTS: %d member repositor%s, %d readable\n",
		len(r.Members), plural(len(r.Members), "y", "ies"), available)

	w := tabwriter.NewWriter(b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  MEMBER\tREVISION\tINDEXED\tROUTES\tCALLS\tCALLS READ IN\tCALLS NOT READ IN")
	for _, m := range r.Members {
		if !m.Available {
			fmt.Fprintf(w, "  %s\t—\t—\t—\t—\t—\t—\n", m.Name)
			continue
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%d\t%d\t%s\t%s\n",
			m.Name, orNone(shortRev(m.GitRevision)), age(m.AgeHours),
			m.Endpoints, m.OutboundCalls,
			orNone(languageNames(m.CallsReadIn)), orNone(languageNames(m.CallsNotReadIn)))
	}
	_ = w.Flush()

	for _, m := range r.Members {
		if m.Available {
			continue
		}
		fmt.Fprintf(b, "\n  ! %s WAS NOT READ\n", strings.ToUpper(m.Name))
		fmt.Fprintf(b, "%s\n\n", wrapIndented("      ", "      ", m.Unavailable))
		fmt.Fprintf(b, "%s\n", wrapIndented("      ", "      ",
			"Its routes and its calls are absent from everything below: a call into it "+
				"reads as a call with no matching route, and nothing it calls was checked."))
	}
	for _, m := range r.Members {
		if !m.Available || len(m.Warnings) == 0 {
			continue
		}
		fmt.Fprintf(b, "\n  %s (%s)\n", m.Name, orNone(m.IndexPath))
		for _, warn := range m.Warnings {
			fmt.Fprintf(b, "%s\n", wrapIndented("    ! ", "      ", warn))
		}
	}

	if len(r.Weakeners) > 0 {
		b.WriteString("\n  EVERY FINDING BELOW IS ONE TIER WEAKER, because the route side is incomplete\n" +
			"  (a finding already at `low` stays there):\n")
		for _, reason := range r.Weakeners {
			fmt.Fprintf(b, "%s\n", wrapIndented("    ! ", "      ", reason))
		}
	}
}

func renderContractProvenance(b *strings.Builder, r *ContractReport) {
	if len(r.Provenance.Notes) == 0 {
		return
	}
	b.WriteString("\nPROVENANCE\n")
	for _, n := range r.Provenance.Notes {
		fmt.Fprintf(b, "%s\n", wrapIndented("  ! ", "    ", n))
	}
}

func renderContractSummary(b *strings.Builder, r *ContractReport) {
	s := r.Summary
	fmt.Fprintf(b, "\nCALLS EXAMINED: %d\n", s.CallsExamined)
	w := tabwriter.NewWriter(b, 0, 0, 2, ' ', 0)
	// The count is the last cell of every row, so no row ends in padding.
	fmt.Fprintf(w, "  method and path agree with a declared route\t%d%s\n", s.Matched, matchedNote(s))
	fmt.Fprintf(w, "  literal prefix of a run-time target starts a route\t%d\n", s.MatchedByPrefix)
	fmt.Fprintf(w, "  path matches a route, method does not\t%d\n", s.MethodMismatch)
	fmt.Fprintf(w, "  path-variable count differs from the nearest route\t%d\n", s.PathVariableMismatch)
	fmt.Fprintf(w, "  no route in any member\t%d\n", s.NoMatchingRoute)
	fmt.Fprintf(w, "  agree only under a base path nothing indexed states\t%d\n", s.BasePathUnverified)
	fmt.Fprintf(w, "  not compared\t%d\n", s.NotCompared)
	_ = w.Flush()
	if s.CallsInTestFiles > 0 {
		fmt.Fprintf(b, "%s\n", wrapIndented("  ", "  ", fmt.Sprintf(
			"%d further call%s in test files %s set aside: a test calling a server it "+
				"started is not a client of this system.",
			s.CallsInTestFiles, plural(s.CallsInTestFiles, "", "s"),
			plural(s.CallsInTestFiles, "was", "were"))))
	}
	fmt.Fprintf(b, "ROUTES EXAMINED: %d, of which no indexed call reaches %d\n",
		s.RoutesExamined, s.RoutesUncalled)
}

func matchedNote(s ContractSummary) string {
	var parts []string
	if s.AnsweredInOwnMember > 0 {
		parts = append(parts, fmt.Sprintf("%d inside the calling member", s.AnsweredInOwnMember))
	}
	if s.MatchedSeveralRoutes > 0 {
		parts = append(parts, fmt.Sprintf("%d by more than one route", s.MatchedSeveralRoutes))
	}
	if len(parts) == 0 {
		return ""
	}
	return "  (" + strings.Join(parts, ", ") + ")"
}

// contractHeading is the section title for each kind of finding.
func contractHeading(k ContractKind) string {
	switch k {
	case ContractMethodMismatch:
		return "METHOD MISMATCH"
	case ContractPathVariables:
		return "PATH-VARIABLE MISMATCH"
	case ContractNoRoute:
		return "NO MATCHING ROUTE"
	case ContractBasePath:
		return "BASE PATH UNVERIFIED"
	default:
		return strings.ToUpper(string(k))
	}
}

func contractTotal(s ContractSummary, k ContractKind) int {
	switch k {
	case ContractMethodMismatch:
		return s.MethodMismatch
	case ContractPathVariables:
		return s.PathVariableMismatch
	case ContractNoRoute:
		return s.NoMatchingRoute
	case ContractBasePath:
		return s.BasePathUnverified
	default:
		return 0
	}
}

func renderContractFindings(b *strings.Builder, r *ContractReport, kind ContractKind) {
	total := contractTotal(r.Summary, kind)
	fmt.Fprintf(b, "\n%s (%d)\n", contractHeading(kind), total)
	if total == 0 {
		// Never omitted: a section that vanishes when it is empty cannot be
		// told from a section the command does not have.
		b.WriteString("  none found — which is a statement about the calls and routes that were indexed\n")
		return
	}
	for _, fd := range r.Findings {
		if fd.Kind != kind {
			continue
		}
		weak := ""
		if fd.Weakened {
			weak = "  (one tier weaker: see above)"
		}
		fmt.Fprintf(b, "\n  [%s] %s  %s%s\n", fd.Confidence, fd.Call.Member,
			callTarget(fd.Call.Method, fd.Call.Path, fd.Call.Dynamic), weak)
		fmt.Fprintf(b, "    call   %s:%d%s%s\n", fd.Call.File, fd.Call.Line,
			callerNote(fd.Call.Caller), clientNote(fd.Call.Client))
		for _, rt := range fd.Routes {
			fmt.Fprintf(b, "    route  %s  %s  %s:%d%s\n",
				rt.Member, rt.Endpoint, rt.File, rt.Line, handlerNote(rt))
		}
		if fd.MoreRoutes > 0 {
			fmt.Fprintf(b, "    route  … and %d more\n", fd.MoreRoutes)
		}
		fmt.Fprintf(b, "%s\n", wrapIndented("    ", "    ", fd.Detail+"."))
	}
	renderTruncated(b, r, string(kind))
}

func renderNotCompared(b *strings.Builder, r *ContractReport) {
	fmt.Fprintf(b, "\nNOT COMPARED (%d)\n", r.Summary.NotCompared)
	if r.Summary.NotCompared == 0 {
		b.WriteString("  every indexed call had a path that could be compared\n")
		return
	}
	for _, u := range r.NotCompared {
		fmt.Fprintf(b, "\n  %s  %s\n", u.Call.Member, callTarget(u.Call.Method, u.Call.Path, u.Call.Dynamic))
		fmt.Fprintf(b, "    call   %s:%d%s%s\n", u.Call.File, u.Call.Line,
			callerNote(u.Call.Caller), clientNote(u.Call.Client))
		fmt.Fprintf(b, "%s\n", wrapIndented("    ", "    ", u.Reason+"."))
	}
	renderTruncated(b, r, "not_compared")
}

func renderUncalled(b *strings.Builder, r *ContractReport) {
	fmt.Fprintf(b, "\nROUTES NO INDEXED CALL REACHES (%d) — informational, the weakest section\n",
		r.Summary.RoutesUncalled)
	if blind := membersWithUnreadCalls(r); len(blind) > 0 {
		fmt.Fprintf(b, "%s\n", wrapIndented("  ! ", "    ",
			"Calls made from "+joinAnd(blind)+" are partly or wholly unread (see the member "+
				"table), so a route listed here may well be called from there."))
	}
	if n := r.Summary.NotCompared; n > 0 && r.Summary.RoutesUncalled > 0 {
		fmt.Fprintf(b, "%s\n", wrapIndented("  ! ", "    ", fmt.Sprintf(
			"%d call%s could not be compared at all (see NOT COMPARED) and may reach any "+
				"route listed here.", n, plural(n, "", "s"))))
	}
	for _, rt := range r.UncalledRoutes {
		fmt.Fprintf(b, "  [%s] %s  %s  %s:%d%s\n", rt.Confidence, rt.Member, rt.Endpoint,
			rt.File, rt.Line, handlerNote(rt.ContractRoute))
	}
	renderTruncated(b, r, "uncalled_routes")
}

func membersWithUnreadCalls(r *ContractReport) []string {
	var out []string
	for _, m := range r.Members {
		if !m.Available || len(m.CallsNotReadIn) > 0 {
			out = append(out, m.Name)
		}
	}
	return out
}

func renderTruncated(b *strings.Builder, r *ContractReport, section string) {
	if n := r.Truncated[section]; n > 0 {
		fmt.Fprintf(b, "  ! %d more not shown (raise --limit)\n", n)
	}
}

func renderContractCaveats(b *strings.Builder, r *ContractReport) {
	b.WriteString("\nWHAT THIS IS NOT\n")
	for _, c := range r.Caveats {
		fmt.Fprintf(b, "%s\n", wrapIndented("  ", "  ", c))
	}
	if !r.Complete() {
		fmt.Fprintf(b, "%s\n", wrapIndented("  ", "  ",
			"This report is INCOMPLETE: "+strings.Join(r.UnavailableMembers, ", ")+
				" could not be read, so every count above is a floor."))
	}
}

func languageNames(ls []LanguageFiles) string {
	names := make([]string, 0, len(ls))
	for _, l := range ls {
		names = append(names, l.Language)
	}
	return strings.Join(names, ", ")
}

func callerNote(caller string) string {
	if caller == "" {
		return ""
	}
	return "  in " + caller
}

func handlerNote(rt ContractRoute) string {
	if rt.Handler == "" {
		return ""
	}
	return "  handler " + rt.Handler
}
