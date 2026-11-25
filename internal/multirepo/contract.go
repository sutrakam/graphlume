package multirepo

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sutrakam/graphlume/pkg/model"
)

// API contract checks across the members of a system.
//
// # What this adds to the system graph
//
// [Federator.Build] answers "which call in one repository is answered by which
// route in another". A call that is NOT answered lands in its Unjoined list
// with one sentence, whatever the reason. This file takes those same two
// projections — the staged outbound calls and the declared routes of every
// member — and asks the narrower question a reviewer has before a release:
// does each client still call something that exists?
//
// # The four findings and the one observation
//
//   - method_mismatch: the path of a call matches a route, and no route with
//     that path accepts the verb the call uses.
//   - path_variable_mismatch: a route begins exactly where the call does and
//     differs only in how many path segments follow.
//   - no_matching_route: nothing any member declares matches the call. It is a
//     broken call, or a call into something outside this system; the index
//     cannot tell which, and the finding says so.
//   - base_path_unverified: the call and a route agree only if a path prefix
//     this index does not hold is assumed on one side — a mount point, a
//     gateway rewrite, a client base URL. Reported, always at `low`, rather
//     than either silently matched or silently called broken.
//   - uncalled routes: a route no indexed call reaches. An observation rather
//     than a finding, because the callers this tool cannot see are most of the
//     world.
//
// # What is deliberately not done
//
// Nothing here is `exact`, for the reason the package comment gives. Nothing
// here resolves a host to a member, because no index records which service a
// hostname is. And nothing here compares query, header or body parameters: the
// index holds a call's literal URL and a route's method and path, and nothing
// about what either side sends or reads beyond that. Each of these is stated
// in [ContractReport.Caveats] rather than left for a reader to infer from an
// empty section.

// DefaultMaxContractRows bounds each section of a contract report.
const DefaultMaxContractRows = 50

// maxRoutesPerFinding bounds the routes named under one finding. A call whose
// path is /users aligns with a /users route in every member that has one, and
// a finding that lists thirty of them is not thirty times as useful.
const maxRoutesPerFinding = 3

// WithMaxContractRows caps each section of the contract report.
func WithMaxContractRows(n int) Option {
	return func(f *Federator) error {
		if n <= 0 {
			return fmt.Errorf("multirepo: max contract rows must be positive, got %d", n)
		}
		f.maxContractRows = n
		return nil
	}
}

// WithExtractionCoverage names the client conventions and route frameworks the
// indexer extracts, so the report can print them.
//
// Injected rather than imported: the lists belong to internal/analyze/api, and
// a second copy here would be the one that goes stale. With no coverage
// supplied the report says that it was not told, instead of printing a list it
// made up.
func WithExtractionCoverage(clients, routeFrameworks []string) Option {
	return func(f *Federator) error {
		f.extractedClients = append([]string(nil), clients...)
		f.extractedRoutes = append([]string(nil), routeFrameworks...)
		return nil
	}
}

// WithTestPaths tells the contract report which files are test code, so calls
// made from them are counted and set aside instead of being checked.
//
// A test that requests /api/fake from a server it started itself is not a
// client of this system, and the indexer already makes the matching decision
// for routes: one declared in a test file is a fixture, not an endpoint.
// Without this, every such call would be reported as having no matching route.
// The predicate is injected because the convention it encodes belongs to
// internal/capability, which this package has no other reason to import. With
// none supplied, nothing is set aside.
func WithTestPaths(isTest func(path string) bool) Option {
	return func(f *Federator) error {
		f.isTestPath = isTest
		return nil
	}
}

// ContractKind names what disagrees between a call and the routes.
type ContractKind string

const (
	// ContractMethodMismatch: a route has the call's path, and no route with
	// that path accepts the call's verb.
	ContractMethodMismatch ContractKind = "method_mismatch"
	// ContractPathVariables: a route starts where the call starts and the two
	// differ in how many path segments follow.
	ContractPathVariables ContractKind = "path_variable_mismatch"
	// ContractNoRoute: no route in any member matches the call.
	ContractNoRoute ContractKind = "no_matching_route"
	// ContractBasePath: the two agree only under a base path nothing indexed
	// states.
	ContractBasePath ContractKind = "base_path_unverified"
)

// contractKindOrder is the order findings are listed in: the ones with a route
// on the other side first, because those are the ones a reader can act on by
// opening two files.
func contractKindOrder() []ContractKind {
	return []ContractKind{
		ContractMethodMismatch, ContractPathVariables, ContractNoRoute, ContractBasePath,
	}
}

// ContractCall is the client side of a finding.
type ContractCall struct {
	Member string `json:"member"`
	// Caller is the declaration containing the call; empty for a call outside
	// every declaration.
	Caller string `json:"caller,omitempty"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	// Method is empty when the call's syntax stated no verb this scan read.
	Method string `json:"method,omitempty"`
	// Path is the target as written. For a dynamic call it is only the literal
	// text before the first run-time part.
	Path    string `json:"path"`
	Dynamic bool   `json:"dynamic"`
	Client  string `json:"client,omitempty"`
	// Host is the host of an absolute target, kept because matching DROPS it:
	// no index records which member a hostname is, so the host is shown to the
	// reader instead of being used.
	Host string `json:"host,omitempty"`
}

// ContractRoute is the server side of a finding.
type ContractRoute struct {
	Member string `json:"member"`
	// Endpoint is the route as declared, "GET /api/orders/{id}".
	Endpoint string `json:"endpoint"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	// Handler is empty when the framework's syntax revealed none.
	Handler     string `json:"handler,omitempty"`
	HandlerFile string `json:"handler_file,omitempty"`
}

// ContractFinding is one call that disagrees with what the members declare.
type ContractFinding struct {
	Kind ContractKind `json:"kind"`
	// Confidence is how far the indexed text supports the finding. Never
	// `exact`, and never above `high`.
	Confidence model.Confidence `json:"confidence"`
	Call       ContractCall     `json:"call"`
	// Routes are the declarations the finding is about, with file and line.
	// Empty for no_matching_route, which is by definition a finding with
	// nothing on the other side.
	Routes []ContractRoute `json:"routes"`
	// MoreRoutes counts further routes the finding applies to that were not
	// listed.
	MoreRoutes int `json:"more_routes,omitempty"`
	// Detail says what was compared and what differs.
	Detail string `json:"detail"`
	// Weakened is true when the confidence was lowered a tier because part of
	// the system was not read; ContractReport.Weakeners says which part. A
	// finding already at `low` stays there and is not marked.
	Weakened bool `json:"weakened,omitempty"`
}

// UncalledRoute is a route no indexed call reaches.
type UncalledRoute struct {
	ContractRoute
	// Confidence is always `low`: the absence of an indexed caller is the
	// weakest thing this report says.
	Confidence model.Confidence `json:"confidence"`
}

// UncomparedCall is a call the index does not hold enough of to compare.
type UncomparedCall struct {
	Call   ContractCall `json:"call"`
	Reason string       `json:"reason"`
}

// LanguageFiles is one language present in a member.
type LanguageFiles struct {
	Language string `json:"language"`
	Files    int    `json:"files"`
}

// ContractMember is what the contract report can say about one member: the
// same status the system graph prints, plus which of its languages the two
// extractions this report depends on actually read.
type ContractMember struct {
	MemberStatus
	// Stale is true when the index is older than the stale-after age.
	Stale bool `json:"stale"`
	// CallsReadIn are the languages present whose outbound calls are
	// extracted; CallsNotReadIn are the ones present whose calls are not. A
	// member with only the second kind makes calls this report cannot see.
	CallsReadIn    []LanguageFiles `json:"client_calls_read_in"`
	CallsNotReadIn []LanguageFiles `json:"client_calls_not_read_in"`
	// RoutesReadIn / RoutesNotReadIn are the same split for route extraction.
	RoutesReadIn    []LanguageFiles `json:"routes_read_in"`
	RoutesNotReadIn []LanguageFiles `json:"routes_not_read_in"`
}

// ContractSummary is the arithmetic of the comparison. Every count is taken
// before any section is capped, and CallsExamined is the sum of the seven
// outcomes (Matched, MatchedByPrefix, the four findings and NotCompared), so
// the report can be checked by adding it up.
type ContractSummary struct {
	CallsExamined int `json:"calls_examined"`
	// CallsInTestFiles were set aside, not examined: a test calling a server
	// it started is not a client of this system. Not part of CallsExamined.
	CallsInTestFiles int `json:"calls_in_test_files"`

	// Matched: method and path agree with at least one declared route.
	Matched int `json:"matched"`
	// AnsweredInOwnMember is the subset of Matched bound inside the calling
	// repository by the indexer itself.
	AnsweredInOwnMember int `json:"answered_in_own_member"`
	// MatchedSeveralRoutes is the subset of Matched that more than one route
	// could answer. Not a contract problem; the system graph refuses to draw an
	// edge for these and says why.
	MatchedSeveralRoutes int `json:"matched_several_routes"`
	// MatchedByPrefix: a run-time-assembled target whose literal prefix starts
	// a route that accepts the verb.
	MatchedByPrefix int `json:"matched_by_prefix"`

	MethodMismatch       int `json:"method_mismatch"`
	PathVariableMismatch int `json:"path_variable_mismatch"`
	NoMatchingRoute      int `json:"no_matching_route"`
	BasePathUnverified   int `json:"base_path_unverified"`
	NotCompared          int `json:"not_compared"`

	RoutesExamined int `json:"routes_examined"`
	RoutesUncalled int `json:"routes_uncalled"`
}

// ContractReport is the contract view across the members of a system.
type ContractReport struct {
	Members []ContractMember `json:"members"`
	Summary ContractSummary  `json:"summary"`
	// Findings are sorted by kind, then strongest first.
	Findings []ContractFinding `json:"findings"`
	// UncalledRoutes is informational and weaker than every finding.
	UncalledRoutes []UncalledRoute `json:"uncalled_routes"`
	// NotCompared are calls the index holds too little of to compare.
	NotCompared []UncomparedCall `json:"not_compared"`
	// Weakeners are the reasons every finding marked Weakened lost a tier: an
	// unreadable member, a partial or stale index, a language routes are not
	// extracted from.
	Weakeners []string `json:"weakeners"`
	// UnavailableMembers names members whose index could not be read.
	UnavailableMembers []string `json:"unavailable_members"`
	// Truncated records what the row cap cut, per section.
	Truncated  map[string]int `json:"truncated,omitempty"`
	Provenance Provenance     `json:"provenance"`
	// Caveats are the standing limits of the method.
	Caveats []string `json:"caveats"`
}

// Complete reports whether every member was read.
func (r *ContractReport) Complete() bool { return len(r.UnavailableMembers) == 0 }

// Contracts compares every member's outbound calls with every member's routes.
//
// It takes the same members [Federator.Build] does, refuses the same malformed
// lists, and carries an unreadable member through as a finding for the same
// reason: a contract report that silently dropped a server would report every
// call into it as broken.
func (f *Federator) Contracts(ctx context.Context, members []Member) (*ContractReport, error) {
	facts, err := loadMembers(ctx, members)
	if err != nil {
		return nil, err
	}

	languages := make(map[string]map[string]int, len(facts))
	for _, mf := range facts {
		if !mf.member.Available() {
			continue
		}
		counts, err := mf.member.Source.LanguageCounts(ctx, mf.member.RunID)
		if err != nil {
			return nil, fmt.Errorf("multirepo: member %q: read language counts: %w", mf.member.Name, err)
		}
		languages[mf.member.Name] = counts
	}

	rep := &ContractReport{
		Findings:           []ContractFinding{},
		UncalledRoutes:     []UncalledRoute{},
		NotCompared:        []UncomparedCall{},
		UnavailableMembers: []string{},
		Truncated:          map[string]int{},
	}
	rep.Members = f.contractMembers(facts, languages)
	rep.Weakeners = contractWeakeners(rep.Members)
	rep.Provenance = f.provenance(facts)
	for _, m := range rep.Members {
		if !m.Available {
			rep.UnavailableMembers = append(rep.UnavailableMembers, m.Name)
		}
	}

	routes := buildRoutes(facts)
	called := map[routeKey]bool{}
	weakened := len(rep.Weakeners) > 0

	for _, mf := range facts {
		if !mf.member.Available() {
			continue
		}
		for _, c := range mf.calls {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if f.isTestPath != nil && f.isTestPath(c.FilePath) {
				rep.Summary.CallsInTestFiles++
				continue
			}
			rep.Summary.CallsExamined++

			if c.Binding == bindingBound {
				// The indexer already bound this call inside its own
				// repository, with the framework's own precedence rules. It
				// matched; there is nothing to check twice.
				rep.Summary.Matched++
				rep.Summary.AnsweredInOwnMember++
				if c.EndpointID > 0 {
					called[routeKey{mf.member.Name, c.EndpointID}] = true
				}
				continue
			}

			v := classifyCall(routes, c)
			for _, rt := range v.reached {
				called[routeKey{rt.member, rt.endpoint.SymbolID}] = true
			}
			switch v.outcome {
			case outcomeMatched:
				rep.Summary.Matched++
				if len(v.reached) > 1 {
					rep.Summary.MatchedSeveralRoutes++
				}
			case outcomeMatchedByPrefix:
				rep.Summary.MatchedByPrefix++
			case outcomeNotCompared:
				rep.Summary.NotCompared++
				rep.NotCompared = append(rep.NotCompared, UncomparedCall{
					Call: contractCall(mf.member.Name, c), Reason: v.detail,
				})
			default:
				fd := v.finding(mf.member.Name, c)
				if weakened {
					// Marked only when the tier actually moved: a finding
					// that was already `low` has no tier left to lose, and
					// saying it lost one would be inexact.
					if lower := demote(fd.Confidence); lower != fd.Confidence {
						fd.Confidence, fd.Weakened = lower, true
					}
				}
				fd.Confidence = capHeuristic(fd.Confidence)
				rep.Findings = append(rep.Findings, fd)
				rep.Summary.count(fd.Kind)
			}
		}
	}

	for _, rt := range routes {
		rep.Summary.RoutesExamined++
		if called[routeKey{rt.member, rt.endpoint.SymbolID}] {
			continue
		}
		rep.Summary.RoutesUncalled++
		rep.UncalledRoutes = append(rep.UncalledRoutes, UncalledRoute{
			ContractRoute: contractRoute(rt),
			Confidence:    model.ConfidenceLow,
		})
	}

	f.boundContract(rep)
	rep.Caveats = f.contractCaveats()
	if len(rep.Truncated) == 0 {
		rep.Truncated = nil
	}
	return rep, nil
}

// routeKey identifies one route in one member. Symbol ids are per index, so
// the member name is part of the identity.
type routeKey struct {
	member string
	id     int64
}

func (s *ContractSummary) count(k ContractKind) {
	switch k {
	case ContractMethodMismatch:
		s.MethodMismatch++
	case ContractPathVariables:
		s.PathVariableMismatch++
	case ContractNoRoute:
		s.NoMatchingRoute++
	case ContractBasePath:
		s.BasePathUnverified++
	}
}

// boundContract sorts every section and applies the row cap.
//
// Sorted BEFORE it is cut, so what survives a cap is the strongest evidence
// rather than whichever member's name sorts first.
func (f *Federator) boundContract(rep *ContractReport) {
	rank := map[ContractKind]int{}
	for i, k := range contractKindOrder() {
		rank[k] = i
	}
	sort.SliceStable(rep.Findings, func(i, j int) bool {
		a, b := rep.Findings[i], rep.Findings[j]
		switch {
		case a.Kind != b.Kind:
			return rank[a.Kind] < rank[b.Kind]
		case a.Confidence != b.Confidence:
			return a.Confidence.Rank() > b.Confidence.Rank()
		case (a.Call.Host == "") != (b.Call.Host == ""):
			// A call that names a host is the likeliest to be leaving the
			// system on purpose, so it reads after the ones that do not.
			return a.Call.Host == ""
		default:
			return contractCallLess(a.Call, b.Call)
		}
	})
	kept := rep.Findings[:0]
	perKind := map[ContractKind]int{}
	for _, fd := range rep.Findings {
		if perKind[fd.Kind] >= f.maxContractRows {
			rep.Truncated[string(fd.Kind)]++
			continue
		}
		perKind[fd.Kind]++
		kept = append(kept, fd)
	}
	rep.Findings = kept

	sort.SliceStable(rep.NotCompared, func(i, j int) bool {
		return contractCallLess(rep.NotCompared[i].Call, rep.NotCompared[j].Call)
	})
	if n := len(rep.NotCompared) - f.maxContractRows; n > 0 {
		rep.Truncated["not_compared"] = n
		rep.NotCompared = rep.NotCompared[:f.maxContractRows]
	}

	sort.SliceStable(rep.UncalledRoutes, func(i, j int) bool {
		a, b := rep.UncalledRoutes[i], rep.UncalledRoutes[j]
		switch {
		case a.Member != b.Member:
			return a.Member < b.Member
		case a.File != b.File:
			return a.File < b.File
		case a.Line != b.Line:
			return a.Line < b.Line
		default:
			return a.Endpoint < b.Endpoint
		}
	})
	if n := len(rep.UncalledRoutes) - f.maxContractRows; n > 0 {
		rep.Truncated["uncalled_routes"] = n
		rep.UncalledRoutes = rep.UncalledRoutes[:f.maxContractRows]
	}
}

func contractCallLess(a, b ContractCall) bool {
	switch {
	case a.Member != b.Member:
		return a.Member < b.Member
	case a.File != b.File:
		return a.File < b.File
	case a.Line != b.Line:
		return a.Line < b.Line
	case a.Path != b.Path:
		return a.Path < b.Path
	default:
		return a.Method < b.Method
	}
}

// ------------------------------------------------------------ classification

type callOutcome int

const (
	outcomeFinding callOutcome = iota
	outcomeMatched
	outcomeMatchedByPrefix
	outcomeNotCompared
)

// callVerdict is what one call turned out to be.
type callVerdict struct {
	outcome callOutcome
	// kind, confidence and routes describe a finding.
	kind       ContractKind
	confidence model.Confidence
	routes     []routeCandidate
	// detail explains a finding, or says why a call was not compared.
	detail string
	// reached are the routes this call counts as a caller of: the ones it
	// matches, and the ones it MAY match under an assumption this report could
	// not check (a literal prefix, an unverified base path). A route named
	// under base_path_unverified is therefore not also listed as uncalled,
	// which would be the report contradicting itself. A route named under
	// method_mismatch or path_variable_mismatch is not reached: the finding is
	// precisely that the call does not arrive there as declared.
	reached []routeCandidate
}

func (v callVerdict) finding(member string, c callRow) ContractFinding {
	fd := ContractFinding{
		Kind: v.kind, Confidence: v.confidence,
		Call: contractCall(member, c), Routes: []ContractRoute{}, Detail: v.detail,
	}
	for i, rt := range v.routes {
		if i >= maxRoutesPerFinding {
			fd.MoreRoutes = len(v.routes) - maxRoutesPerFinding
			break
		}
		fd.Routes = append(fd.Routes, contractRoute(rt))
	}
	return fd
}

func contractCall(member string, c callRow) ContractCall {
	return ContractCall{
		Member: member, Caller: c.Caller, File: c.FilePath, Line: c.Line,
		Method: c.Method, Path: c.Path, Dynamic: c.Dynamic, Client: c.Client,
		Host: targetHost(c.Path),
	}
}

func contractRoute(rt routeCandidate) ContractRoute {
	return ContractRoute{
		Member: rt.member, Endpoint: rt.endpoint.Signature,
		File: rt.endpoint.FilePath, Line: rt.endpoint.Line,
		Handler: rt.endpoint.Handler, HandlerFile: rt.endpoint.HandlerPath,
	}
}

// targetHost returns the host of an absolute target, lowercased, or "".
func targetHost(p string) string {
	p = strings.TrimSpace(p)
	i := strings.Index(p, "://")
	if i < 0 {
		return ""
	}
	rest := p[i+3:]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		rest = rest[:j]
	}
	return strings.ToLower(rest)
}

// classifyCall compares one call the indexer did not bind inside its own
// repository against every route in the system, the caller's own included.
//
// The caller's own routes are candidates here although [matchCall] excludes
// them, and the difference is deliberate. That function draws a dependency
// EDGE between two repositories, and a same-repository edge is the indexer's
// to draw. This one asks whether anything answers the call at all, and a
// frontend and its backend indexed as one member are still a client and a
// server: a call the indexer left unbound because its verb is wrong is exactly
// what this report exists to show.
//
// The order of the checks is the order of the evidence, strongest first, and a
// call stops at the first one that applies.
func classifyCall(routes []routeCandidate, c callRow) callVerdict {
	method := canonMethod(c.Method)
	segs := canonSegments(c.Path)

	if c.Dynamic {
		return classifyDynamicCall(routes, c, method, segs)
	}
	if len(segs) == 0 {
		// Not compared, but a root route is not then called "uncalled" either:
		// this may be the call that reaches it.
		var roots []routeCandidate
		for _, rt := range routes {
			if len(rt.segs) == 0 {
				roots = append(roots, rt)
			}
		}
		return callVerdict{outcome: outcomeNotCompared, reached: roots, detail: "the target " +
			"canonicalises to the root path, which is indistinguishable from a target this " +
			"scan could not read"}
	}

	// 1. Same path. Either the verb agrees and the call is answered, or it
	// does not and that is the finding.
	var exact, wild, verbExact, verbWild []routeCandidate
	for _, rt := range routes {
		used, ok := segmentsMatch(rt.segs, segs)
		if !ok {
			continue
		}
		agrees := methodsMatch(method, rt.method)
		switch {
		case agrees && used == 0:
			exact = append(exact, rt)
		case agrees:
			wild = append(wild, rt)
		case used == 0:
			verbExact = append(verbExact, rt)
		default:
			verbWild = append(verbWild, rt)
		}
	}
	switch {
	case len(exact) > 0:
		return callVerdict{outcome: outcomeMatched, reached: exact}
	case len(wild) > 0:
		return callVerdict{outcome: outcomeMatched, reached: wild}
	case len(verbExact) > 0:
		return callVerdict{
			kind: ContractMethodMismatch, confidence: model.ConfidenceHigh, routes: verbExact,
			detail: fmt.Sprintf("the path is declared, literally, and accepts %s; no route with "+
				"this path accepts %s", verbsOf(verbExact), verbLabel(c.Method)),
		}
	case len(verbWild) > 0:
		return callVerdict{
			kind: ContractMethodMismatch, confidence: model.ConfidenceMedium, routes: verbWild,
			detail: fmt.Sprintf("the path matches a parameterised route that accepts %s; no "+
				"route matching this path accepts %s. A path variable absorbed a segment, so "+
				"which resource the call names is not proven", verbsOf(verbWild), verbLabel(c.Method)),
		}
	}

	// 2. Same start, different number of segments after it.
	var needsMore, suppliesMore []routeCandidate
	for _, rt := range routes {
		if !methodsMatch(method, rt.method) {
			continue
		}
		if _, ok := routeNeedsMore(rt.segs, segs); ok {
			needsMore = append(needsMore, rt)
		} else if _, ok := callSuppliesMore(rt.segs, segs); ok {
			suppliesMore = append(suppliesMore, rt)
		}
	}
	if len(needsMore) > 0 {
		rt := needsMore[0]
		extra, _ := routeNeedsMore(rt.segs, segs)
		conf := model.ConfidenceMedium
		if method == "" || rt.method == "" {
			conf = demote(conf)
		}
		return callVerdict{
			kind: ContractPathVariables, confidence: conf, routes: needsMore,
			detail: fmt.Sprintf("the nearest route declares %d path variable%s after %s and the "+
				"call supplies none: it has %d path segment%s where the route has %d",
				extra, plural(extra, "", "s"), pathOf(segs), len(segs),
				plural(len(segs), "", "s"), len(rt.segs)),
		}
	}
	if len(suppliesMore) > 0 {
		rt := suppliesMore[0]
		extra, _ := callSuppliesMore(rt.segs, segs)
		return callVerdict{
			kind: ContractPathVariables, confidence: model.ConfidenceLow, routes: suppliesMore,
			detail: fmt.Sprintf("the nearest route ends in a path variable and the call has %d "+
				"more segment%s than it declares (%d against %d). Either the variable count "+
				"changed, or the call targets a sub-route no member declares",
				extra, plural(extra, "", "s"), len(segs), len(rt.segs)),
		}
	}

	// 3. Agreement only under a prefix nothing indexed states.
	if v, ok := classifyBasePath(routes, method, segs); ok {
		return v
	}

	// 4. Nothing.
	detail := "no route declared by any member matches this path"
	if host := targetHost(c.Path); host != "" {
		detail += "; the target names the host " + host + ", so this is an external call " +
			"unless that host is one of these members — no index records which service a host is"
	} else {
		detail += "; the call is broken, or it is answered by something outside this system " +
			"(a static file, a third party, a service absent from the member list)"
	}
	return callVerdict{kind: ContractNoRoute, confidence: model.ConfidenceHigh, detail: detail}
}

// classifyDynamicCall handles a target assembled at run time, of which the
// index keeps only the literal text before the first run-time part.
//
// Everything it says is `low`, because the part of the path that decides the
// answer was never read.
func classifyDynamicCall(routes []routeCandidate, c callRow, method string, segs []string) callVerdict {
	raw := strings.TrimSpace(c.Path)
	// Only whole literal segments are trusted, exactly as matchByPrefix has it:
	// "/api/user" + id is not a call under /api/user.
	if len(segs) > 0 && !strings.HasSuffix(raw, "/") {
		segs = segs[:len(segs)-1]
	}
	if len(segs) == 0 {
		reason := "the target begins with a run-time value — most often a base URL read from " +
			"configuration or the environment, which this tool does not read — and the index " +
			"keeps only the literal text before the first run-time part, so the path that " +
			"follows was not indexed and cannot be compared"
		if raw != "" {
			reason = "the target is assembled at run time and its literal prefix " + quote(raw) +
				" ends before a whole path segment does, so there is no path to compare"
		}
		return callVerdict{outcome: outcomeNotCompared, detail: reason}
	}

	// The call has at least one more segment than its trusted prefix: either
	// the half-segment that was just dropped, or whatever follows the slash.
	var longer, same []routeCandidate
	otherVerb := 0
	for _, rt := range routes {
		if !prefixMatches(rt.segs, segs) {
			continue
		}
		switch {
		case !methodsMatch(method, rt.method):
			otherVerb++
		case len(rt.segs) > len(segs):
			longer = append(longer, rt)
		default:
			same = append(same, rt)
		}
	}
	switch {
	case len(longer) > 0:
		return callVerdict{outcome: outcomeMatchedByPrefix, reached: longer}
	case len(same) > 0:
		return callVerdict{
			kind: ContractPathVariables, confidence: model.ConfidenceLow, routes: same,
			detail: fmt.Sprintf("the call appends at least one run-time segment after %s, and "+
				"the only route that starts there and accepts %s ends there. Only the literal "+
				"prefix of this target is indexed", pathOf(segs), verbLabel(c.Method)),
		}
	}
	detail := fmt.Sprintf("no route in any member starts with %s and accepts %s. Only the "+
		"literal prefix of this run-time target is indexed", pathOf(segs), verbLabel(c.Method))
	if otherVerb > 0 {
		detail += fmt.Sprintf("; %d route%s start%s there with another verb",
			otherVerb, plural(otherVerb, "", "s"), plural(otherVerb, "s", ""))
	}
	return callVerdict{kind: ContractNoRoute, confidence: model.ConfidenceLow, detail: detail}
}

// classifyBasePath looks for a route that would answer the call if one side
// carried a path prefix the other does not.
//
// Two real arrangements produce exactly this and neither is in any index:
//
//   - The service is mounted under a prefix its route declarations do not
//     spell: an Express router attached with app.use("/api", router), a servlet
//     context path, a gateway that strips /orders-service before forwarding.
//     The call is then LONGER than the route.
//   - The client was configured with a base URL that carries a path, so the
//     call is written without it. The call is then SHORTER than the route.
//
// Treating either as a match would be guessing silently; treating it as a
// broken call would report every service behind a gateway as unreachable. So
// it is its own finding, always `low`, and names the prefix that has to be
// true for the two to meet.
func classifyBasePath(routes []routeCandidate, method string, segs []string) (callVerdict, bool) {
	type aligned struct {
		rt       routeCandidate
		prefix   []string
		onServer bool
	}
	var agree, differ []aligned
	for _, rt := range routes {
		prefix, onServer, ok := basePathAligned(rt.segs, segs)
		if !ok {
			continue
		}
		a := aligned{rt: rt, prefix: prefix, onServer: onServer}
		if methodsMatch(method, rt.method) {
			agree = append(agree, a)
		} else {
			differ = append(differ, a)
		}
	}
	hits, verbDiffers := agree, false
	if len(hits) == 0 {
		hits, verbDiffers = differ, true
	}
	if len(hits) == 0 {
		return callVerdict{}, false
	}

	first := hits[0]
	var detail string
	if first.onServer {
		detail = fmt.Sprintf("the call matches this route only if the service is reached under "+
			"%s — a mount point, context path or gateway prefix that no indexed file states",
			pathOf(first.prefix))
	} else {
		detail = fmt.Sprintf("the call matches this route only if %s is prepended to it — by a "+
			"client base URL or a proxy rewrite, configuration this tool does not read",
			pathOf(first.prefix))
	}
	if verbDiffers {
		detail += "; and even then the verb differs"
	}
	rts := make([]routeCandidate, 0, len(hits))
	for _, h := range hits {
		rts = append(rts, h.rt)
	}
	return callVerdict{
		kind: ContractBasePath, confidence: model.ConfidenceLow,
		routes: rts, reached: rts, detail: detail,
	}, true
}

// basePathAligned reports whether the shorter of a route and a call is the
// tail of the longer, and returns the segments the shorter one lacks.
//
// The shared tail must contain a literal segment: /{id} is the tail of every
// path there is. A prefix the CLIENT would have to supply must be literal too,
// because a base URL is a constant and a path variable is not.
func basePathAligned(route, call []string) (prefix []string, onServer, ok bool) {
	switch {
	case len(route) == 0 || len(call) == 0 || len(route) == len(call):
		return nil, false, false
	case len(call) > len(route):
		d := len(call) - len(route)
		if !tailMatches(route, call[d:]) {
			return nil, false, false
		}
		return call[:d], true, true
	default:
		d := len(route) - len(call)
		for _, s := range route[:d] {
			if s == "*" {
				return nil, false, false
			}
		}
		if !tailMatches(route[d:], call) {
			return nil, false, false
		}
		return route[:d], false, true
	}
}

// tailMatches is segmentsMatch with the extra demand that at least one literal
// segment agreed.
func tailMatches(route, call []string) bool {
	used, ok := segmentsMatch(route, call)
	return ok && used < len(route)
}

// routeNeedsMore reports that a route begins with the whole call and then
// declares only path variables, and how many.
//
// GET /api/orders against GET /api/orders/{id}: the route cannot be reached
// without a value the call does not supply.
func routeNeedsMore(route, call []string) (extra int, ok bool) {
	if len(call) == 0 || len(route) <= len(call) {
		return 0, false
	}
	if !tailMatches(route[:len(call)], call) {
		return 0, false
	}
	for _, s := range route[len(call):] {
		if s != "*" {
			return 0, false
		}
	}
	return len(route) - len(call), true
}

// callSuppliesMore reports that a call begins with a whole parameterised route
// and then carries further segments, and how many.
//
// GET /api/orders/acme/42 against GET /api/orders/{id}. The route has to END
// in a path variable for this to be about variables at all: /users/{id}/orders
// against /users/7/orders/9 is a sub-route nobody declared, and is reported as
// a call with no matching route.
func callSuppliesMore(route, call []string) (extra int, ok bool) {
	if len(route) == 0 || len(call) <= len(route) || route[len(route)-1] != "*" {
		return 0, false
	}
	if !tailMatches(route, call[:len(route)]) {
		return 0, false
	}
	return len(call) - len(route), true
}

// verbsOf lists the distinct verbs a set of routes accepts, as written.
func verbsOf(rs []routeCandidate) string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rs {
		v := strings.ToUpper(r.method)
		if v == "" {
			v = "ANY"
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return joinAnd(out)
}

func verbLabel(method string) string {
	if strings.TrimSpace(method) == "" {
		return "ANY"
	}
	return strings.ToUpper(method)
}

// pathOf renders canonical segments back as a path, for messages.
func pathOf(segs []string) string { return "/" + strings.Join(segs, "/") }

func quote(s string) string { return `"` + s + `"` }

// ------------------------------------------------------------------ members

// contractMembers builds the member section: the system graph's own status
// rows, plus what this report in particular could not read.
func (f *Federator) contractMembers(facts []*memberFacts, languages map[string]map[string]int) []ContractMember {
	statuses := f.memberStatuses(facts)
	out := make([]ContractMember, 0, len(statuses))
	for _, st := range statuses {
		cm := ContractMember{
			MemberStatus:   st,
			CallsReadIn:    []LanguageFiles{},
			CallsNotReadIn: []LanguageFiles{},
			RoutesReadIn:   []LanguageFiles{}, RoutesNotReadIn: []LanguageFiles{},
		}
		if st.Available {
			cm.Stale = st.IndexedUnix > 0 &&
				time.Duration(st.AgeHours*float64(time.Hour)) > f.staleAfter
			cm.CallsReadIn, cm.CallsNotReadIn = splitLanguages(languages[st.Name], clientCallLanguage)
			cm.RoutesReadIn, cm.RoutesNotReadIn = splitLanguages(languages[st.Name], routeLanguage)
			cm.Warnings = append(cm.Warnings, coverageWarnings(cm)...)
		}
		out = append(out, cm)
	}
	return out
}

// coverageWarnings turns a member's language split into the sentences a reader
// needs before trusting an empty row.
func coverageWarnings(m ContractMember) []string {
	var out []string
	if len(m.CallsNotReadIn) > 0 {
		s := fmt.Sprintf("outbound calls are not extracted from %s, so calls this member makes "+
			"from that code are invisible here: none of them can be checked, and a route only "+
			"they call is listed as uncalled", languageList(m.CallsNotReadIn))
		if len(m.CallsReadIn) == 0 {
			s += ". No language in this member has client-call extraction at all"
		}
		out = append(out, s)
	}
	if len(m.RoutesNotReadIn) > 0 {
		out = append(out, fmt.Sprintf("routes are not extracted from %s, so an endpoint this "+
			"member declares there is unknown to this report and a call into it reads as a "+
			"call with no matching route", languageList(m.RoutesNotReadIn)))
	}
	return out
}

// contractWeakeners lists why the route side of the comparison is not the
// whole system. Any entry costs every finding one tier.
//
// One tier for the lot rather than one per reason: three members being
// unreadable is not three times as unknown as one, and stacking would drive
// every finding to `low` and make the tiers stop distinguishing anything.
func contractWeakeners(members []ContractMember) []string {
	out := []string{}
	for _, m := range members {
		switch {
		case !m.Available:
			out = append(out, fmt.Sprintf("%s could not be read, so a route it declares may "+
				"answer a call reported here", m.Name))
			continue
		case m.Status != "" && m.Status != string(model.StatusReady):
			out = append(out, fmt.Sprintf("%s's index run is %s, so routes may be missing from "+
				"it rather than absent from the code", m.Name, m.Status))
		case m.IndexState != "" && m.IndexState != string(model.IndexStateComplete):
			out = append(out, fmt.Sprintf("%s's index is %s, so routes may be missing from it "+
				"rather than absent from the code", m.Name, m.IndexState))
		}
		if m.Stale {
			out = append(out, fmt.Sprintf("%s was indexed %.0f day(s) ago, so its routes and "+
				"calls are the ones it had then", m.Name, m.AgeHours/24))
		}
		if len(m.RoutesNotReadIn) > 0 {
			out = append(out, fmt.Sprintf("%s holds %s, which routes are not extracted from",
				m.Name, languageList(m.RoutesNotReadIn)))
		}
	}
	return out
}

func languageList(ls []LanguageFiles) string {
	parts := make([]string, 0, len(ls))
	for _, l := range ls {
		parts = append(parts, fmt.Sprintf("%s (%d file%s)", l.Language, l.Files, plural(l.Files, "", "s")))
	}
	return joinAnd(parts)
}

// contractCaveats are the limits of the method, printed whatever the data.
func (f *Federator) contractCaveats() []string {
	coverage := "This report was not told which client conventions and route frameworks the " +
		"indexer extracts; 'graphlume doctor' lists them."
	if len(f.extractedClients) > 0 || len(f.extractedRoutes) > 0 {
		coverage = fmt.Sprintf("Only what the indexer extracts is compared. Outbound calls: %s. "+
			"Routes: %s. A call made through any other client, a renamed client instance or a "+
			"generated SDK, and a route declared any other way (an OpenAPI document, a gateway "+
			"configuration, gRPC, GraphQL) are invisible, and their absence here is not evidence.",
			orNone(strings.Join(f.extractedClients, ", ")), orNone(strings.Join(f.extractedRoutes, ", ")))
	}
	return []string{
		"Matching is a HEURISTIC over two pieces of source text written by two teams. No " +
			"finding is ever `exact`, and none is above `high`: only a compiler index can make " +
			"`exact`, and none spans two repositories.",
		coverage,
		"Hosts are not resolved. The host of an absolute URL is dropped before matching " +
			"because no index records which member a hostname is, so a call to another " +
			"company's /v1/users can match a member's /v1/users, and a call with no matching " +
			"route may simply be leaving this system.",
		"Base paths are not read. A target that BEGINS with a run-time value (a configured base " +
			"URL) keeps none of its path in the index and is listed as not compared; a mount " +
			"point or gateway prefix is in no index at all, so agreement that depends on one is " +
			"reported as base_path_unverified at `low` instead of being assumed.",
		"Only the method, the path and the NUMBER of path segments are compared. The index " +
			"holds neither the query, header or body parameters a call sends nor the ones a " +
			"handler reads, so a renamed field or a missing query parameter is not detected.",
		"\"No indexed call reaches this route\" is the weakest statement here. A mobile app, a " +
			"cron job, a partner, a test, a repository absent from the member list, or a caller " +
			"in a language whose calls are not extracted would not appear.",
	}
}
