package mcp

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/capability/deadcode"
	"github.com/sutrakam/graphlume/internal/capability/hotspots"
	"github.com/sutrakam/graphlume/internal/graph"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// This file holds the `full`-profile tools: three that navigate the graph
// (callees, implementations, a path between two symbols) and two that report
// on the repository as a whole (unused definitions, change hotspots).
//
// None of them adds a way of knowing anything. Each is a different question
// asked of the same edges, so each inherits the same two obligations as
// callers: say how every relationship was established, and never let an
// empty or short answer read as proof.

// ---------------------------------------------------------------- arguments

type pathArgs struct {
	From     string   `json:"from" jsonschema:"Symbol the path starts at, e.g. 'handleCallback'."`
	To       string   `json:"to" jsonschema:"Symbol the path should reach, e.g. 'saveTransaction'."`
	FromPath string   `json:"from_path,omitempty" jsonschema:"File or directory of the 'from' definition, when several symbols share the name."`
	FromLine int      `json:"from_line,omitempty" jsonschema:"Line the 'from' definition starts on."`
	ToPath   string   `json:"to_path,omitempty" jsonschema:"File or directory of the 'to' definition, when several symbols share the name."`
	ToLine   int      `json:"to_line,omitempty" jsonschema:"Line the 'to' definition starts on."`
	MaxDepth int      `json:"max_depth,omitempty" jsonschema:"Longest route to look for, in hops. Default 6. Raise it only if the answer was 'no route found' and you expect a long one."`
	Via      string   `json:"via,omitempty" jsonschema:"Intermediate symbol the path must pass through."`
	ViaPath  string   `json:"via_path,omitempty" jsonschema:"File path of the 'via' definition."`
	ViaLine  int      `json:"via_line,omitempty" jsonschema:"Line the 'via' definition starts on."`
	Through  []string `json:"through,omitempty" jsonschema:"Further symbols the path must pass through, in order, after 'via' (at most 3; each must name one definition)."`
	Exclude  []string `json:"exclude,omitempty" jsonschema:"Symbol names to exclude from paths."`
	Kinds    []string `json:"kinds,omitempty" jsonschema:"Edge kinds to traverse (e.g. ['calls'])."`
	minConfidenceArg
}

type deadCodeArgs struct {
	Path            string `json:"path,omitempty" jsonschema:"Only report definitions in files under this path prefix, e.g. 'internal/billing/'."`
	MinCertainty    string `json:"min_certainty,omitempty" jsonschema:"'likely' (the default) returns only candidates where every ordinary reason for a missing caller was ruled out. 'possible' adds the rest, which are mostly uses the index could not bind: search before trusting any of them."`
	IncludeExported bool   `json:"include_exported,omitempty" jsonschema:"Also report exported definitions, which are never better than 'possible'. Default false: in a library the whole public API has no caller in the index."`
	IncludeTestOnly bool   `json:"include_test_only,omitempty" jsonschema:"Also list definitions that DO have callers, every one of them in a test file. They come in their own section, always 'possible' and never 'likely': production code only its tests keep alive is a judgement call, not unused code. Default false: it is a second pass over the index."`
	Limit           int    `json:"limit,omitempty" jsonschema:"Most candidates to return. Default 25."`
}

type hotspotArgs struct {
	Path         string `json:"path,omitempty" jsonschema:"Only rank files under this path prefix."`
	Commits      int    `json:"commits,omitempty" jsonschema:"How many recent commits to read. Default 500."`
	Limit        int    `json:"limit,omitempty" jsonschema:"Most files to return. Default 10."`
	IncludeTests bool   `json:"include_tests,omitempty" jsonschema:"Also rank test files. Default false: tests change with the code they cover."`
}

// registerStructure adds the full-profile tools.
func (s *Server) registerStructure(allowed map[string]bool) {
	descs := toolDescriptions()

	addTool(s, allowed, &sdk.Tool{
		Name:        "callees",
		Description: descs["callees"],
	}, s.findCallees)

	addTool(s, allowed, &sdk.Tool{
		Name:        "implementations",
		Description: descs["implementations"],
	}, s.findImplementations)

	// The whole-repository reports read only the registry, so like
	// endpoints they register whether or not a graph is present.
	addTool(s, allowed, &sdk.Tool{
		Name:        "deadcode",
		Description: descs["deadcode"],
	}, s.findDeadCode)

	addTool(s, allowed, &sdk.Tool{
		Name:        "hotspots",
		Description: descs["hotspots"],
	}, s.findHotspots)

	if s.deps.Graph == nil {
		return
	}
	addTool(s, allowed, &sdk.Tool{
		Name:        "trace",
		Description: descs["trace"],
	}, s.traceCallPath)
}

// ---------------------------------------------------------------- shared lookup

// Selecting the definition is selectSymbol's (select.go), shared with every
// other tool that takes a symbol.

// outgoingItems renders edges that START at a symbol.
func outgoingItems(edges []registry.EdgeHit) []Item {
	items := make([]Item, 0, len(edges))
	for _, e := range edges {
		items = append(items, Item{
			Kind:       strings.ToLower(e.Kind),
			Name:       e.DstName,
			Path:       e.EvidenceFile,
			Line:       e.EvidenceLine,
			Confidence: e.Confidence,
			Source:     e.Source,
			Why:        e.Detail,
		})
	}
	return items
}

// gate judges coverage for an answer about sel's definition, records it as the
// response's Reliability and puts the caveat ahead of every other note. Shared
// wording with every other relationship tool, through coverage.
func (s *Server) gate(ctx context.Context, resp *Response, sel selection, subject string, about question) coverage {
	cov := s.coverageFor(ctx, sel.RunID, sel.Target.Language, sel.Target.FilePath, capability.TargetBinding{}, about)
	s.applyCoverage(resp, cov, subject)
	return cov
}

// applyCoverage attaches cov to resp: its Reliability, and its caveat first.
// When the response already carries a Reliability (a route has two ends), the
// worse of the two is kept.
func (s *Server) applyCoverage(resp *Response, cov coverage, subject string) {
	resp.Reliability = worse(resp.Reliability, cov.reliability(subject))
	if gate := cov.caveat(subject); gate != nil {
		resp.Notes = append(gate, resp.Notes...)
	}
}

// ---------------------------------------------------------------- callees

func (s *Server) findCallees(ctx context.Context, req *sdk.CallToolRequest, args relationArgs) (*sdk.CallToolResult, *Response, error) {
	if strings.TrimSpace(args.Name) == "" {
		return failWith("invalid_argument",
			"Pass a non-empty name, then call callees again.",
			"name must not be empty")
	}
	minConf, err := parseMinConfidence(args.MinConfidence)
	if err != nil {
		return failFrom(err, "%v", err)
	}
	page, err := s.page(args.pageArg, "callee(s)")
	if err != nil {
		return failWith("invalid_argument", "Pass limit and offset as non-negative numbers, or omit them.", "%v", err)
	}
	q := symbolQuery{Tool: "callees", Param: "name", PathParam: "path", LineParam: "line",
		Name: args.Name, Path: args.Path, Line: args.Line,
		Prefer: callableKinds, PreferWord: "functions and methods", PreferNoun: "function or method"}
	sel, sc, scResp := s.selectSymbol(ctx, req, q)
	if sc != nil {
		return sc, scResp, nil
	}

	filter := registry.EdgeFilter{MinConfidence: minConf, Kinds: []string{string(model.EdgeCalls)}}
	edges, total, err := s.deps.Registry.EdgePage(ctx, sel.RunID, sel.Target.ID, false, page.Limit, page.Offset, filter)
	if err != nil {
		return failFrom(err, "relationship lookup failed: %v", err)
	}
	page.Total = total

	resp := &Response{
		IndexRevision: sel.RunID,
		IndexStatus:   sel.Status,
		Summary: fmt.Sprintf("%s (%s:%d) calls %d symbol(s) the index could bind.",
			sel.Target.Name, sel.Target.FilePath, sel.Target.StartLine, total),
		Items: outgoingItems(edges),
		Page:  page,
	}
	s.gate(ctx, resp, sel, "callee lookup", aboutCallees)
	// The gap here has a different shape from callers', so it gets its
	// own sentence: a call into a library has no definition in this index and
	// can never appear, however well the repository itself resolves.
	resp.Notes = append(resp.Notes,
		"Only calls to definitions INSIDE this index are listed. Calls into libraries, the standard "+
			"library and generated code have no definition here to bind to, so this is what the "+
			"symbol calls within the repository, not everything it calls.")
	if n := sel.note(q); n != "" {
		resp.Notes = append(resp.Notes, n)
	}
	return s.reply(ctx, req, resp)
}

// ---------------------------------------------------------------- implementations

// hierarchyKinds are the edges that record a type relationship.
var hierarchyKinds = []string{string(model.EdgeImplements), string(model.EdgeExtends)}

// implementationArgs is implementations' arguments: the relationship
// tools' and one more.
type implementationArgs struct {
	relationArgs

	DirectOnly bool `json:"direct_only,omitempty" jsonschema:"Only direct subtypes. Default false: also those reached through other subtypes, direct ones first."`
}

func (s *Server) findImplementations(ctx context.Context, req *sdk.CallToolRequest, args implementationArgs) (*sdk.CallToolResult, *Response, error) {
	if strings.TrimSpace(args.Name) == "" {
		return failWith("invalid_argument",
			"Pass a non-empty name, then call implementations again.",
			"name must not be empty")
	}
	minConf, err := parseMinConfidence(args.MinConfidence)
	if err != nil {
		return failFrom(err, "%v", err)
	}
	page, err := s.page(args.pageArg, "type relationship(s)")
	if err != nil {
		return failWith("invalid_argument", "Pass limit and offset as non-negative numbers, or omit them.", "%v", err)
	}
	// The question is about a TYPE. "What implements Render" was answered for
	// a method named Render, because the first definition of the name was
	// taken whatever it was.
	q := symbolQuery{Tool: "implementations", Param: "name", PathParam: "path", LineParam: "line",
		Name: args.Name, Path: args.Path, Line: args.Line,
		Prefer: typeKinds, PreferWord: "types", PreferNoun: "type",
		// An abstract or overridden method has implementations too: the index
		// records an override as an extends or implements edge on the method.
		Admit: func(ctx context.Context, runID int64, c registry.SymbolCandidate) bool {
			f := registry.EdgeFilter{Kinds: hierarchyKinds}
			_, up, uerr := s.deps.Registry.EdgePage(ctx, runID, c.ID, false, 1, 0, f)
			_, down, derr := s.deps.Registry.EdgePage(ctx, runID, c.ID, true, 1, 0, f)
			return uerr != nil || derr != nil || up+down > 0
		},
		AdmitWhy: "has an extends or implements relationship recorded"}
	sel, sc, scResp := s.selectSymbol(ctx, req, q)
	if sc != nil {
		return sc, scResp, nil
	}
	target := sel.Target

	// ONE list, supertypes first, paged as one. There are always far fewer
	// supertypes than subtypes, so they lead and cannot be crowded out; and
	// because offset and limit run over the concatenation, "rows 51-100 of
	// 136" means the same rows on every call. Capping the two
	// separately at 25 each would leave no way to see the 26th.
	//
	// The subtypes are every type that reaches the target, direct ones first.
	// They are not the direct ones alone, which would let a base class with a hundred
	// descendants answer "4 implementing it" for the four that name it.
	filter := registry.EdgeFilter{MinConfidence: minConf, Kinds: hierarchyKinds}
	up, upTotal, err := s.deps.Registry.EdgePage(ctx, sel.RunID, target.ID, false, page.Limit, page.Offset, filter)
	if err != nil {
		return failFrom(err, "relationship lookup failed: %v", err)
	}
	// Asked for even when the page is already full of supertypes, with a
	// limit of one, because the counts come back with it.
	downLimit, downOffset := page.Limit-len(up), max(page.Offset-upTotal, 0)
	fetchOnlyCounts := downLimit <= 0
	if fetchOnlyCounts {
		downLimit = 1
	}
	subs, err := s.deps.Registry.SubtypeClosure(ctx, sel.RunID, target.ID, filter, args.DirectOnly, downLimit, downOffset)
	if err != nil {
		return failFrom(err, "relationship lookup failed: %v", err)
	}
	if fetchOnlyCounts {
		subs.Hits = nil
	}
	page.Total = upTotal + subs.Total

	resp := &Response{
		IndexRevision: sel.RunID,
		IndexStatus:   sel.Status,
		Summary: fmt.Sprintf("%s (%s:%d): %s, %d it implements or extends.",
			target.Name, target.FilePath, target.StartLine, subtypeCounts(subs, args.DirectOnly), upTotal),
		Page: page,
	}
	proven := false
	for _, e := range up {
		why := target.Name + " " + strings.ToLower(e.Kind) + " this"
		if e.Detail != "" {
			why += " — " + e.Detail
		}
		resp.Items = append(resp.Items, Item{
			Kind: "supertype", Name: e.DstName, Path: e.EvidenceFile, Line: e.EvidenceLine,
			Confidence: e.Confidence, Source: e.Source, Why: why,
		})
		proven = proven || e.Confidence == string(model.ConfidenceExact)
	}
	for _, e := range subs.Hits {
		resp.Items = append(resp.Items, subtypeItem(e, target.Name))
		proven = proven || e.Confidence == string(model.ConfidenceExact)
	}

	cov := s.coverageFor(ctx, sel.RunID, target.Language, target.FilePath, capability.TargetBinding{}, aboutHierarchy)
	cov.requireHierarchy()
	if !proven {
		// A compiler-proven row means a compiler index was imported, and that
		// index does see structural implementations.
		cov.Structural = structuralTyping(target.Language, target.Kind)
	}
	if !cov.NoHierarchy {
		if gap, gerr := s.subtypeGap(ctx, sel.RunID, target); gerr == nil {
			cov.Unlinked, cov.UnlinkedDetail = gap.lead(target.Name), gap.detail(target.Name)
		}
	}
	s.applyCoverage(resp, cov, "type hierarchy lookup")

	if n := localSubtypeNote(subs, target.Name); n != "" {
		resp.Notes = append(resp.Notes, n)
	}
	resp.Notes = append(resp.Notes,
		"A type relationship is recorded only where the source states it (extends, implements, an "+
			"embedded type) or a compiler index proved it. Structural typing — a Go type that satisfies "+
			"an interface without naming it, a TypeScript object that happens to fit — is visible only "+
			"after 'graphlume scip import'. Treat an empty list as 'none declared', not 'none exist'.")
	if n := sel.note(q); n != "" {
		resp.Notes = append(resp.Notes, n)
	}
	return s.reply(ctx, req, resp)
}

// subtypeCounts is the clause of the first line that says how many types extend
// or implement the target, split into the ones that name it and the ones that
// reach it through another type.
func subtypeCounts(subs registry.Subtypes, directOnly bool) string {
	out := subtypeCountsOfTypes(subs, directOnly)
	if subs.Local > 0 {
		out += fmt.Sprintf("; %s%d anonymous or local type(s) inside functions also extend it (not rows: they are not types)",
			capFloor(subs.Capped), subs.Local)
	}
	return out
}

func capFloor(capped bool) string {
	if capped {
		return "at least "
	}
	return ""
}

func subtypeCountsOfTypes(subs registry.Subtypes, directOnly bool) string {
	floor := ""
	if subs.Capped {
		floor = "at least "
	}
	indirect := subs.All - subs.Direct
	switch {
	case directOnly && indirect > 0:
		return fmt.Sprintf("%d implementing or extending it directly (%s%d more reach it through other types; "+
			"direct_only=false lists them)", subs.Direct, floor, indirect)
	case directOnly:
		return fmt.Sprintf("%d implementing or extending it directly, and no others reach it through other types",
			subs.Direct)
	case indirect > 0:
		return fmt.Sprintf("%s%d implementing or extending it (%d directly, %s%d through other types; "+
			"direct_only=true lists the direct ones alone)", floor, subs.All, subs.Direct, floor, indirect)
	default:
		return fmt.Sprintf("%d implementing or extending it, all directly", subs.Direct)
	}
}

// localSubtypeNote names the functions that contain an anonymous or local type
// extending the target. The index attributes such a class to the function that
// holds it, so the edge's source is a function and not a type; listing it as a
// subtype put `buildInitParams` among the subclasses of a router. It is counted
// on the first line and named here instead.
func localSubtypeNote(subs registry.Subtypes, target string) string {
	if subs.Local == 0 {
		return ""
	}
	var where []string
	for _, h := range subs.LocalHits {
		where = append(where, fmt.Sprintf("inside %s (%s:%d)", h.SrcName, h.EvidenceFile, h.EvidenceLine))
	}
	more := ""
	if n := subs.Local - len(subs.LocalHits); n > 0 {
		more = fmt.Sprintf(", and %s%d more", capFloor(subs.Capped), n)
	}
	return fmt.Sprintf("%s%d anonymous or local type(s) declared inside functions extend or implement %s: %s%s. "+
		"They are subtypes with no name of their own, so they are counted here and are not rows of the list.",
		capFloor(subs.Capped), subs.Local, target, strings.Join(where, "; "), more)
}

// subtypeItem renders one subtype. A direct one says it names the target; a
// deeper one says which type it names instead, how far down it is, and that its
// confidence is the weakest link of the path.
func subtypeItem(e registry.SubtypeHit, target string) Item {
	it := Item{
		Kind: "subtype", Name: e.SrcName, Path: e.EvidenceFile, Line: e.EvidenceLine,
		Confidence: string(e.PathConfidence), Source: e.Source,
	}
	if e.Depth <= 1 {
		it.Why = "implements or extends " + target
		if e.Detail != "" {
			it.Why += " — " + e.Detail
		}
		return it
	}
	it.Why = fmt.Sprintf("depth %d: %s %s, which reaches %s (confidence is the weakest link on the path)",
		e.Depth, strings.ToLower(e.Kind), e.DstName, target)
	return it
}

// subtypeGap is what the index holds about subtypes of the target that are not
// in the list: supertype references that did not become an edge, and lines that
// read as a declaration naming the target and are recorded as nothing.
type subtypeGap struct {
	refs registry.SupertypeRefs
	text registry.UnrecordedSubtypes
}

func (s *Server) subtypeGap(ctx context.Context, runID int64, t registry.SymbolCandidate) (subtypeGap, error) {
	var g subtypeGap
	var err error
	if g.refs, err = s.deps.Registry.SupertypeReferences(ctx, runID, t.ID, t.Name); err != nil {
		return g, err
	}
	defs, err := s.deps.Registry.FindSymbolsByName(ctx, runID, t.Name, "", maxSelectRows)
	if err != nil {
		return g, err
	}
	spans := make([]registry.DefSpan, 0, len(defs))
	for _, h := range defs {
		spans = append(spans, registry.DefSpan{Path: h.FilePath, From: h.StartLine, To: max(h.EndLine, h.StartLine)})
	}
	g.text, err = s.deps.Registry.UnrecordedSubtypeDeclarations(ctx, runID, t.Name, spans)
	return g, err
}

// parts are the two findings as clauses, each with its own count.
func (g subtypeGap) parts(name string) []string {
	var out []string
	if n := g.refs.Outstanding(); n > 0 {
		var why []string
		if g.refs.NoSource > 0 {
			why = append(why, fmt.Sprintf("%d on a type this index does not define", g.refs.NoSource))
		}
		if g.refs.Unresolved > 0 {
			why = append(why, fmt.Sprintf("%d unresolved", g.refs.Unresolved))
		}
		if g.refs.Ambiguous > 0 {
			why = append(why, fmt.Sprintf("%d ambiguous", g.refs.Ambiguous))
		}
		if g.refs.Pending > 0 {
			why = append(why, fmt.Sprintf("%d not yet resolved", g.refs.Pending))
		}
		out = append(out, fmt.Sprintf("%d of the %d supertype reference(s) spelled %q did not become a subtype below (%s)",
			n, g.refs.References, name, strings.Join(why, ", ")))
	}
	if g.text.Known && g.text.Named > 0 {
		out = append(out, fmt.Sprintf("%s%d other line(s) declare a named type whose supertype list names it and are not "+
			"recorded as a subtype", capFloor(!g.text.Complete), g.text.Named))
	}
	if g.text.Known && g.text.Anonymous > 0 {
		out = append(out, fmt.Sprintf("%s%d anonymous class or object expression(s) name it as a supertype and are not "+
			"listed (an anonymous type has no name to list)", capFloor(!g.text.Complete), g.text.Anonymous))
	}
	return out
}

func placeList(groups ...[]registry.Place) string {
	var sites []string
	for _, g := range groups {
		for _, p := range g {
			sites = append(sites, fmt.Sprintf("%s:%d", p.Path, p.Line))
		}
	}
	return strings.Join(sites, ", ")
}

// lead is the clause for the first line. Empty when nothing is outstanding.
func (g subtypeGap) lead(name string) string {
	parts := g.parts(name)
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ") + ", so subtypes exist that this list does not carry."
}

// detail is the full-length account, with the sites, for a note.
func (g subtypeGap) detail(name string) string {
	parts := g.parts(name)
	if len(parts) == 0 {
		return ""
	}
	out := strings.Join(parts, ". Also, ") + ". Each of them may be a type that extends or implements " + name +
		" and is missing from the list, so the count above is a floor"
	if sites := placeList(g.text.Sample, g.text.AnonymousSample); sites != "" {
		out += " (lines: " + sites + ")"
	}
	return out + ". Search for the name before concluding which types extend it."
}

// ---------------------------------------------------------------- trace

const (
	// defaultPathDepth is how long a route trace looks for unless
	// asked. Six hops spans a request handler to a storage call in most
	// layered services; beyond it a "route" is rarely an explanation.
	defaultPathDepth = 6
	// maxPathDepth bounds what a caller may ask for.
	maxPathDepth = 12
	// maxExcludeNames bounds the exclude list of one path query, and
	// maxExcludeDefinitions how many definitions one excluded name resolves
	// to, so resolving the list before the search stays a handful of queries.
	maxExcludeNames       = 32
	maxExcludeDefinitions = 1000
	// maxTracedPaths is how many shortest routes are rendered. Each costs one
	// item per hop, so this and the depth together bound the response.
	maxTracedPaths = 3
)

func (s *Server) traceCallPath(ctx context.Context, req *sdk.CallToolRequest, args pathArgs) (*sdk.CallToolResult, *Response, error) {
	if strings.TrimSpace(args.From) == "" || strings.TrimSpace(args.To) == "" {
		return failWith("invalid_argument",
			"Pass both 'from' and 'to' as symbol names, then call trace again.",
			"from and to must both be non-empty")
	}
	depth := args.MaxDepth
	switch {
	case depth < 0:
		return failWith("invalid_argument",
			"Pass max_depth as a positive number of hops, or omit it for the default.",
			"max_depth must not be negative")
	case depth == 0:
		depth = defaultPathDepth
	case depth > maxPathDepth:
		depth = maxPathDepth
	}

	fromQ := symbolQuery{Tool: "trace", Param: "from", PathParam: "from_path", LineParam: "from_line",
		Name: args.From, Path: args.FromPath, Line: args.FromLine,
		Prefer: callableKinds, PreferWord: "functions and methods", PreferNoun: "function or method",
		Rest: " to=" + quoteArg(args.To)}
	toQ := symbolQuery{Tool: "trace", Param: "to", PathParam: "to_path", LineParam: "to_line",
		Name: args.To, Path: args.ToPath, Line: args.ToLine,
		Prefer: callableKinds, PreferWord: "functions and methods", PreferNoun: "function or method",
		Rest: " from=" + quoteArg(args.From)}
	from, sc, scResp := s.selectSymbol(ctx, req, fromQ)
	if sc != nil {
		return sc, scResp, nil
	}
	to, sc, scResp := s.selectSymbol(ctx, req, toQ)
	if sc != nil {
		return sc, scResp, nil
	}

	var viaID int64
	if strings.TrimSpace(args.Via) != "" {
		viaQ := symbolQuery{Tool: "trace", Param: "via", PathParam: "via_path", LineParam: "via_line",
			Name: args.Via, Path: args.ViaPath, Line: args.ViaLine,
			Prefer: callableKinds, PreferWord: "functions and methods", PreferNoun: "function or method"}
		viaSym, sc, scResp := s.selectSymbol(ctx, req, viaQ)
		if sc != nil {
			return sc, scResp, nil
		}
		viaID = viaSym.Target.ID
	}
	var waypoints []int64
	if viaID != 0 {
		waypoints = append(waypoints, viaID)
	}
	if len(args.Through) > 0 {
		if viaID == 0 {
			return failWith("invalid_argument", "Pass 'via' before adding 'through' waypoints.", "through without via")
		}
		if len(args.Through)+1 > graph.MaxWaypoints {
			return failWith("invalid_argument",
				fmt.Sprintf("Pass at most %d waypoints in via and through together.", graph.MaxWaypoints),
				"%d waypoints", len(args.Through)+1)
		}
		for _, name := range args.Through {
			wq := symbolQuery{Tool: "trace", Param: "through", Name: strings.TrimSpace(name),
				Prefer: callableKinds, PreferWord: "functions and methods", PreferNoun: "function or method"}
			wsym, sc, scResp := s.selectSymbol(ctx, req, wq)
			if sc != nil {
				return sc, scResp, nil
			}
			waypoints = append(waypoints, wsym.Target.ID)
		}
	}

	// Excluded names are resolved to ids ONCE, before the search. The filter
	// runs inside the graph's row scan, while that scan holds a pooled
	// connection: a lookup per visited node there was one query per node and,
	// under concurrent calls, a second connection held per call, which is how
	// the pool is exhausted.
	var excludeFunc func(id int64) bool
	if len(args.Exclude) > 0 {
		if len(args.Exclude) > maxExcludeNames {
			return failWith("invalid_argument",
				fmt.Sprintf("Pass at most %d names in exclude, then call trace again.", maxExcludeNames),
				"exclude lists %d names, limit %d", len(args.Exclude), maxExcludeNames)
		}
		excludeIDs := make(map[int64]bool)
		for _, ex := range args.Exclude {
			name := strings.TrimSpace(ex)
			if name == "" {
				continue
			}
			hits, err := s.deps.Registry.FindSymbolsByName(ctx, from.RunID, name, "", maxExcludeDefinitions)
			if err != nil {
				return failFrom(err, "resolve exclude %q: %v", name, err)
			}
			for _, h := range hits {
				excludeIDs[h.ID] = true
			}
		}
		excludeFunc = func(id int64) bool { return excludeIDs[id] }
	}

	var kinds []model.EdgeKind
	for _, k := range args.Kinds {
		if k != "" {
			kinds = append(kinds, model.EdgeKind(k))
		}
	}
	minConf := model.Confidence(args.MinConfidence)

	paths, err := s.deps.Graph.PathsThrough(ctx, from.Target.ID, waypoints, to.Target.ID, maxTracedPaths, depth, kinds, minConf, excludeFunc)
	if err != nil {
		return failFrom(err, "path search failed: %v", err)
	}

	resp := &Response{IndexRevision: from.RunID, IndexStatus: from.Status}
	// A route is only as good as the weaker of its two ends: a path into a
	// language whose calls are not extracted cannot be found from either side.
	s.gate(ctx, resp, from, "path search", aboutRoutes)
	if to.Target.Language != from.Target.Language {
		s.gate(ctx, resp, to, "path search", aboutRoutes)
	}

	if len(paths) == 0 {
		resp.Summary = fmt.Sprintf("No route found from %s (%s:%d) to %s (%s:%d) within %d hop(s).",
			from.Target.Name, from.Target.FilePath, from.Target.StartLine,
			to.Target.Name, to.Target.FilePath, to.Target.StartLine, depth)
		resp.Notes = append(resp.Notes,
			"This is not proof that none exists. A route passes only through relationships the "+
				"index bound, so one unresolved call, an interface dispatch or a callback anywhere "+
				"along the way breaks it. Try callers on the destination and callees on "+
				"the start to see where the two halves stop.")
	} else {
		hops := len(paths[0]) - 1
		resp.Summary = fmt.Sprintf("%d shortest route(s) from %s (%s:%d) to %s (%s:%d), %d hop(s) each.",
			len(paths), from.Target.Name, from.Target.FilePath, from.Target.StartLine,
			to.Target.Name, to.Target.FilePath, to.Target.StartLine, hops)
		for pi, p := range paths {
			for hi := 0; hi+1 < len(p); hi++ {
				resp.Items = append(resp.Items, s.hopItem(ctx, from.RunID, p[hi], p[hi+1], pi+1, hi+1, len(p)-1))
			}
		}
		resp.Notes = append(resp.Notes,
			"Only SHORTEST routes are shown. Longer routes between the same two symbols may exist "+
				"and are not listed. A route is as trustworthy as its weakest hop: read the "+
				"confidence on each one.")
	}
	for _, n := range []string{from.note(fromQ), to.note(toQ)} {
		if n != "" {
			resp.Notes = append(resp.Notes, n)
		}
	}
	return s.reply(ctx, req, resp)
}

// hopItem renders one step of a route, with the evidence for that step.
func (s *Server) hopItem(ctx context.Context, runID, src, dst int64, pathNo, hopNo, hops int) Item {
	where := fmt.Sprintf("route %d, hop %d of %d", pathNo, hopNo, hops)
	e, ok, err := s.deps.Registry.EdgeBetween(ctx, runID, src, dst)
	if err != nil || !ok {
		// The traversal found the hop moments ago, so this is a reindex
		// committing mid-request. Say so rather than inventing a line.
		return Item{Kind: "hop", Name: fmt.Sprintf("#%d -> #%d", src, dst),
			Why: where + "; its evidence could not be re-read (the index may have just been rebuilt)"}
	}
	return Item{
		Kind:       strings.ToLower(e.Kind),
		Name:       e.SrcName + " -> " + e.DstName,
		Path:       e.EvidenceFile,
		Line:       e.EvidenceLine,
		Confidence: e.Confidence,
		Source:     e.Source,
		Why:        where,
	}
}

// ---------------------------------------------------------------- deadcode

const defaultDeadCodeItems = 25

func (s *Server) findDeadCode(ctx context.Context, req *sdk.CallToolRequest, args deadCodeArgs) (*sdk.CallToolResult, *Response, error) {
	// The default is the likely tier alone. The possible tier is dominated by
	// uses the index could not bind, and an agent handed twenty-five of those
	// under the heading "dead code" will act on some of them. Asking for
	// exported definitions is asking for the possible tier, since an exported
	// definition is never graded higher.
	minCert := deadcode.CertaintyLikely
	switch strings.ToLower(strings.TrimSpace(args.MinCertainty)) {
	case "":
		if args.IncludeExported {
			minCert = ""
		}
	case string(deadcode.CertaintyPossible):
		minCert = ""
	case string(deadcode.CertaintyLikely):
	default:
		return failWith("invalid_argument",
			"Pass min_certainty as 'likely' or 'possible', or omit it.",
			"unknown min_certainty %q", args.MinCertainty)
	}
	if args.Limit < 0 {
		return failWith("invalid_argument", "Pass limit as a positive number, or omit it.",
			"limit must not be negative")
	}
	runID, status, err := s.currentRun(ctx)
	if err != nil {
		return s.noRun(ctx, req, err)
	}

	limit := args.Limit
	if limit == 0 {
		limit = defaultDeadCodeItems
	}
	limit = min(limit, s.limits.MaxItems)

	opts := deadcode.Options{
		Limit:           limit,
		MinCertainty:    minCert,
		PathPrefix:      strings.TrimSpace(args.Path),
		IncludeExported: args.IncludeExported,
		Judgeable:       s.deps.Judgeable,
	}
	if args.IncludeTestOnly {
		// Two sections share one item cap. Each gets half, so a full page of
		// unreferenced rows cannot push every test-only row out of the answer.
		opts.Limit = min(limit, max(s.limits.MaxItems/2, 1))
		opts.TestOnly = s.deps.Registry
	}
	rep, err := deadcode.Find(ctx, s.capDeps, s.deps.Registry, runID, opts)
	if err != nil {
		return failFrom(err, "dead-code scan: %v", err)
	}

	resp := &Response{
		IndexRevision: runID,
		IndexStatus:   status,
		Summary: fmt.Sprintf("%d definition(s) have no caller in the index: %d likely unused, %d possibly. "+
			"Showing %d.", rep.Likely+rep.Possible, rep.Likely, rep.Possible, len(rep.Candidates)),
	}
	for _, c := range rep.Candidates {
		resp.Items = append(resp.Items, Item{
			Kind: c.Kind, Name: c.Name, FQN: c.FQN, Path: c.Path, Line: c.Line,
			Why: deadCodeWhy(c),
		})
	}
	if to := rep.TestOnly; to != nil {
		resp.Summary += fmt.Sprintf(" Separately, %d definition(s) have callers only in test files; showing %d.",
			to.Found, len(to.Candidates))
		for _, c := range to.Candidates {
			resp.Items = append(resp.Items, Item{
				Kind: c.Kind, Name: c.Name, FQN: c.FQN, Path: c.Path, Line: c.Line,
				Why: testOnlyWhy(c),
			})
		}
	}

	// The warning leads. A list of deletable-looking functions is the most
	// actionable thing this server returns, and the most dangerous to act on
	// unread.
	resp.Notes = append(resp.Notes, deadcode.Limits)
	// Judged over the run: a dead-code list spans every language in it. The
	// advice is coverage's own, so it names 'graphlume resolve' only when
	// something is actually pending.
	cov := coverageOf(rep.Completeness.Resolution, "")
	cov.About = "what is unused"
	if cov.level() == ReliabilityLow {
		resp.Reliability = cov.reliability("the dead-code scan")
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"COVERAGE TOO LOW — only %.0f%% of the references that name something this index defines "+
				"are bound. Most of what is listed here is more likely unbound than unused.%s",
			100*rep.Completeness.BindingRate, cov.resolveAdvice()))
	}
	if rep.ExportUnknown > 0 {
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"%d candidate(s) in %s are graded possible at best: the index cannot tell whether a "+
				"definition is exported in those languages, so a public API with no caller here looks "+
				"exactly like dead code. Nothing in them is graded likely.",
			rep.ExportUnknown, displayLanguages(rep.ExportUnknownLangs)))
	}
	if rep.SkippedUnjudged > 0 {
		// The first line says "N definitions have no caller", and N leaves
		// these out. When they are all there is, the list is empty because
		// nothing could be judged, and the first line must say that.
		unjudged := fmt.Sprintf("%d definition(s) in %s were not judged, because this index holds no "+
			"reference from that code and every one of them would look unused.",
			rep.SkippedUnjudged, displayLanguages(rep.UnjudgedLangs))
		if rep.Likely+rep.Possible == 0 {
			resp.Reliability = &Reliability{Level: ReliabilityNone, Lead: unjudged}
		} else {
			resp.Summary += " " + unjudged
		}
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"%d definition(s) in %s were not judged: this index holds no reference from those "+
				"languages, so every definition in them would look unused.",
			rep.SkippedUnjudged, displayLanguages(rep.UnjudgedLangs)))
	}
	if n := rep.Skipped - rep.SkippedUnjudged; n > 0 {
		what := "tests, entry points and exported definitions"
		if args.IncludeExported {
			what = "tests and entry points"
		}
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"%d further unreferenced definition(s) were left out as expected: %s.", n, what))
	}
	if rep.LikelyUnchecked > 0 {
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"Every row shown as likely was also checked against the indexed source text. %d more were "+
				"graded likely from the graph alone and not checked, so the likely total in the summary "+
				"is an upper bound; pass a path to check them.", rep.LikelyUnchecked))
	}
	if minCert == deadcode.CertaintyLikely && rep.Possible > 0 {
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"%d more candidate(s) are graded possible and not shown. Most are uses the index could not "+
				"bind. Pass min_certainty='possible' to list them.", rep.Possible))
	}
	if rep.EntryPoints > 0 {
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"%d candidate(s) are spelled like a route's handler or consume an event topic. A framework "+
				"calls such a function without a call expression, so they are graded possible, never likely.",
			rep.EntryPoints))
	}
	if rep.Completeness.Truncated {
		resp.markTruncated(rep.Completeness.TruncatedReason)
	}
	if to := rep.TestOnly; to != nil {
		resp.Notes = append(resp.Notes, testOnlyNotes(to)...)
		if to.Truncated {
			resp.markTruncated(to.TruncatedReason)
		}
	}
	return s.reply(ctx, req, resp)
}

// testOnlyNotes is what an agent must read beside the test-only rows: what
// the section claims, how far it was checked, and what it left out.
func testOnlyNotes(to *deadcode.TestOnlyReport) []string {
	if to.TestFiles == 0 {
		return []string{"REACHED ONLY FROM TESTS: the index holds no file it recognises as a test, " +
			"so no definition can have all its callers in one. This is not a finding of none."}
	}
	notes := []string{"REACHED ONLY FROM TESTS is a separate finding from unused. " + deadcode.TestOnlyLimits}
	if to.NameElsewhere > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d of the %d test-only definition(s) checked against the source text have their name in code "+
				"that is not a test: either a production use the graph has no edge for (a function passed "+
				"as a value, a type used without a call) or an unrelated identifier spelled the same. "+
				"They are listed last and say so; search for the name to tell which.",
			to.NameElsewhere, to.TextChecked))
	}
	if unchecked := to.Found - to.TextChecked; unchecked > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d more test-only definition(s) were found from the graph alone and not checked against the "+
				"text, so the test-only total is an upper bound; pass a path to check them.", unchecked))
	}
	if left := to.Skipped + to.EntryPoints; left > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d definition(s) with callers only in test files were left out as expected: %d exported, "+
				"named like an entry point or in a language the index does not bind, and %d that a "+
				"framework calls (route handlers, event consumers).", left, to.Skipped, to.EntryPoints))
	}
	return notes
}

// testOnlyWhy is the one-line justification shown beside a test-only row. It
// opens with the section's name, because the row sits in the same list as the
// unreferenced ones and must not be read as one of them.
func testOnlyWhy(c deadcode.Candidate) string {
	why := fmt.Sprintf("reached only from tests (possible, never likely), %d line(s): all %d reference(s) "+
		"the index bound are in test files", c.Lines(), c.TestReferences)
	var also []string
	for _, r := range c.Reasons {
		if r != deadcode.ReasonTestOnly {
			also = append(also, r.Text())
		}
	}
	if len(also) == 0 {
		return why + ", and its name appears in no other code that is not a test"
	}
	return why + ", but " + strings.Join(also, "; ")
}

// displayLanguages joins index language names in the spelling a reader uses.
func displayLanguages(langs []string) string {
	out := make([]string, len(langs))
	for i, l := range langs {
		out[i] = capability.DisplayLanguage(l)
	}
	return strings.Join(out, ", ")
}

// deadCodeWhy is the one-line justification shown beside a candidate.
func deadCodeWhy(c deadcode.Candidate) string {
	size := fmt.Sprintf("%d line(s)", c.Lines())
	if c.Certainty == deadcode.CertaintyLikely {
		return "likely unused, " + size + ": unexported, no caller, no unbound reference with this name, " +
			"and the name appears nowhere else in the indexed text"
	}
	reasons := make([]string, 0, len(c.Reasons))
	for _, r := range c.Reasons {
		reasons = append(reasons, r.Text())
	}
	return "possibly unused, " + size + ": no caller in the index, but " + strings.Join(reasons, "; ")
}

// ---------------------------------------------------------------- hotspots

// defaultHotspotItems is how many files are ranked unless asked. Each can carry
// up to three coupled files as rows of their own, so ten hotspots is at most
// forty items, inside the response's item bound.
const defaultHotspotItems = 10

func (s *Server) findHotspots(ctx context.Context, req *sdk.CallToolRequest, args hotspotArgs) (*sdk.CallToolResult, *Response, error) {
	if args.Commits < 0 || args.Limit < 0 {
		return failWith("invalid_argument", "Pass commits and limit as positive numbers, or omit them.",
			"commits and limit must not be negative")
	}
	runID, status, err := s.currentRun(ctx)
	if err != nil {
		return s.noRun(ctx, req, err)
	}
	if s.deps.History == nil {
		return s.guide(ctx, req, runID, status,
			"No commit history is available to this server, so nothing can be ranked.",
			"Hotspots are read from git history. The project is not a git repository, or its "+
				"history could not be opened. Everything else this server answers is unaffected.")
	}

	limit := args.Limit
	if limit == 0 {
		limit = defaultHotspotItems
	}
	limit = min(limit, s.limits.MaxItems)

	rep, err := hotspots.Rank(ctx, s.deps.History, s.deps.Registry, runID, hotspots.Options{
		Limit:        limit,
		CommitWindow: args.Commits,
		PathPrefix:   strings.TrimSpace(args.Path),
		IncludeTests: args.IncludeTests,
	})
	if err != nil {
		return failFrom(err, "hotspot ranking: %v", err)
	}

	resp := &Response{
		IndexRevision: runID,
		IndexStatus:   status,
		Summary: fmt.Sprintf("%d file(s) ranked by change frequency x size, from the last %d commit(s) "+
			"touching %d indexed file(s)%s.", len(rep.Hotspots), rep.CommitsRead, rep.FilesChanged,
			mergesPhrase(rep.MergesSkipped)),
		Notes: rep.Notes,
	}
	if rep.CommitsRead > 0 && len(rep.Hotspots) > 0 {
		// The window stated so a count can be checked: git lists no paths for
		// a merge, and the ranking does not count one.
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"A file's commit count reproduces with `git log -n %d --name-only --format= HEAD`: "+
				"the commits in the window that touched it, merges not counted.", rep.CommitsRead))
	}
	for _, h := range rep.Hotspots {
		// Name is the base name and Path the whole path: the two are bounded
		// separately, and a long path repeated in both would be cut in both.
		resp.Items = append(resp.Items, Item{
			Kind: "file", Name: path.Base(h.Path), Path: h.Path, Source: string(model.SourceGit),
			Why: hotspotWhy(h),
		})
		// Each coupled file is its own row, directly under its hotspot. Folded
		// into the hotspot's one-line reason they were cut off by the per-item
		// byte bound as soon as paths were long, and the coupling is the part
		// of this answer no other tool can give.
		for _, p := range h.ChangesWith {
			resp.Items = append(resp.Items, Item{
				Kind: "changes_with", Name: path.Base(p.Path), Path: p.Path, Source: string(model.SourceGit),
				Why: fmt.Sprintf("committed with %s in %d of its %d commit(s)", path.Base(h.Path), p.Together, h.Commits),
			})
		}
	}
	if len(rep.Hotspots) > 0 {
		resp.Notes = append(resp.Notes,
			"The score orders this list and means nothing across repositories. A changes_with row "+
				"names a file committed together with the hotspot above it: when no call or import "+
				"connects them, that is a dependency impact cannot see. Check those files "+
				"when you change the hotspot.")
	}
	return s.reply(ctx, req, resp)
}

// mergesPhrase is the parenthesis that says how many merges a window held.
func mergesPhrase(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("; %d of them are merges and are not counted", n)
}

// hotspotWhy is the one-line account of a hotspot's rank.
func hotspotWhy(h hotspots.Hotspot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "score %d: %d commit(s), %s, %d symbol(s), %d inbound relationship(s)",
		h.Score, h.Commits, humanSize(h.Size), h.Symbols, h.Dependents)
	if h.LastChanged > 0 {
		fmt.Fprintf(&b, ", last changed %s", time.Unix(h.LastChanged, 0).UTC().Format("2006-01-02"))
	}
	return b.String()
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// compile-time check that the graph the server holds can trace a path.
var _ interface {
	Paths(ctx context.Context, from, to int64, maxPaths, maxDepth int) ([][]int64, error)
} = (*graph.Graph)(nil)
