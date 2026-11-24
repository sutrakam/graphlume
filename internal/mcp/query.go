package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type searchGraphArgs struct {
	Query string `json:"query" jsonschema:"A read-only SQL query (SELECT or WITH) against index tables: symbols, edges, files, file_edges (edges summed per src_file_id, dst_file_id, kind, confidence; count n), file_symbols (definitions per file_id; count n), references, http_routes, data_access."`
	Limit int    `json:"limit,omitempty" jsonschema:"Maximum rows to return (default: 50, maximum: 200)."`
}

func (s *Server) registerSearchGraph(allowed map[string]bool) {
	if allowed != nil && !allowed["graph"] {
		return
	}
	descs := toolDescriptions()
	addTool(s, allowed, &sdk.Tool{
		Name:        "graph",
		Description: descs["graph"],
	}, s.searchGraph)
}

func (s *Server) searchGraph(ctx context.Context, req *sdk.CallToolRequest, args searchGraphArgs) (*sdk.CallToolResult, *Response, error) {
	if s.deps.Registry == nil {
		return failWith("unsupported", "No index database available. Run 'graphlume index' first.", "registry is nil")
	}

	q := strings.TrimSpace(args.Query)
	if q == "" {
		return failWith("invalid_argument", "query is required", "empty query")
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 50
	}

	res, err := s.deps.Registry.QueryGraph(ctx, q, limit)
	if err != nil {
		return failWith("bad_request", fmt.Sprintf("Query rejected or failed: %v", err), "query error: %s", err.Error())
	}

	// Render Markdown table representation
	var b strings.Builder
	fmt.Fprintf(&b, "Query returned %d row(s)", res.TotalRows)
	if res.Truncated {
		fmt.Fprintf(&b, " (capped at limit %d)", limit)
	}
	b.WriteString(":\n\n")

	// Table header
	b.WriteString("| ")
	b.WriteString(strings.Join(res.Columns, " | "))
	b.WriteString(" |\n|")
	for range res.Columns {
		b.WriteString("---|")
	}
	b.WriteString("\n")

	// Table rows
	for _, row := range res.Rows {
		b.WriteString("| ")
		rowStrs := make([]string, len(row))
		for i, val := range row {
			if val == nil {
				rowStrs[i] = "NULL"
			} else {
				rowStrs[i] = fmt.Sprintf("%v", val)
			}
		}
		b.WriteString(strings.Join(rowStrs, " | "))
		b.WriteString(" |\n")
	}

	rawJSON, _ := json.Marshal(res)
	resp := &Response{
		Summary: fmt.Sprintf("%d row(s) returned by query", res.TotalRows),
		Notes: []string{
			fmt.Sprintf("Query executed against index database: %s", q),
		},
		Truncated: res.Truncated,
	}

	return &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: b.String()}},
		Meta: map[string]any{
			"structured": json.RawMessage(rawJSON),
		},
	}, resp, nil
}
