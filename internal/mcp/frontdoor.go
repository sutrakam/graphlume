package mcp

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type frontdoorArgs struct {
	Action string `json:"action" jsonschema:"The code intelligence action to execute: 'explore' (single-turn deep dive on a symbol with code, callers, callees, blast-radius and routes), 'context' (task-oriented context pack), 'search' (code search), 'symbol' (find/get symbol definitions), 'callers' (find upstream callers), 'callees' (find downstream callees), 'references' (find usage references), 'impact' (blast-radius impact analysis), 'tests' (find tests covering a symbol), 'diff' (compress git diff), 'overview' (project overview), 'query' (read-only declarative graph query), 'estate' (multi-repository links, callers or impact; only when the server has an estate configured; put links, callers or impact in kind and member:symbol in name)."`

	Query   string `json:"query,omitempty" jsonschema:"Search query or natural language task description."`
	Project string `json:"project,omitempty" jsonschema:"Project identifier or repository name for multi-project workspaces."`
	Name    string `json:"name,omitempty" jsonschema:"Symbol name to look up or analyze."`
	Path    string `json:"path,omitempty" jsonschema:"File path to disambiguate or scope the query."`
	Line    int    `json:"line,omitempty" jsonschema:"1-based line number for precise symbol selection."`

	Diff    string `json:"diff,omitempty" jsonschema:"Unified git diff text for impact or diff compression."`
	Content string `json:"content,omitempty" jsonschema:"Proposed file content for speculative dryrun patch analysis."`
	Within  string `json:"within,omitempty" jsonschema:"Path filter for call sites in callers or references (e.g. 'src/')."`
	Kind    string `json:"kind,omitempty" jsonschema:"Optional symbol kind filter: class, interface, function, method, field, constant."`

	MinConfidence string   `json:"min_confidence,omitempty" jsonschema:"Confidence floor: exact, high, medium, or low."`
	Limit         int      `json:"limit,omitempty" jsonschema:"Maximum items to return."`
	Offset        int      `json:"offset,omitempty" jsonschema:"Pagination offset."`
	DryRun        bool     `json:"dry_run,omitempty" jsonschema:"If true, returns preview and diff without modifying files on disk."`
	Via           string   `json:"via,omitempty" jsonschema:"Intermediate symbol for path searches."`
	Exclude       []string `json:"exclude,omitempty" jsonschema:"Symbol names to exclude from path searches."`
	MaxDepth      int      `json:"max_depth,omitempty" jsonschema:"Maximum hops for path searches (default 6)."`
}

func (s *Server) registerFrontdoor(allowed map[string]bool) {
	if allowed == nil || !allowed["graphlume"] {
		return
	}
	descs := toolDescriptions()
	addTool(s, allowed, &sdk.Tool{
		Name:        "graphlume",
		Description: descs["graphlume"],
	}, s.graphlumeFrontdoor)
}

func (s *Server) graphlumeFrontdoor(ctx context.Context, req *sdk.CallToolRequest, args frontdoorArgs) (*sdk.CallToolResult, *Response, error) {
	action := strings.ToLower(strings.TrimSpace(args.Action))
	if action == "" {
		return failWith("invalid_argument",
			"Specify an action: 'explore', 'context', 'search', 'symbol', 'callers', 'callees', 'references', 'impact', 'tests', 'diff', 'overview', 'query', 'outline', 'endpoints', 'tables', 'blindspots', 'plan', 'rename', 'config', or 'patch'.",
			"action is required")
	}

	switch action {
	case "explore", "inspect":
		name := args.Name
		if name == "" {
			name = args.Query
		}
		return s.exploreSymbol(ctx, req, exploreArgs{
			Name:             name,
			Path:             args.Path,
			Line:             args.Line,
			minConfidenceArg: minConfidenceArg{MinConfidence: args.MinConfidence},
			Limit:            args.Limit,
		})

	case "context", "task", "task_context":
		if s.deps.Graph == nil {
			return failWith("unsupported",
				"Resolve the project graph first with 'graphlume resolve' to enable task context.",
				"context requires resolved graph")
		}
		task := args.Query
		if task == "" {
			task = args.Name
		}
		return s.getTaskContext(ctx, req, taskContextArgs{
			Task:             task,
			MaxFiles:         args.Limit,
			minConfidenceArg: minConfidenceArg{MinConfidence: args.MinConfidence},
		})

	case "search":
		q := args.Query
		if q == "" {
			q = args.Name
		}
		return s.searchCode(ctx, req, searchArgs{
			Query: q,
			Limit: args.Limit,
		})

	case "symbol", "definition":
		name := args.Name
		if name == "" {
			name = args.Query
		}
		if args.Path != "" || args.Line > 0 {
			return s.getSymbol(ctx, req, symbolRefArgs{
				Name:             name,
				Path:             args.Path,
				lineArg:          lineArg{Line: args.Line},
				minConfidenceArg: minConfidenceArg{MinConfidence: args.MinConfidence},
			})
		}
		return s.findSymbol(ctx, req, symbolArgs{
			Name:             name,
			Kind:             args.Kind,
			minConfidenceArg: minConfidenceArg{MinConfidence: args.MinConfidence},
		})

	case "callers":
		name := args.Name
		if name == "" {
			name = args.Query
		}
		return s.findCallers(ctx, req, siteArgs{
			relationArgs: relationArgs{
				Name:             name,
				Path:             args.Path,
				lineArg:          lineArg{Line: args.Line},
				minConfidenceArg: minConfidenceArg{MinConfidence: args.MinConfidence},
				pageArg:          pageArg{Limit: args.Limit, Offset: args.Offset},
			},
			Within: args.Within,
		})

	case "callees":
		name := args.Name
		if name == "" {
			name = args.Query
		}
		return s.findCallees(ctx, req, relationArgs{
			Name:             name,
			Path:             args.Path,
			lineArg:          lineArg{Line: args.Line},
			minConfidenceArg: minConfidenceArg{MinConfidence: args.MinConfidence},
			pageArg:          pageArg{Limit: args.Limit, Offset: args.Offset},
		})

	case "references", "refs":
		name := args.Name
		if name == "" {
			name = args.Query
		}
		return s.findReferences(ctx, req, siteArgs{
			relationArgs: relationArgs{
				Name:             name,
				Path:             args.Path,
				lineArg:          lineArg{Line: args.Line},
				minConfidenceArg: minConfidenceArg{MinConfidence: args.MinConfidence},
				pageArg:          pageArg{Limit: args.Limit, Offset: args.Offset},
			},
			Within: args.Within,
		})

	case "impact":
		if s.deps.Graph == nil {
			return failWith("unsupported",
				"Resolve the project graph first with 'graphlume resolve' to enable impact analysis.",
				"impact requires resolved graph")
		}
		name := args.Name
		if name == "" {
			name = args.Query
		}
		return s.impactAnalysis(ctx, req, impactArgs{
			Symbol:        name,
			Path:          args.Path,
			lineArg:       lineArg{Line: args.Line},
			MinConfidence: args.MinConfidence,
			pageArg:       pageArg{Limit: args.Limit, Offset: args.Offset},
		})

	case "tests":
		if s.deps.Graph == nil {
			return failWith("unsupported",
				"Resolve the project graph first with 'graphlume resolve' to enable test discovery.",
				"tests requires resolved graph")
		}
		name := args.Name
		if name == "" {
			name = args.Query
		}
		return s.findTests(ctx, req, testsArgs{
			targetArgs: targetArgs{
				Symbol:           name,
				Path:             args.Path,
				lineArg:          lineArg{Line: args.Line},
				minConfidenceArg: minConfidenceArg{MinConfidence: args.MinConfidence},
				pageArg:          pageArg{Limit: args.Limit, Offset: args.Offset},
			},
		})

	case "diff":
		return s.minimizeDiff(ctx, req, minimizeDiffArgs{
			Diff: args.Diff,
		})

	case "overview", "project_overview":
		return s.getProjectOverview(ctx, req, emptyArgs{})

	case "query", "sql", "graph_query", "query_graph", "graph":
		q := args.Query
		if q == "" {
			q = args.Name
		}
		return s.searchGraph(ctx, req, searchGraphArgs{
			Query: q,
			Limit: args.Limit,
		})

	case "outline":
		return s.getFileOutline(ctx, req, fileOutlineArgs{
			Path: args.Path,
		})

	case "endpoints", "api_endpoints":
		return s.findAPIEndpoints(ctx, req, apiEndpointArgs{
			Method:  args.Kind, // can pass method e.g. GET
			pageArg: pageArg{Limit: args.Limit, Offset: args.Offset},
		})

	case "tables", "db_tables", "database":
		tbl := args.Name
		if tbl == "" {
			tbl = args.Query
		}
		return s.findDBAccess(ctx, req, dbAccessArgs{
			Table:   tbl,
			pageArg: pageArg{Limit: args.Limit, Offset: args.Offset},
		})

	case "estate":
		if s.deps.Estate == nil {
			return failWith("unsupported",
				"Start the server with 'graphlume serve --estate <file>', or set estate: in your user config.",
				"no estate is configured for this server")
		}
		sub := strings.ToLower(strings.TrimSpace(args.Kind))
		if sub == "" {
			sub = estateLinks
		}
		symbol := args.Name
		if symbol == "" {
			symbol = args.Query
		}
		return s.estateTool(ctx, req, estateArgs{
			Action:        sub,
			Symbol:        symbol,
			Path:          args.Path,
			Depth:         args.MaxDepth,
			MinConfidence: args.MinConfidence,
			pageArg:       pageArg{Limit: args.Limit, Offset: args.Offset},
		})

	case "projects", "list_projects":
		return s.listProjects(ctx, req, listProjectsArgs{
			Query: args.Query,
		})

	case "blindspots", "untested":
		return s.detectBlindspots(ctx, req, blindspotArgs{})

	case "plan", "change_plan":
		name := args.Name
		if name == "" {
			name = args.Query
		}
		return s.planChange(ctx, req, targetArgs{
			Symbol:           name,
			Path:             args.Path,
			lineArg:          lineArg{Line: args.Line},
			minConfidenceArg: minConfidenceArg{MinConfidence: args.MinConfidence},
			pageArg:          pageArg{Limit: args.Limit, Offset: args.Offset},
		})

	case "rename":
		name := args.Name
		if name == "" {
			name = args.Query
		}
		return s.planRename(ctx, req, renameArgs{
			Name:             name,
			NewName:          args.Query,
			Path:             args.Path,
			lineArg:          lineArg{Line: args.Line},
			minConfidenceArg: minConfidenceArg{MinConfidence: args.MinConfidence},
		})

	case "config", "config_key":
		key := args.Name
		if key == "" {
			key = args.Query
		}
		return s.getConfigKey(ctx, req, configKeyArgs{
			Key: key,
		})

	case "patch", "dryrun":
		content := args.Content
		if content == "" {
			content = args.Diff
		}
		return s.analyzePatch(ctx, req, patchAnalysisArgs{
			Path:    args.Path,
			Content: content,
		})

	case "replace_body", "replace_symbol_body":
		name := args.Name
		if name == "" {
			name = args.Query
		}
		dryRun := args.DryRun || !s.deps.Config.AllowEdits
		return s.replaceSymbolBody(ctx, req, replaceSymbolBodyArgs{
			Name:    name,
			Path:    args.Path,
			Line:    args.Line,
			NewBody: args.Content,
			DryRun:  dryRun,
		})

	case "insert_after", "insert_after_symbol":
		name := args.Name
		if name == "" {
			name = args.Query
		}
		dryRun := args.DryRun || !s.deps.Config.AllowEdits
		return s.insertAfterSymbol(ctx, req, insertAfterSymbolArgs{
			Name:   name,
			Path:   args.Path,
			Line:   args.Line,
			Code:   args.Content,
			DryRun: dryRun,
		})

	case "safe_delete", "safe_delete_symbol":
		name := args.Name
		if name == "" {
			name = args.Query
		}
		dryRun := args.DryRun || !s.deps.Config.AllowEdits
		return s.safeDeleteSymbol(ctx, req, safeDeleteSymbolArgs{
			Name:   name,
			Path:   args.Path,
			Line:   args.Line,
			DryRun: dryRun,
		})

	case "paths", "find_paths", "trace", "find_paths_matching":
		from := args.Name
		to := args.Query
		depth := args.MaxDepth
		if depth <= 0 && args.Limit > 0 {
			depth = args.Limit
		}
		var kinds []string
		if args.Kind != "" {
			kinds = []string{args.Kind}
		}
		return s.traceCallPath(ctx, req, pathArgs{
			From:             from,
			To:               to,
			FromPath:         args.Path,
			FromLine:         args.Line,
			MaxDepth:         depth,
			Via:              args.Via,
			Exclude:          args.Exclude,
			Kinds:            kinds,
			minConfidenceArg: minConfidenceArg{MinConfidence: args.MinConfidence},
		})

	case "mentions", "find_mentions", "doc_mentions":
		name := args.Name
		if name == "" {
			name = args.Query
		}
		return s.findMentions(ctx, req, findMentionsArgs{
			Name:  name,
			Path:  args.Path,
			Line:  args.Line,
			Limit: args.Limit,
		})

	default:
		return failWith("invalid_argument",
			fmt.Sprintf("Unknown action %q. Supported actions: 'explore', 'context', 'search', 'symbol', 'callers', 'callees', 'references', 'impact', 'tests', 'diff', 'overview', 'query', 'outline', 'endpoints', 'tables', 'blindspots', 'plan', 'rename', 'config', 'patch', 'replace_body', 'insert_after', 'safe_delete', 'paths', 'mentions', 'estate'.", action),
			"unknown action: %s", action)
	}
}
