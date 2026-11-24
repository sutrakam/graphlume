package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/pipeline"
)

// The same "not installed" sentence for every project of a language is printed
// once, with the count, not once per project.
func TestRenderCompilerIndexersGroupsIdenticalSkips(t *testing.T) {
	skip := "scip-dotnet is not installed"
	o := pipeline.CompilerIndexOutcome{Steps: []pipeline.CompilerIndexStep{
		{Indexer: "scip-dotnet", Language: "csharp", Dir: "a", Skipped: skip},
		{Indexer: "scip-dotnet", Language: "csharp", Dir: "b", Skipped: skip},
		{Indexer: "scip-dotnet", Language: "csharp", Dir: "c", Skipped: skip},
		{Indexer: "scip-typescript", Language: "typescript", Dir: "web", Skipped: "scip-typescript exited 1 for typescript at web"},
	}}
	var b bytes.Buffer
	renderCompilerIndexers(&b, config.AccuracyAuto, o)
	out := b.String()
	if n := strings.Count(out, skip); n != 1 {
		t.Fatalf("the repeated skip was printed %d times:\n%s", n, out)
	}
	if !strings.Contains(out, "the same for 3 projects") {
		t.Errorf("the count is missing:\n%s", out)
	}
	if !strings.Contains(out, "exited 1 for typescript at web") {
		t.Errorf("a distinct skip was dropped:\n%s", out)
	}
}
