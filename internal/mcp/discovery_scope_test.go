package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/multirepo"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/store"
)

// The projects action lists this machine's other repositories only to a
// local stdio session; otherwise it reports the current project alone. A
// repository-chosen display name is bounded and stripped of control text.
func TestProjectsActionIsScopedUnlessLocal(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cur := filepath.Join(home, "work", "current")
	other := filepath.Join(home, "work", "other")
	for _, d := range []string{cur, other} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := multirepo.RegisterProject(context.Background(), home, multirepo.ProjectEntry{RootPath: cur,
		Name: "cur\x1b[31m" + strings.Repeat("x", 200)}); err != nil {
		t.Fatal(err)
	}
	if err := multirepo.RegisterProject(context.Background(), home, multirepo.ProjectEntry{RootPath: other}); err != nil {
		t.Fatal(err)
	}

	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	cfg := config.Defaults()
	cfg.RepoRoot = cur

	for _, local := range []bool{false, true} {
		srv, err := New(Deps{Registry: registry.New(db), Config: cfg, ListAllProjects: local})
		if err != nil {
			t.Fatal(err)
		}
		_, resp, err := srv.listProjects(ctx, nil, listProjectsArgs{})
		if err != nil || resp == nil {
			t.Fatalf("projects (local=%v): %v", local, err)
		}
		var paths []string
		for _, it := range resp.Items {
			paths = append(paths, it.Path)
			if strings.ContainsRune(it.Name, '\x1b') || len(it.Name) > 100 {
				t.Errorf("display name not sanitised: %q", it.Name)
			}
		}
		want := 1
		if local {
			want = 2
		}
		if len(paths) != want {
			t.Errorf("local=%v listed %v; want %d project(s)", local, paths, want)
		}
		if !local && len(paths) == 1 && multirepo.CanonicalPath(paths[0]) != multirepo.CanonicalPath(cur) {
			t.Errorf("non-local listing shows %s, not the current project", paths[0])
		}
	}
}
