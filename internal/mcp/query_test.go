package mcp

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sutrakam/graphlume/internal/config"
)

func TestSearchGraphTool(t *testing.T) {
	fs := newFullServer(t, seedChain, withProfile(config.MCPFull))
	ctx := context.Background()

	// 1. Successful SELECT query
	res, err := fs.sess.CallTool(ctx, &sdk.CallToolParams{
		Name: "graph",
		Arguments: map[string]any{
			"query": "SELECT count(*) AS total_symbols FROM symbols",
		},
	})
	if err != nil {
		t.Fatalf("call graph: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("empty content from graph")
	}
	text := res.Content[0].(*sdk.TextContent).Text
	if !strings.Contains(text, "total_symbols") {
		t.Errorf("expected table with total_symbols, got: %s", text)
	}

	// 2. Reject mutating query
	res, err = fs.sess.CallTool(ctx, &sdk.CallToolParams{
		Name: "graph",
		Arguments: map[string]any{
			"query": "DROP TABLE symbols",
		},
	})
	if err != nil {
		t.Fatalf("call graph drop: %v", err)
	}
	errText := res.Content[0].(*sdk.TextContent).Text
	if !strings.Contains(errText, "Query rejected or failed") {
		t.Errorf("expected rejection, got: %s", errText)
	}
}
