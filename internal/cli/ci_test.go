package cli

import (
	"context"
	"strings"
	"testing"
)

func TestCICheckHelp(t *testing.T) {
	root := NewRoot()
	root.SetArgs([]string{"ci", "--help"})
	var out strings.Builder
	root.SetOut(&out)

	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ci --help failed: %v", err)
	}
	if !strings.Contains(out.String(), "ci check") {
		t.Errorf("expected 'ci check' in help output, got: %s", out.String())
	}
}
