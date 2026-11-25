package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/internal/capability/impact"
	"github.com/sutrakam/graphlume/internal/multirepo"
	"github.com/sutrakam/graphlume/pkg/model"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// This file holds estate, the multi-repository tool: what `graphlume system`
// answers on the command line, answered for an agent. It is registered only
// when an estate is configured (serve --estate, or estate: in config), and
// only on the full profile, because a server serving one repository has no
// use for its schema.
//
// The answers are the federation's own reports, read through the service the
// CLI layer injects; this file only turns them into the envelope. Two rules
// are enforced here as well as in internal/multirepo, because an agent acts on
// what this file emits: every row names its member, and a row whose
// relationship crosses a repository boundary is never `exact`.

// EstateService answers estate questions. *multirepo.EstateService is the
// production implementation; the CLI builds it over the members' indexes,
// opened read-only through a pool, and hands it in through Deps.
type EstateService interface {
	Members() []multirepo.EstateMember
	// Unreadable maps each member that cannot be opened now to the reason.
	Unreadable(ctx context.Context) map[string]string
	Links(ctx context.Context) (*multirepo.Report, error)
	Callers(ctx context.Context, symbol, path string, limit int) (*multirepo.SystemCallersReport, multirepo.SymbolPick, error)
	Impact(ctx context.Context, symbol, path string, opts multirepo.ImpactOptions) (*multirepo.SystemImpactReport, multirepo.SymbolPick, error)
}

var _ EstateService = (*multirepo.EstateService)(nil)

// estateStatus is the index_status an estate answer carries: it is read from
// several members' indexes, not from this server's own.
const estateStatus = "estate"

const (
	estateLinks   = "links"
	estateCallers = "callers"
	estateImpact  = "impact"
)

// maxEstateDepth bounds the impact walk an agent may ask for.
const maxEstateDepth = 8

type estateArgs struct {
	Action        string `json:"action" jsonschema:"What to ask of the estate: 'links' (HTTP route links between members, tables more than one member touches, Go function and method calls into another member's module, with version warnings), 'callers' (who calls symbol, in its own member and in the others), or 'impact' (what depends on symbol across members)."`
	Symbol        string `json:"symbol,omitempty" jsonschema:"For callers and impact: the symbol as member:name, e.g. 'lib:Do', or 'lib:Client.Send' for a method. The member is a name from the estate file."`
	Path          string `json:"path,omitempty" jsonschema:"For callers and impact: a file or directory in that member, to choose between definitions that share the name."`
	Depth         int    `json:"depth,omitempty" jsonschema:"For impact: hops to walk out from the symbol, repository crossings included, 1 to 8. Default 3."`
	MinConfidence string `json:"min_confidence,omitempty" jsonschema:"Lowest confidence to follow or return: exact, high, medium or low. For impact the default is medium, as for 'graphlume system impact'; rows below it are not walked. For links and callers the default returns every row, and a floor drops the rows below it and says how many."`
	pageArg
}

// registerEstate adds estate when an estate service is configured. The full
// profile's nil filter admits it; every other profile names it nowhere.
func (s *Server) registerEstate(allowed map[string]bool) {
	if s.deps.Estate == nil {
		return
	}
	addTool(s, allowed, &sdk.Tool{
		Name:        "estate",
		Description: toolDescriptions()["estate"],
	}, s.estateTool)
}

func (s *Server) estateTool(ctx context.Context, req *sdk.CallToolRequest, args estateArgs) (*sdk.CallToolResult, *Response, error) {
	if s.deps.Estate == nil {
		return failWith("unsupported",
			"Start the server with 'graphlume serve --estate <file>', or set estate: in your user config.",
			"no estate is configured for this server")
	}
	page, err := s.page(args.pageArg, "row(s)")
	if err != nil {
		return failWith("invalid_argument", "", "%v", err)
	}
	action := strings.ToLower(strings.TrimSpace(args.Action))
	switch action {
	case estateLinks:
		floor, err := parseMinConfidence(args.MinConfidence)
		if err != nil {
			return failFrom(err, "%v", err)
		}
		return s.estateLinks(ctx, req, floor, page)
	case estateCallers:
		floor, err := parseMinConfidence(args.MinConfidence)
		if err != nil {
			return failFrom(err, "%v", err)
		}
		return s.estateCallers(ctx, req, args, floor, page)
	case estateImpact:
		floor, err := parseImpactFloor(args.MinConfidence)
		if err != nil {
			return failFrom(err, "%v", err)
		}
		if floor == "" {
			floor = impact.DefaultMinConfidence
		}
		depth := args.Depth
		if depth < 0 || depth > maxEstateDepth {
			return failWith("invalid_argument", "Pass depth from 1 to 8, or omit it for 3.",
				"depth %d is out of range", depth)
		}
		if depth == 0 {
			depth = multirepo.DefaultSystemImpactDepth
		}
		return s.estateImpact(ctx, req, args, depth, floor, page)
	case "":
		return failWith("invalid_argument", "Pass action: 'links', 'callers' or 'impact'.", "action is required")
	default:
		return failWith("invalid_argument", "Pass action: 'links', 'callers' or 'impact'.",
			"unknown action %q", action)
	}
}

// crossingConfidence is the grade a row that crosses a repository boundary
// may carry. The federation already never produces `exact` for one; this is
// the same rule applied where the agent reads it, so a regression there
// cannot reach an agent as a compiler-proven fact.
func crossingConfidence(c model.Confidence) string {
	if c == model.ConfidenceExact {
		return string(model.ConfidenceHigh)
	}
	return string(c)
}

// meetsFloor reports whether c is at or above floor. An unknown grade fails a
// floor above low, so a floor never admits what it cannot judge.
func meetsFloor(c string, floor model.Confidence) bool {
	if floor == "" || floor == model.ConfidenceLow {
		return true
	}
	cc := model.Confidence(c)
	return cc.Valid() && cc.Rank() >= floor.Rank()
}

// estateProvenance replaces the local index's HEAD comparison, which says
// nothing about an answer read from other repositories' indexes. It never
// claims a match: each member is indexed at its own commit, and the member
// notes say which are old or were built by another version.
func estateProvenance() *Provenance {
	return &Provenance{Caveat: "This answer is read from the estate members' own indexes, each at the commit " +
		"it was indexed at, not from this repository's index. The member notes say which are old or were " +
		"built by another version; working-tree edits in any member are not reflected."}
}

// cliFlagWords rewords the federation's notes, written for the command line,
// into this tool's argument names.
var cliFlagWords = strings.NewReplacer(
	"(raise --depth)", "(raise depth, up to 8)",
	" (raise --limit)", "",
	" (--limit); raise it to see the rest", ", the most this tool walks; lower depth or raise min_confidence to narrow it",
)

// estateLowerBound is the reading rule every estate answer carries.
const estateLowerBound = "This is a LOWER BOUND. Only what the members' indexes recorded is joined: a call " +
	"assembled at run time, an unresolved reference, a method call whose receiver's type the source " +
	"does not show, and every repository outside the estate are absent, so an empty answer means " +
	"nothing was found, never that nothing crosses."

// estateCrossing explains the confidence of rows that cross a boundary.
const estateCrossing = "A row that crosses a repository boundary is never `exact`: route matching is a " +
	"heuristic and no compiler checked two repositories together, so its confidence is the weakest " +
	"leg of its path and `high` at most. Rows inside one member keep that member's recorded grade."

// unavailableNotes names each member that could not be read, and says the
// answer is a floor because of it.
func unavailableNotes(names []string, statuses map[string]string) []string {
	if len(names) == 0 {
		return nil
	}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	out := []string{fmt.Sprintf("INCOMPLETE: %d member(s) could not be read (%s), so nothing they hold "+
		"appears here and every count is a floor.", len(sorted), strings.Join(sorted, ", "))}
	for _, n := range sorted {
		if why := statuses[n]; why != "" {
			out = append(out, fmt.Sprintf("member %s: %s", n, why))
		}
	}
	return out
}

// memberReasons maps each unreadable member to why. Callers and impact
// reports name the member but not the reason, which the service knows.
func (s *Server) memberReasons(ctx context.Context, names []string) map[string]string {
	if len(names) == 0 {
		return nil
	}
	return s.deps.Estate.Unreadable(ctx)
}

// ---------------------------------------------------------------- links

func (s *Server) estateLinks(ctx context.Context, req *sdk.CallToolRequest, floor model.Confidence, page *Page) (*sdk.CallToolResult, *Response, error) {
	rep, err := s.deps.Estate.Links(ctx)
	if err != nil {
		return failFrom(err, "estate links: %v", err)
	}

	var items []Item
	dropped := 0
	add := func(it Item) {
		if !meetsFloor(it.Confidence, floor) {
			dropped++
			return
		}
		items = append(items, it)
	}

	edges := append([]multirepo.CrossRepoEdge(nil), rep.Edges...)
	sort.SliceStable(edges, func(i, j int) bool {
		a, b := edges[i], edges[j]
		switch {
		case a.FromMember != b.FromMember:
			return a.FromMember < b.FromMember
		case a.FromFile != b.FromFile:
			return a.FromFile < b.FromFile
		case a.FromLine != b.FromLine:
			return a.FromLine < b.FromLine
		case a.ToMember != b.ToMember:
			return a.ToMember < b.ToMember
		default:
			return a.ToEndpoint < b.ToEndpoint
		}
	})
	for _, e := range edges {
		handler := e.ToEndpoint
		if e.ToHandler != "" {
			handler = e.ToHandler
		}
		why := fmt.Sprintf("%s %s calls %s; answered in %s by %s:%s (%s:%d); %s match",
			e.FromMember, nonEmptyOr(e.FromSymbol, "(top level)"), callText(e.Method, e.Path),
			e.ToMember, e.ToMember, handler, e.ToEndpointFile, e.ToEndpointLine, e.Match)
		if e.Note != "" {
			why += "; " + e.Note
		}
		add(Item{
			Kind: "http_link", Name: e.FromMember + ":" + nonEmptyOr(e.FromSymbol, "(top level)"),
			FQN:  e.FromMember + " -> " + e.ToMember + " " + e.ToEndpoint,
			Path: e.FromFile, Line: e.FromLine,
			Confidence: crossingConfidence(e.Confidence), Source: "heuristic", Why: why,
		})
	}

	var goNotes []string
	if g := rep.GoModules; g != nil {
		links := append([]multirepo.GoLink(nil), g.Links...)
		sort.SliceStable(links, func(i, j int) bool {
			a, b := links[i], links[j]
			switch {
			case a.FromMember != b.FromMember:
				return a.FromMember < b.FromMember
			case a.FromFile != b.FromFile:
				return a.FromFile < b.FromFile
			case a.FromLine != b.FromLine:
				return a.FromLine < b.FromLine
			case a.Call != b.Call:
				return a.Call < b.Call
			default:
				return a.ToMember+a.ToSymbol < b.ToMember+b.ToSymbol
			}
		})
		for _, l := range links {
			why := fmt.Sprintf("%s calls %s, linked to %s:%s (%s) at %s:%d through module %s",
				l.CallKind, l.Call, l.ToMember, l.ToSymbol, l.ToKind, l.ToFile, l.ToLine, l.Module)
			if l.Note != "" {
				why += "; " + l.Note
			}
			add(Item{
				Kind: "go_" + l.CallKind + "_link", Name: l.FromMember + ":" + l.FromSymbol,
				FQN:  l.FromMember + " -> " + l.ToMember + ":" + l.ToSymbol,
				Path: l.FromFile, Line: l.FromLine,
				Confidence: crossingConfidence(l.Confidence), Source: multirepo.LegGoModule, Why: why,
			})
		}
		for _, p := range g.Pairs {
			goNotes = append(goNotes, fmt.Sprintf("Go module %s: %s imports it from %s; function calls linked %d, "+
				"ambiguous %d, unmatched %d; method calls linked %d, ambiguous %d, not resolvable %d.",
				p.Module, p.Importer, p.Provider, p.Links, p.Ambiguous, p.Unmatched,
				p.MethodLinks, p.MethodAmbiguous, sumCounts(p.MethodNotResolvable)))
			goNotes = append(goNotes, p.Warnings...)
		}
		goNotes = append(goNotes, g.Notes...)
	}

	tables := append([]multirepo.SharedTable(nil), rep.SharedTables...)
	sort.SliceStable(tables, func(i, j int) bool { return tables[i].Table < tables[j].Table })
	for _, st := range tables {
		var members []string
		for _, p := range st.Participants {
			members = append(members, p.Member)
		}
		sort.Strings(members)
		why := fmt.Sprintf("touched by %s; written by %s; read by %s",
			strings.Join(members, ", "), orNoneList(st.Writers), orNoneList(st.Readers))
		if st.WriteWrite {
			why += "; more than one member WRITES it"
		}
		if st.Note != "" {
			why += "; " + st.Note
		}
		add(Item{
			Kind: "shared_table", Name: strings.Join(members, ",") + ":" + st.Table,
			Confidence: crossingConfidence(st.Confidence), Source: "heuristic", Why: why,
		})
	}

	available := 0
	statuses := map[string]string{}
	for _, m := range rep.Members {
		if m.Available {
			available++
		} else {
			statuses[m.Name] = m.Unavailable
		}
	}
	httpN, goN, tableN := len(rep.Edges), 0, len(rep.SharedTables)
	if rep.GoModules != nil {
		goN = len(rep.GoModules.Links)
	}
	resp := &Response{
		IndexStatus: estateStatus,
		Provenance:  estateProvenance(),
		Summary: fmt.Sprintf("Estate of %d member(s), %d readable: %d HTTP route link(s), %d Go function or "+
			"method link(s), %d table(s) touched by more than one member.",
			len(rep.Members), available, httpN, goN, tableN),
	}
	resp.Notes = append(resp.Notes, unavailableNotes(rep.Unjoined.UnavailableMembers, statuses)...)
	for _, m := range rep.Members {
		for _, w := range m.Warnings {
			resp.Notes = append(resp.Notes, fmt.Sprintf("member %s: %s", m.Name, w))
		}
	}
	resp.Notes = append(resp.Notes, goNotes...)
	if u := rep.Unjoined; len(u.Calls)+len(u.AmbiguousCalls)+len(u.Endpoints) > 0 {
		resp.Notes = append(resp.Notes, fmt.Sprintf("Not joined: %d outbound call(s) matched no route in the estate, "+
			"%d matched routes in several members and were not linked, %d route(s) no member calls. "+
			"'graphlume system' lists them.", len(u.Calls), len(u.AmbiguousCalls), len(u.Endpoints)))
	}
	if note := truncationNote(rep); note != "" {
		resp.Notes = append(resp.Notes, note)
		resp.markTruncated("the estate report cut a section at its row cap")
	}
	if n := floorNote(floor, dropped); n != "" {
		resp.Notes = append(resp.Notes, n)
	}
	resp.Notes = append(resp.Notes, estateLowerBound, estateCrossing)
	paginate(resp, page, items)
	return s.reply(ctx, req, resp)
}

// truncationNote says which sections of the system report hit their caps.
func truncationNote(rep *multirepo.Report) string {
	counts := map[string]int{}
	for k, v := range rep.Unjoined.Truncated {
		counts[k] += v
	}
	if rep.GoModules != nil {
		for k, v := range rep.GoModules.Truncated {
			counts["go "+k] += v
		}
	}
	if len(counts) == 0 {
		return ""
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s: %d more", k, counts[k]))
	}
	return "The estate report cut sections at their row caps (" + strings.Join(parts, ", ") +
		"); 'graphlume system' with larger --max-* flags lists them."
}

func callText(method, path string) string {
	if method == "" {
		return path
	}
	return method + " " + path
}

func nonEmptyOr(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

func orNoneList(xs []string) string {
	if len(xs) == 0 {
		return "none"
	}
	return strings.Join(xs, ", ")
}

func sumCounts(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// ---------------------------------------------------------------- callers and impact

// symbolRefused answers a member:symbol that could not be resolved: a
// malformed argument is the caller's error, a missing symbol or an
// unreadable member is an answer.
func (s *Server) symbolRefused(ctx context.Context, req *sdk.CallToolRequest, err error) (*sdk.CallToolResult, *Response, error) {
	var pe *multirepo.SymbolPickError
	if !errors.As(err, &pe) {
		return failFrom(err, "estate: %v", err)
	}
	members := make([]string, 0, len(s.deps.Estate.Members()))
	for _, m := range s.deps.Estate.Members() {
		members = append(members, m.Name)
	}
	switch pe.Kind {
	case multirepo.PickMalformed, multirepo.PickUnknownMember:
		return failWith("invalid_argument",
			"Pass symbol as member:name, where member is one of: "+strings.Join(members, ", ")+".",
			"%s", pe.Error())
	case multirepo.PickNotFound:
		return s.reply(ctx, req, &Response{
			IndexStatus: estateStatus,
			Provenance:  estateProvenance(),
			Summary:     pe.Error() + ".",
			Notes: []string{"Names must match exactly; write a method as Owner.name (lib:Client.Send). " +
				"Absence here is not proof the symbol does not exist: generated, oversized and excluded " +
				"files are not indexed."},
		})
	case multirepo.PickMemberUnreadable:
		return s.reply(ctx, req, &Response{
			IndexStatus: estateStatus,
			Provenance:  estateProvenance(),
			Summary:     pe.Error() + ".",
			Notes: []string{"That member's index could not be opened, so nothing can be said about its " +
				"symbols. The reason names the command that repairs it; the other members still answer " +
				"action 'links'."},
		})
	}
	return failFrom(err, "estate: %v", err)
}

// pickNotes says which definition answered when several shared the name.
func pickNotes(p multirepo.SymbolPick) []string {
	if !p.Ambiguous() {
		return nil
	}
	count := fmt.Sprintf("%d", len(p.All))
	if p.LookupLimit > 0 && len(p.All) >= p.LookupLimit {
		count = "At least " + count
	}
	return []string{fmt.Sprintf("AMBIGUOUS: %s definitions share the name %q in %s. This answers for the first, "+
		"%s:%d, and says nothing about the others (%s). Pass path to choose one.",
		count, p.Name, p.Target.Member, p.Target.File, p.Target.Line, strings.Join(p.Locations(), ", "))}
}

func (s *Server) estateCallers(ctx context.Context, req *sdk.CallToolRequest, args estateArgs, floor model.Confidence, page *Page) (*sdk.CallToolResult, *Response, error) {
	if strings.TrimSpace(args.Symbol) == "" {
		return failWith("invalid_argument", "Pass symbol as member:name, e.g. 'lib:Do'.", "callers needs symbol")
	}
	// A fixed bound rather than offset+limit, so every page is a slice of
	// one ordering: the rows are sorted before the bound cuts them.
	rep, pick, err := s.deps.Estate.Callers(ctx, args.Symbol, strings.TrimSpace(args.Path), multirepo.DefaultSystemCallersLimit)
	if err != nil {
		return s.symbolRefused(ctx, req, err)
	}

	var items []Item
	dropped := 0
	crossing := 0
	for _, c := range rep.Callers {
		conf := string(c.Confidence)
		why := "leg " + c.Leg
		source := c.Source
		if c.Leg == multirepo.LegGoModule || c.Member != rep.Target.Member {
			conf = crossingConfidence(c.Confidence)
			why = fmt.Sprintf("leg %s: calls %s across a repository boundary", c.Leg, c.Call)
			source = multirepo.LegGoModule
			crossing++
		} else if c.Source != "" {
			why += " (" + c.Source + ")"
		}
		if !meetsFloor(conf, floor) {
			dropped++
			continue
		}
		items = append(items, Item{
			Kind: "caller", Name: c.Member + ":" + c.Caller, Path: c.File, Line: c.Line,
			Confidence: conf, Source: source, Why: why,
		})
	}

	t := rep.Target
	members := map[string]bool{}
	for _, c := range rep.Callers {
		members[c.Member] = true
	}
	resp := &Response{
		IndexStatus: estateStatus,
		Provenance:  estateProvenance(),
		Summary: fmt.Sprintf("%d caller(s) of %s:%s (%s at %s:%d) in %d member(s), %d across a repository boundary.",
			len(rep.Callers)+rep.Truncated, t.Member, t.Qualified, t.Kind, t.File, t.Line, len(members), crossing),
	}
	resp.Notes = append(resp.Notes, pickNotes(pick)...)
	resp.Notes = append(resp.Notes, unavailableNotes(rep.UnavailableMembers, s.memberReasons(ctx, rep.UnavailableMembers))...)
	resp.Notes = append(resp.Notes, rep.Warnings...)
	if rep.Truncated > 0 {
		note := fmt.Sprintf("Only the first %d callers are read; %d more exist.", multirepo.DefaultSystemCallersLimit, rep.Truncated)
		if rep.TruncationNote != "" {
			note += " " + cliFlagWords.Replace(rep.TruncationNote) + "."
		}
		resp.Notes = append(resp.Notes, note)
		resp.markTruncated("the estate callers bound was reached")
	}
	if n := floorNote(floor, dropped); n != "" {
		resp.Notes = append(resp.Notes, n)
	}
	resp.Notes = append(resp.Notes, estateLowerBound, estateCrossing)
	paginate(resp, page, items)
	return s.reply(ctx, req, resp)
}

func (s *Server) estateImpact(ctx context.Context, req *sdk.CallToolRequest, args estateArgs, depth int, floor model.Confidence, page *Page) (*sdk.CallToolResult, *Response, error) {
	if strings.TrimSpace(args.Symbol) == "" {
		return failWith("invalid_argument", "Pass symbol as member:name, e.g. 'lib:Do'.", "impact needs symbol")
	}
	rep, pick, err := s.deps.Estate.Impact(ctx, args.Symbol, strings.TrimSpace(args.Path), multirepo.ImpactOptions{
		MaxDepth: depth, MaxRows: multirepo.DefaultSystemImpactRows,
		Kinds: impact.DefaultKinds, MinConfidence: floor,
	})
	if err != nil {
		return s.symbolRefused(ctx, req, err)
	}

	items := make([]Item, 0, len(rep.Rows))
	crossing := 0
	for _, r := range rep.Rows {
		conf := string(r.Confidence)
		if r.Via == multirepo.LegGoModule || r.Member != rep.Target.Member {
			conf = crossingConfidence(r.Confidence)
			crossing++
		}
		items = append(items, Item{
			Kind: "dependent", Name: r.Member + ":" + r.Symbol, Path: r.File, Line: r.Line,
			Confidence: conf, Source: r.Via,
			Why: fmt.Sprintf("depth %d via %s; reaches %s", r.Depth, r.Via, r.Reaches),
		})
	}

	t := rep.Target
	resp := &Response{
		IndexStatus: estateStatus,
		Provenance:  estateProvenance(),
		Summary: fmt.Sprintf("%d dependent(s) of %s:%s (%s at %s:%d) within depth %d at confidence %s and above, "+
			"%d reached across a repository boundary; members reached: %s.",
			len(rep.Rows), t.Member, t.Qualified, t.Kind, t.File, t.Line, rep.MaxDepth, floor, crossing,
			strings.Join(rep.Members, ", ")),
	}
	resp.Notes = append(resp.Notes, pickNotes(pick)...)
	resp.Notes = append(resp.Notes, unavailableNotes(rep.UnavailableMembers, s.memberReasons(ctx, rep.UnavailableMembers))...)
	resp.Notes = append(resp.Notes, rep.Warnings...)
	if rep.TruncationNote != "" {
		resp.Notes = append(resp.Notes, "Truncated: "+cliFlagWords.Replace(rep.TruncationNote)+".")
		if rep.Truncated > 0 {
			resp.markTruncated("the estate impact walk hit a bound")
		}
	}
	resp.Notes = append(resp.Notes, estateLowerBound, estateCrossing)
	paginate(resp, page, items)
	return s.reply(ctx, req, resp)
}
