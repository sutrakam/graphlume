package mcp

// The CLI answers "what HTTP endpoints does this service expose", "which code
// reads and writes each table" and "where is this config key read" today, and
// before this file an agent over MCP could not ask any of them. Those are
// three of the things an agent most often rebuilds by grepping, which is
// exactly the cost this project exists to remove.
//
// Each handler is a thin query over the same Registry report the matching CLI
// command reads — EndpointReport for `graphlume api`, TableUsageReport for
// `graphlume db`, ConfigReport for `graphlume config` — plus an optional
// in-memory filter. No query logic is duplicated: the store call is the one
// internal/cli/apidb.go and internal/cli/configdeps.go already make.
//
// None of these need the call graph, so they register in registerTools()
// rather than registerCapabilities(): an index whose references were never
// resolved should still answer "what endpoints exist", exactly as
// overview and search already do.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/sutrakam/graphlume/internal/analyze/api"
	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/multirepo"
	"github.com/sutrakam/graphlume/internal/registry"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---------------------------------------------------------------- endpoints

type apiEndpointArgs struct {
	Method string `json:"method,omitempty" jsonschema:"Filter to one HTTP method, e.g. GET. Case-insensitive."`
	Query  string `json:"query,omitempty" jsonschema:"Keep only endpoints whose method and path, or handler name, contain this text, e.g. '/orders'. Case-insensitive."`
	Within string `json:"within,omitempty" jsonschema:"Keep only endpoints declared in this file or under this directory, e.g. 'services/billing/'."`
	pageArg
}

// pathWithin is EdgeFilter.Within for a path held in memory: within names a
// directory (a trailing slash, or any prefix ending at a path separator) or
// one file.
func pathWithin(path, within string) bool {
	w := strings.TrimPrefix(strings.TrimSpace(within), "./")
	if w == "" {
		return true
	}
	if strings.HasSuffix(w, "/") {
		return strings.HasPrefix(path, w)
	}
	return path == w || strings.HasPrefix(path, w+"/")
}

func (s *Server) findAPIEndpoints(ctx context.Context, req *sdk.CallToolRequest, args apiEndpointArgs) (*sdk.CallToolResult, *Response, error) {
	page, err := s.page(args.pageArg, "endpoint(s)")
	if err != nil {
		return failWith("invalid_argument", "Pass limit and offset as non-negative numbers, or omit them.", "%v", err)
	}
	runID, status, err := s.currentRun(ctx)
	if err != nil {
		return s.noRun(ctx, req, err)
	}

	endpoints, err := s.endpointReport(ctx, runID)
	if err != nil {
		return failFrom(err, "endpoint report: %v", err)
	}

	// Endpoints declared in test files are real routes in real code but are
	// NOT this service's API surface — the same split newAPICmd applies in
	// internal/cli/apidb.go, via the same capability.IsTestPath.
	var live []registry.Endpoint
	testFixtures := 0
	for _, e := range endpoints {
		if capability.IsTestPath(e.FilePath) {
			testFixtures++
			continue
		}
		live = append(live, e)
	}

	// One declaration can reach the report twice (a router file a second
	// mount re-reads, say) with the same route, file, line and handler. It is
	// one route: list it once, so a count never includes a copy.
	live, folded := dedupeEndpoints(live)

	if method := strings.ToUpper(strings.TrimSpace(args.Method)); method != "" {
		var filtered []registry.Endpoint
		for _, e := range live {
			if strings.HasPrefix(strings.ToUpper(e.Signature), method+" ") {
				filtered = append(filtered, e)
			}
		}
		live = filtered
	}
	if q, w := strings.ToLower(strings.TrimSpace(args.Query)), args.Within; q != "" || strings.TrimSpace(w) != "" {
		var filtered []registry.Endpoint
		for _, e := range live {
			if !pathWithin(e.FilePath, w) {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(e.Signature), q) && !strings.Contains(strings.ToLower(e.Handler), q) {
				continue
			}
			filtered = append(filtered, e)
		}
		live = filtered
	}

	resp := &Response{
		IndexRevision: runID,
		IndexStatus:   status,
		Summary: fmt.Sprintf("%d endpoint(s). Discovery covers %s.",
			len(live), strings.Join(frameworkNames(), ", ")),
	}

	unhandled := 0
	items := make([]Item, 0, len(live))
	for _, e := range live {
		why := "handler: " + e.Handler
		if e.HandlerPath != "" && e.HandlerPath != e.FilePath {
			why += " (" + e.HandlerPath + ")"
		}
		if e.Handler == "" {
			why = "no handler recorded: the route is real, but the framework's syntax did " +
				"not name the function"
			unhandled++
		}
		items = append(items, Item{
			Kind: "endpoint",
			Name: e.Signature,
			Path: e.FilePath,
			Line: e.Line,
			Why:  why,
		})
	}
	paginate(resp, page, items)

	if len(live) == 0 && (args.Query != "" || strings.TrimSpace(args.Within) != "") {
		resp.Notes = append(resp.Notes, "No endpoint matches the query and folder given. Call endpoints without them to see every route.")
	} else if len(live) == 0 {
		resp.Notes = append(resp.Notes, "No HTTP endpoints found. Route discovery covers "+
			strings.Join(frameworkNames(), ", ")+". A service defined through OpenAPI, "+
			"GraphQL, gRPC or gateway configuration will report nothing here — that is a "+
			"coverage limit, not a finding about the code.")
	}
	if unhandled > 0 {
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"%d endpoint(s) have no handler recorded.", unhandled))
	}
	if folded > 0 {
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"%d repeated listing(s) of the same route, file, line and handler were counted once.", folded))
	}
	if testFixtures > 0 {
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"%d further route(s) are declared in test files and excluded — they are fixtures "+
				"standing up a server for a test, not this service's API surface.", testFixtures))
	}
	return s.reply(ctx, req, resp)
}

// dedupeEndpoints keeps the first of each route listed more than once with
// the same signature, file, line and handler, in order, and says how many it
// dropped.
func dedupeEndpoints(in []registry.Endpoint) ([]registry.Endpoint, int) {
	type key struct {
		sig, file, handler, handlerPath string
		line                            int
	}
	seen := make(map[key]bool, len(in))
	out := in[:0:0]
	for _, e := range in {
		k := key{e.Signature, e.FilePath, e.Handler, e.HandlerPath, e.Line}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, e)
	}
	return out, len(in) - len(out)
}

// frameworkNames renders api.SupportedFrameworks() as strings, mirroring
// internal/cli.frameworkList without importing the cli package.
func frameworkNames() []string {
	fws := api.SupportedFrameworks()
	names := make([]string, 0, len(fws))
	for _, f := range fws {
		names = append(names, string(f))
	}
	return names
}

// ---------------------------------------------------------------- db access

type dbAccessArgs struct {
	Table  string `json:"table,omitempty" jsonschema:"Filter to one table name. Case-insensitive."`
	Query  string `json:"query,omitempty" jsonschema:"Keep only tables whose name contains this text, e.g. 'order'. Case-insensitive."`
	Within string `json:"within,omitempty" jsonschema:"Count only the reads and writes made by code in this file or under this directory, e.g. 'services/billing/'. A table no code there touches is left out."`
	pageArg
}

func (s *Server) findDBAccess(ctx context.Context, req *sdk.CallToolRequest, args dbAccessArgs) (*sdk.CallToolResult, *Response, error) {
	page, err := s.page(args.pageArg, "table(s)")
	if err != nil {
		return failWith("invalid_argument", "Pass limit and offset as non-negative numbers, or omit them.", "%v", err)
	}
	runID, status, err := s.currentRun(ctx)
	if err != nil {
		return s.noRun(ctx, req, err)
	}

	usage, err := s.deps.Registry.TableUsageReportWithin(ctx, runID, args.Within)
	if err != nil {
		return failFrom(err, "table usage report: %v", err)
	}

	table := strings.ToLower(strings.TrimSpace(args.Table))
	query := strings.ToLower(strings.TrimSpace(args.Query))
	if table != "" || query != "" {
		var filtered []registry.TableUsage
		for _, u := range usage {
			if table != "" && u.Table != table {
				continue
			}
			if query != "" && !strings.Contains(strings.ToLower(u.Table), query) {
				continue
			}
			filtered = append(filtered, u)
		}
		usage = filtered
	}

	resp := &Response{
		IndexRevision: runID,
		IndexStatus:   status,
		Summary:       fmt.Sprintf("%d table(s).", len(usage)),
	}

	var external, uncertain int
	items := make([]Item, 0, len(usage))
	for _, u := range usage {
		declared := "declared in this repository"
		if !u.Declared {
			declared = "used but not declared in this repository — most likely owned by " +
				"another service"
			external++
		}
		uncertain += u.Uncertain
		items = append(items, Item{
			Kind: "table",
			Name: u.Table,
			Why:  fmt.Sprintf("%d read(s), %d write(s); %s", u.Readers, u.Writers, declared),
		})
	}
	paginate(resp, page, items)

	if len(usage) == 0 {
		if query != "" || strings.TrimSpace(args.Within) != "" {
			resp.Notes = append(resp.Notes, "No table matches the query and folder given. Call tables without them to see every table.")
		} else if args.Table != "" {
			resp.Notes = append(resp.Notes, fmt.Sprintf(
				"No access recorded for table %q. SQL is recognised in .sql files and in "+
					"string literals in source; queries built by an ORM's fluent API, or "+
					"assembled far from where they run, are not visible to a static scan.",
				args.Table))
		} else {
			resp.Notes = append(resp.Notes,
				"No database access found. SQL is recognised in .sql files and in string "+
					"literals in source; queries built by an ORM's fluent API, or assembled "+
					"far from where they run, are not visible to a static scan — that is a "+
					"coverage limit, not a finding about the code.")
		}
	}
	if external > 0 {
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"%d table(s) are not declared by any migration here — most likely owned by "+
				"another service.", external))
	}
	if uncertain > 0 {
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"%d statement(s) were assembled at run time; the table is real, but whether that "+
				"exact statement executes as written is not something a static read can "+
				"establish.", uncertain))
	}
	return s.reply(ctx, req, resp)
}

// ---------------------------------------------------------------- config key

type configKeyArgs struct {
	Key    string `json:"key,omitempty" jsonschema:"The configuration key to look up, e.g. DATABASE_URL. Omit it to list every key, a page at a time."`
	Query  string `json:"query,omitempty" jsonschema:"Listing only: keep keys whose name contains this text. Case-insensitive."`
	Status string `json:"status,omitempty" jsonschema:"Listing only: keep keys in one state: undeclared (read, declared nowhere), unread (declared, read by no code) or declared (declared and read)."`
	pageArg
}

// Config key states in a listing.
const (
	configDeclared   = "declared"
	configUndeclared = "undeclared"
	configUnread     = "unread"
)

// configStatus is the state of one key: read and declared nowhere, declared
// and read by nothing, or both. A key a tool reads (GraphLume's own
// configuration, a Compose file) is never "unread": no source line reads it,
// by design.
func configStatus(k registry.ConfigKeyUsage) string {
	switch {
	case !k.Declared:
		return configUndeclared
	case k.Readers == 0 && k.ReadBy == "":
		return configUnread
	default:
		return configDeclared
	}
}

func (s *Server) getConfigKey(ctx context.Context, req *sdk.CallToolRequest, args configKeyArgs) (*sdk.CallToolResult, *Response, error) {
	if strings.TrimSpace(args.Key) == "" {
		return s.listConfigKeys(ctx, req, args)
	}
	runID, status, err := s.currentRun(ctx)
	if err != nil {
		return s.noRun(ctx, req, err)
	}

	keys, err := s.configReport(ctx, runID)
	if err != nil {
		return failFrom(err, "config report: %v", err)
	}

	var found *registry.ConfigKeyUsage
	for i := range keys {
		if strings.EqualFold(keys[i].Name, args.Key) {
			found = &keys[i]
			break
		}
	}

	resp := &Response{IndexRevision: runID, IndexStatus: status}
	if found == nil {
		resp.Summary = fmt.Sprintf("No configuration key named %q.", args.Key)
		resp.Notes = append(resp.Notes,
			"No such key is declared or read anywhere in the index. Declarations are read "+
				"from .env, YAML, JSON, TOML and .properties files; usage from os.Getenv, "+
				"@Value, process.env and similar lookups. Kubernetes, Helm, Terraform and CI "+
				"definitions are not read, so a key set only there will not appear here — "+
				"that is a coverage limit, not proof the key is unused.")
		return s.reply(ctx, req, resp)
	}

	where := "not declared anywhere in this repository"
	if found.Declared {
		where = fmt.Sprintf("declared at %s:%d", found.FilePath, found.Line)
	}
	resp.Summary = fmt.Sprintf("%s: %d reader(s), %s.", found.Name, found.Readers, where)
	resp.Items = append(resp.Items, Item{
		Kind: "config_key",
		Name: found.Name,
		Path: found.FilePath,
		Line: found.Line,
		Why:  fmt.Sprintf("%d reader(s); %s", found.Readers, where),
	})

	if !found.Declared {
		resp.Notes = append(resp.Notes,
			"READ BUT NOT DECLARED: a service that needs this key and finds nothing declaring "+
				"it fails at start-up.")
	} else if found.Readers == 0 {
		resp.Notes = append(resp.Notes,
			"Declared and never read by any code this scan recognises — either dead "+
				"configuration or a lookup this scan cannot see.")
	}
	if found.Secret {
		resp.Notes = append(resp.Notes,
			"This key's NAME indicates a credential. No configuration VALUE was read to "+
				"produce this report — the response has no field that could hold one.")
	}
	return s.reply(ctx, req, resp)
}

// listConfigKeys is config without a key: every key the index knows, a page
// at a time, keys read but declared nowhere first.
func (s *Server) listConfigKeys(ctx context.Context, req *sdk.CallToolRequest, args configKeyArgs) (*sdk.CallToolResult, *Response, error) {
	want := strings.ToLower(strings.TrimSpace(args.Status))
	switch want {
	case "", configDeclared, configUndeclared, configUnread:
	default:
		return failWith("invalid_argument",
			"Pass status as declared, undeclared or unread, or omit it.",
			"unknown status %q", clipRunes(args.Status, 40))
	}
	page, err := s.page(args.pageArg, "configuration key(s)")
	if err != nil {
		return failWith("invalid_argument", "Pass limit and offset as non-negative numbers, or omit them.", "%v", err)
	}
	runID, status, err := s.currentRun(ctx)
	if err != nil {
		return s.noRun(ctx, req, err)
	}
	keys, err := s.configReport(ctx, runID)
	if err != nil {
		return failFrom(err, "config report: %v", err)
	}

	q := strings.ToLower(strings.TrimSpace(args.Query))
	counts := map[string]int{}
	items := make([]Item, 0, min(len(keys), 1024))
	for _, k := range keys {
		st := configStatus(k)
		if q != "" && !strings.Contains(strings.ToLower(k.Name), q) {
			continue
		}
		counts[st]++
		if want != "" && st != want {
			continue
		}
		var why string
		switch st {
		case configUndeclared:
			why = fmt.Sprintf("%s: %d read(s), declared nowhere; read at %s:%d", st, k.Readers, k.FilePath, k.Line)
		case configUnread:
			why = fmt.Sprintf("%s: declared in %s:%d, read by no code this scan recognises", st, k.FilePath, k.Line)
		default:
			why = fmt.Sprintf("%s: %d read(s); declared in %s:%d", st, k.Readers, k.FilePath, k.Line)
			if k.ReadBy != "" && k.Readers == 0 {
				why = fmt.Sprintf("%s: declared in %s:%d, read by %s rather than by code", st, k.FilePath, k.Line, k.ReadBy)
			}
		}
		if k.Secret {
			why += "; name indicates a credential"
		}
		items = append(items, Item{Kind: "config_key", Name: k.Name, Path: k.FilePath, Line: k.Line, Why: why})
	}

	resp := &Response{
		IndexRevision: runID,
		IndexStatus:   status,
		Summary: fmt.Sprintf("%d configuration key(s): %d read but declared nowhere, %d declared and never read, %d declared and read.",
			counts[configUndeclared]+counts[configUnread]+counts[configDeclared],
			counts[configUndeclared], counts[configUnread], counts[configDeclared]),
	}
	if want != "" {
		resp.Summary += fmt.Sprintf(" Listing the %d %s.", len(items), want)
	}
	paginate(resp, page, items)
	if len(items) == 0 {
		resp.Notes = append(resp.Notes,
			"No configuration key matches. Declarations are read from .env, YAML, JSON, TOML and "+
				".properties files; usage from os.Getenv, @Value, process.env and similar lookups. "+
				"Kubernetes, Helm, Terraform and CI definitions are not read.")
	}
	if counts[configUndeclared] > 0 {
		resp.Notes = append(resp.Notes,
			"READ BUT NOT DECLARED keys come first: a service that needs one and finds nothing declaring it fails at start-up. "+
				"A key set only in Kubernetes, Helm, Terraform or CI is reported undeclared, which is a coverage limit.")
	}
	resp.Notes = append(resp.Notes,
		"VALUES ARE NEVER READ: no field in this answer can hold one. Pass key to look one key up.")
	return s.reply(ctx, req, resp)
}

// ---------------------------------------------------------------- projects

type listProjectsArgs struct {
	Query string `json:"query,omitempty" jsonschema:"Filter projects by name or path."`
}

func (s *Server) listProjects(ctx context.Context, req *sdk.CallToolRequest, args listProjectsArgs) (*sdk.CallToolResult, *Response, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return failFrom(err, "resolve user home: %v", err)
	}

	reg, err := multirepo.LoadGlobalRegistry(home)
	if err != nil {
		return failFrom(err, "load global registry: %v", err)
	}

	resp := &Response{}
	if len(reg.Projects) == 0 {
		resp.Summary = "No projects are registered in GraphLume global registry yet."
		resp.Notes = append(resp.Notes, "Run 'graphlume init' or 'graphlume setup' in your project root to register it.")
		return s.reply(ctx, req, resp)
	}

	current := multirepo.CanonicalPath(s.deps.Config.RepoRoot)
	projects := reg.Projects
	if !s.deps.ListAllProjects {
		// Not a local stdio session: the clients may be other people, so
		// this machine's other repositories stay private.
		projects = nil
		for _, p := range reg.Projects {
			if multirepo.CanonicalPath(p.RootPath) == current {
				projects = append(projects, p)
			}
		}
		resp.Notes = append(resp.Notes,
			"Only the project this server answers for is listed; other registered projects are shown to a local stdio session only.")
	}
	resp.Summary = fmt.Sprintf("Found %d registered project(s) in GraphLume global registry.", len(projects))
	q := strings.ToLower(strings.TrimSpace(args.Query))
	for _, p := range projects {
		if q != "" && !strings.Contains(strings.ToLower(p.ID), q) && !strings.Contains(strings.ToLower(p.RootPath), q) {
			continue
		}
		status := "ok"
		if p.LastIndexedAt == "" {
			status = "not_indexed"
		}
		// The display name comes from the project's own .graphlume.yml:
		// untrusted text headed for an agent's context, so it is bounded
		// and stripped of control characters.
		name := p.ID
		if n := safeDisplayName(p.Name); n != "" && n != p.ID {
			name = p.ID + " (" + n + ")"
		}
		resp.Items = append(resp.Items, Item{
			Kind: "project",
			Name: name,
			Path: p.RootPath,
			Why:  fmt.Sprintf("mode: %s, status: %s, languages: %s", p.Mode, status, strings.Join(p.Languages, ",")),
		})
	}
	return s.reply(ctx, req, resp)
}

// safeDisplayName bounds a repository-supplied name for an agent's context:
// printable characters only, at most 64 of them.
func safeDisplayName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= 64 {
			break
		}
		if unicode.IsPrint(r) {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}
