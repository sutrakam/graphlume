package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type findMentionsArgs struct {
	Name  string `json:"name" jsonschema:"Symbol name to find documentation and ADR mentions for."`
	Path  string `json:"path,omitempty" jsonschema:"File path to disambiguate symbol."`
	Line  int    `json:"line,omitempty" jsonschema:"Line number to disambiguate symbol."`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum mentions to return (default 20)."`
}

func (s *Server) findMentions(ctx context.Context, req *sdk.CallToolRequest, args findMentionsArgs) (*sdk.CallToolResult, *Response, error) {
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return failWith("invalid_argument",
			"Specify a symbol name to find documentation mentions for.",
			"name is required")
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 20
	} else if limit > 100 {
		limit = 100
	}

	q := symbolQuery{
		Tool:      "find_mentions",
		Param:     "name",
		PathParam: "path",
		LineParam: "line",
		Name:      name,
		Path:      args.Path,
		Line:      args.Line,
	}
	sel, sc, scResp := s.selectSymbol(ctx, req, q)
	if sc != nil {
		return sc, scResp, nil
	}

	runID := sel.RunID
	target := sel.Target

	filter := registry.EdgeFilter{
		Kinds: []string{string(model.EdgeDocuments), "documents"},
	}
	edges, err := s.deps.Registry.IncomingEdges(ctx, runID, target.ID, limit, filter)
	if err != nil {
		return failFrom(err, "find mentions: %v", err)
	}

	resp := &Response{
		IndexRevision: runID,
		IndexStatus:   sel.Status,
	}

	for _, e := range edges {
		excerpt, _ := s.deps.Registry.FileExcerpt(ctx, runID, e.EvidenceFile, max(1, e.EvidenceLine-1), 3)
		resp.Items = append(resp.Items, Item{
			Kind:       "documentation",
			Name:       target.Name,
			Path:       e.EvidenceFile,
			Line:       e.EvidenceLine,
			Confidence: e.Confidence,
			Source:     e.Source,
			Excerpt:    excerpt,
			Why:        fmt.Sprintf("documented in %s:%d", e.EvidenceFile, e.EvidenceLine),
		})
	}

	if len(resp.Items) == 0 {
		resp.Summary = fmt.Sprintf("No documentation or ADR mentions found for %s (%s:%d).", target.Name, target.FilePath, target.StartLine)
		resp.Notes = append(resp.Notes, "Doclink indexing records backticked references in markdown, ADR, and documentation files.")
	} else {
		resp.Summary = fmt.Sprintf("Found %d documentation mention(s) for %s (%s:%d).", len(resp.Items), target.Name, target.FilePath, target.StartLine)
	}

	return s.reply(ctx, req, resp)
}
