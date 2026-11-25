package indexer

import (
	"context"
	"testing"

	"github.com/sutrakam/graphlume/internal/config"
)

// TestPlanSkipsProjectsTheIndexExcludes: a repository excluded editors/** from
// the index, yet scip-typescript ran over editors/vscode and its index matched
// nothing, reported as "probably built from a different commit".
func TestPlanSkipsProjectsTheIndexExcludes(t *testing.T) {
	root := writeRepo(t, map[string]string{
		"web/package.json":          `{"name":"web"}`,
		"web/tsconfig.json":         `{}`,
		"web/src/a.ts":              "export const a = 1\n",
		"editors/ext/package.json":  `{"name":"ext"}`,
		"editors/ext/tsconfig.json": `{}`,
		"editors/ext/src/b.ts":      "export const b = 1\n",
	})
	cfg := config.Defaults()
	cfg.RepoRoot = root
	cfg.Exclude = append(cfg.Exclude, "editors/**")
	plan, err := NewPlanWith(context.Background(), root, cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, p := range plan.Projects {
		dirs = append(dirs, p.Dir)
	}
	for _, d := range dirs {
		if d == "editors/ext" {
			t.Errorf("the plan includes editors/ext, which index.exclude leaves out: %v", dirs)
		}
	}
	found := false
	for _, d := range dirs {
		found = found || d == "web"
	}
	if !found {
		t.Errorf("the plan lost web, which is not excluded: %v", dirs)
	}
}
