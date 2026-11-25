package cli

import (
	"strings"
	"testing"
)

func TestCLIExploreCommand(t *testing.T) {
	root := queryFixture(t)

	// Settle calls Save
	out, err := run(t, root, "explore", "Settle")
	if err != nil {
		t.Fatalf("explore failed: %v\nOutput: %s", err, out)
	}

	if !strings.Contains(out, "Settle") {
		t.Errorf("expected output to mention Settle, got:\n%s", out)
	}
	if !strings.Contains(out, "Upstream Callers") {
		t.Errorf("expected output to include Upstream Callers section, got:\n%s", out)
	}
	if !strings.Contains(out, "Downstream Callees") {
		t.Errorf("expected output to include Downstream Callees section, got:\n%s", out)
	}
}
