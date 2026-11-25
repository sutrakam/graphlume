package mcp

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sutrakam/graphlume/internal/config"
)

func TestExploreTool(t *testing.T) {
	fs := newFullServer(t, seedChain, withProfile(config.MCPFull))
	ctx := context.Background()

	// 1. Explore via standalone explore tool
	res, err := fs.sess.CallTool(ctx, &sdk.CallToolParams{
		Name: "explore",
		Arguments: map[string]any{
			"name": "middle",
		},
	})
	if err != nil {
		t.Fatalf("call explore: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("empty response from explore")
	}
	tc, ok := res.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "middle") {
		t.Errorf("expected text to mention 'middle', got: %s", tc.Text)
	}

	// 2. Explore via frontdoor action: explore
	fsFront := newFullServer(t, seedChain, withProfile(config.MCPCompact))
	resFront, err := fsFront.sess.CallTool(ctx, &sdk.CallToolParams{
		Name: "graphlume",
		Arguments: map[string]any{
			"action": "explore",
			"name":   "middle",
		},
	})
	if err != nil {
		t.Fatalf("call frontdoor explore: %v", err)
	}
	if len(resFront.Content) == 0 {
		t.Fatal("empty response from frontdoor explore")
	}
	tcFront, ok := resFront.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", resFront.Content[0])
	}
	if !strings.Contains(tcFront.Text, "middle") {
		t.Errorf("expected frontdoor text to mention 'middle', got: %s", tcFront.Text)
	}
}
