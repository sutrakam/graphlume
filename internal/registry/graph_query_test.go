package registry

import (
	"context"
	"strings"
	"testing"
)

func TestQueryGraphSafeguards(t *testing.T) {
	ctx := context.Background()
	reg, _ := newTestRegistry(t)

	// Valid SELECT
	res, err := reg.QueryGraph(ctx, "SELECT count(*) as c FROM symbols", 10)
	if err != nil {
		t.Fatalf("valid select failed: %v", err)
	}
	if len(res.Columns) != 1 || res.Columns[0] != "c" {
		t.Errorf("unexpected columns: %v", res.Columns)
	}

	// Reject INSERT
	_, err = reg.QueryGraph(ctx, "INSERT INTO symbols (name) VALUES ('evil')", 10)
	if err == nil || !strings.Contains(err.Error(), "only read-only") {
		t.Errorf("expected rejection of INSERT, got: %v", err)
	}

	// Reject DROP
	_, err = reg.QueryGraph(ctx, "DROP TABLE symbols", 10)
	if err == nil {
		t.Errorf("expected rejection of DROP")
	}

	// Reject chained statements with semicolon
	_, err = reg.QueryGraph(ctx, "SELECT 1; DROP TABLE symbols", 10)
	if err == nil || !strings.Contains(err.Error(), "multi-statement") {
		t.Errorf("expected rejection of semicolon chaining, got: %v", err)
	}

	// Reject PRAGMA
	_, err = reg.QueryGraph(ctx, "SELECT * FROM (PRAGMA table_info(symbols))", 10)
	if err == nil || !strings.Contains(err.Error(), "forbidden keyword") {
		t.Errorf("expected rejection of PRAGMA, got: %v", err)
	}
}
