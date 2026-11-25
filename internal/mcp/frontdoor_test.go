package mcp

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sutrakam/graphlume/internal/config"
)

func TestFrontdoorCompactProfile(t *testing.T) {
	fs := newFullServer(t, seedChain, withProfile(config.MCPCompact))
	tools := fs.srv.Tools()
	if len(tools) != 1 || tools[0] != "graphlume" {
		t.Fatalf("expected only ['graphlume'], got %v", tools)
	}

	ctx := context.Background()

	// 1. Overview action
	res, err := fs.sess.CallTool(ctx, &sdk.CallToolParams{
		Name: "graphlume",
		Arguments: map[string]any{
			"action": "overview",
		},
	})
	if err != nil {
		t.Fatalf("call overview: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("empty response from overview")
	}

	// 2. Search action
	res, err = fs.sess.CallTool(ctx, &sdk.CallToolParams{
		Name: "graphlume",
		Arguments: map[string]any{
			"action": "search",
			"query":  "entry",
		},
	})
	if err != nil {
		t.Fatalf("call search: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("empty response from search")
	}

	// 3. Diff action
	res, err = fs.sess.CallTool(ctx, &sdk.CallToolParams{
		Name: "graphlume",
		Arguments: map[string]any{
			"action": "diff",
			"diff":   "diff --git a/foo.go b/foo.go\n--- a/foo.go\n+++ b/foo.go\n@@ -1,2 +1,2 @@\n-old\n+new",
		},
	})
	if err != nil {
		t.Fatalf("call diff: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("empty response from diff")
	}

	// 4. Query action
	res, err = fs.sess.CallTool(ctx, &sdk.CallToolParams{
		Name: "graphlume",
		Arguments: map[string]any{
			"action": "query",
			"query":  "SELECT count(*) as total_symbols FROM symbols",
		},
	})
	if err != nil {
		t.Fatalf("call query: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("empty response from query")
	}
	qText := res.Content[0].(*sdk.TextContent).Text
	if !strings.Contains(qText, "total_symbols") {
		t.Errorf("expected table with total_symbols, got: %s", qText)
	}

	// 5. Blindspots action
	_, err = fs.sess.CallTool(ctx, &sdk.CallToolParams{
		Name: "graphlume",
		Arguments: map[string]any{
			"action": "blindspots",
		},
	})
	if err != nil {
		t.Fatalf("call blindspots: %v", err)
	}

	// 6. Projects action
	res, err = fs.sess.CallTool(ctx, &sdk.CallToolParams{
		Name: "graphlume",
		Arguments: map[string]any{
			"action": "projects",
		},
	})
	if err != nil {
		t.Fatalf("call projects: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("empty response from projects action")
	}

	// 7. Invalid action gives friendly error
	res, err = fs.sess.CallTool(ctx, &sdk.CallToolParams{
		Name: "graphlume",
		Arguments: map[string]any{
			"action": "fly_to_moon",
		},
	})
	if err != nil {
		t.Fatalf("call invalid action: %v", err)
	}
	text := res.Content[0].(*sdk.TextContent).Text
	if !strings.Contains(text, "fly_to_moon") || !strings.Contains(text, "endpoints") || !strings.Contains(text, "blindspots") {
		t.Errorf("expected helpful error for invalid action, got: %s", text)
	}
}
