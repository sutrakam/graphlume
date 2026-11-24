package mcp

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/capability/impact"
	"github.com/sutrakam/graphlume/internal/capability/tests"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type exploreArgs struct {
	Name string `json:"name" jsonschema:"Symbol name to explore."`
	Path string `json:"path,omitempty" jsonschema:"File path to disambiguate symbol."`
	Line int    `json:"line,omitempty" jsonschema:"1-based line number for precise symbol selection."`
	minConfidenceArg
	Limit int `json:"limit,omitempty" jsonschema:"Maximum callers/callees to return (default 10)."`
}

func (s *Server) registerExplore(allowed map[string]bool) {
	descs := toolDescriptions()
	desc, ok := descs["explore"]
	if !ok {
		desc = "Deep-dive on a symbol in one call: returns its definition with verbatim line-numbered source excerpt, upstream callers (with confidence tiers: 'exact' vs 'heuristic'), downstream callees, blast-radius impact summary, and linked HTTP routes. An empty callers result is ambiguous — confirm with search before assuming a symbol is dead."
	}
	addTool(s, allowed, &sdk.Tool{
		Name:        "explore",
		Description: desc,
	}, s.exploreTool)
}

func (s *Server) exploreTool(ctx context.Context, req *sdk.CallToolRequest, args exploreArgs) (*sdk.CallToolResult, *Response, error) {
	return s.exploreSymbol(ctx, req, args)
}

func (s *Server) exploreSymbol(ctx context.Context, req *sdk.CallToolRequest, args exploreArgs) (*sdk.CallToolResult, *Response, error) {
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return failWith("invalid_argument",
			"Pass a non-empty symbol name to explore.",
			"name must not be empty")
	}

	minConf, err := parseMinConfidence(args.MinConfidence)
	if err != nil {
		return failFrom(err, "%v", err)
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	q := symbolQuery{
		Tool:       "explore",
		Param:      "name",
		PathParam:  "path",
		LineParam:  "line",
		Name:       name,
		Path:       args.Path,
		Line:       args.Line,
		Prefer:     callableKinds,
		PreferWord: "functions and methods",
		PreferNoun: "function or method",
		Tolerate:   typeKinds,
	}

	sel, sc, scResp := s.selectSymbol(ctx, req, q)
	if sc != nil {
		return sc, scResp, nil
	}
	runID, status, target := sel.RunID, sel.Status, sel.Target

	// 1. Definition excerpt.
	excerpt := s.symbolExcerpt(ctx, runID, target.SymbolHit)

	// 2. Upstream callers.
	callerFilter := registry.EdgeFilter{MinConfidence: minConf, Kinds: []string{string(model.EdgeCalls)}}
	callerEdges, callerTotal, _ := s.deps.Registry.EdgePage(ctx, runID, target.ID, true, limit, 0, callerFilter)

	// 3. Downstream callees.
	calleeFilter := registry.EdgeFilter{MinConfidence: minConf, Kinds: []string{string(model.EdgeCalls)}}
	calleeEdges, calleeTotal, _ := s.deps.Registry.EdgePage(ctx, runID, target.ID, false, limit, 0, calleeFilter)

	// Compute architectural centrality tier (hub, connector, leaf) based on in-degree and call topology.
	centralityTier := "leaf"
	centralityDesc := fmt.Sprintf("Leaf / helper symbol (in-degree: %d, out-degree: %d)", callerTotal, calleeTotal)
	if callerTotal >= 15 {
		centralityTier = "hub"
		centralityDesc = fmt.Sprintf("Architectural Hub: high centrality, widely referenced across the codebase (in-degree: %d, out-degree: %d); changes carry high blast-radius", callerTotal, calleeTotal)
	} else if callerTotal >= 3 {
		centralityTier = "connector"
		centralityDesc = fmt.Sprintf("Subsystem Connector (in-degree: %d, out-degree: %d)", callerTotal, calleeTotal)
	}

	resp := &Response{
		IndexRevision: runID,
		IndexStatus:   status,
		Summary: fmt.Sprintf("Explored %s (%s) [%s] at %s:%d: %d caller(s), %d callee(s) recorded.",
			target.Name, target.Kind, centralityTier, target.FilePath, target.StartLine, callerTotal, calleeTotal),
	}

	// Attach target definition item first.
	resp.Items = append(resp.Items, Item{
		Kind:       target.Kind,
		Name:       target.Name,
		FQN:        target.FQN,
		Path:       target.FilePath,
		Line:       target.StartLine,
		Confidence: target.Confidence,
		Source:     target.Source,
		Why:        "definition",
		Excerpt:    excerpt,
	})

	// Upstream callers.
	for _, e := range callerEdges {
		resp.Items = append(resp.Items, Item{
			Kind:       "caller",
			Name:       sourceName(e),
			Path:       e.EvidenceFile,
			Line:       e.EvidenceLine,
			Confidence: e.Confidence,
			Source:     e.Source,
			Why:        fmt.Sprintf("calls %s (%s)", target.Name, e.Confidence),
		})
	}

	// Downstream callees.
	for _, e := range calleeEdges {
		resp.Items = append(resp.Items, Item{
			Kind:       "callee",
			Name:       e.DstName,
			Path:       e.EvidenceFile,
			Line:       e.EvidenceLine,
			Confidence: e.Confidence,
			Source:     e.Source,
			Why:        fmt.Sprintf("called by %s (%s)", target.Name, e.Confidence),
		})
	}

	// 4. Impact / blast-radius summary if graph is present.
	if s.deps.Graph != nil {
		rep, err := impact.AnalyzeTarget(ctx, s.capDeps, runID, capability.TargetFromCandidate(target.Name, target),
			impact.Options{MinConfidence: minConf, HubDegreeLimit: 50, CheckValueUse: false})
		if err == nil {
			_, reach := impactItems(rep)
			resp.Items = append(resp.Items, Item{
				Kind: "impact",
				Name: reach.summary(),
				Path: target.FilePath,
				Line: target.StartLine,
				Why:  "transitive impact",
			})
		}
	}

	// 5. Associated HTTP route if applicable.
	if endpoints, err := s.deps.Registry.EndpointReport(ctx, runID); err == nil {
		for _, ep := range endpoints {
			if ep.FilePath == target.FilePath && (ep.Handler == target.Name || ep.HandlerID == target.ID || (ep.Line >= target.StartLine && ep.Line <= target.EndLine)) {
				resp.Items = append(resp.Items, Item{
					Kind: "endpoint",
					Name: ep.Signature,
					Path: ep.FilePath,
					Line: ep.Line,
					Why:  fmt.Sprintf("HTTP endpoint handled by %s", target.Name),
				})
				break
			}
		}
	}

	// Covering tests, so "understand this symbol" is one call rather than
	// explore + tests. Bounded (depth 3, five rows): explore is a
	// summary, and tests is the full answer with its coverage caveats.
	if rep, terr := tests.FindTestsForTarget(ctx, s.capDeps, runID,
		capability.TargetFromCandidate(target.Name, target), tests.Options{MaxDepth: 3}); terr == nil {
		runnable, _, _ := splitRunnable(rep.Tests)
		for i, t := range runnable {
			if i >= exploreMaxTests {
				resp.Notes = append(resp.Notes, fmt.Sprintf(
					"%d more test(s) reach %s; tests lists them all.", len(runnable)-exploreMaxTests, target.Name))
				break
			}
			resp.Items = append(resp.Items, Item{
				Kind: "test", Name: t.Name, Path: t.FilePath,
				Confidence: string(t.Confidence),
				Why:        fmt.Sprintf("test reaching %s (%s)", target.Name, t.Strongest),
			})
		}
		if len(runnable) == 0 {
			resp.Notes = append(resp.Notes, fmt.Sprintf(
				"No test was found reaching %s within 3 calls; tests says whether that is a real absence.", target.Name))
		}
	}

	// Apply coverage gate.
	s.gate(ctx, resp, sel, "explore", aboutCallers)

	resp.Notes = append(resp.Notes, fmt.Sprintf("Centrality: %s.", centralityDesc))
	resp.Notes = append(resp.Notes, fmt.Sprintf("Visual Deep-Link: View interactive blast-radius at http://127.0.0.1:7777/#/s/%s via 'graphlume ui'.", url.PathEscape(target.Name)))

	if callerTotal == 0 {
		resp.Notes = append(resp.Notes,
			"No callers found in index. Note that an empty caller result is ambiguous: unindexed callers, dynamic dispatch, or reflection may exist.")
	}
	if note := s.markedNote(ctx, runID, target.SymbolHit); note != "" {
		resp.Notes = append(resp.Notes, note)
	}
	if note := s.docNote(ctx, runID, target.SymbolHit); note != "" {
		resp.Notes = append(resp.Notes, note)
	}

	return s.reply(ctx, req, resp)
}

// exploreMaxTests bounds the covering tests explore lists.
const exploreMaxTests = 5
