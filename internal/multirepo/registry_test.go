package multirepo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sutrakam/graphlume/internal/acquire/fs"
)

func TestGlobalRegistryLifecycle(t *testing.T) {
	home := t.TempDir()

	// Initial load should give empty registry
	reg, err := LoadGlobalRegistry(home)
	if err != nil {
		t.Fatalf("load empty registry: %v", err)
	}
	if reg.SchemaVersion != GlobalRegistryCurrentVersion {
		t.Errorf("version = %d, want %d", reg.SchemaVersion, GlobalRegistryCurrentVersion)
	}
	if len(reg.Projects) != 0 {
		t.Errorf("expected 0 projects, got %d", len(reg.Projects))
	}

	// Register two projects
	p1 := ProjectEntry{
		ID:       "auth-service",
		RootPath: filepath.Join(home, "work", "auth-service"),
		DBPath:   filepath.Join(home, "work", "auth-service", ".graphlume", "index.db"),
		Mode:     "single_repo",
	}
	p2 := ProjectEntry{
		ID:       "billing-api",
		RootPath: filepath.Join(home, "work", "billing-api"),
		DBPath:   filepath.Join(home, "work", "billing-api", ".graphlume", "index.db"),
		Mode:     "single_repo",
	}

	for _, p := range []ProjectEntry{p1, p2} {
		if err := os.MkdirAll(p.RootPath, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := RegisterProject(context.Background(), home, p1); err != nil {
		t.Fatalf("register p1: %v", err)
	}
	if err := RegisterProject(context.Background(), home, p2); err != nil {
		t.Fatalf("register p2: %v", err)
	}

	reg, err = LoadGlobalRegistry(home)
	if err != nil {
		t.Fatalf("load registered: %v", err)
	}
	if len(reg.Projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(reg.Projects))
	}

	// Test FindProjectByID
	found, ok := FindProjectByID(reg, "auth-service")
	if !ok || found.ID != "auth-service" {
		t.Errorf("FindProjectByID failed: %+v", found)
	}

	// Test FindProjectByPath (exact and subpath)
	subpath := filepath.Join(home, "work", "auth-service", "cmd", "server", "main.go")
	matched, ok := FindProjectByPath(reg, subpath)
	if !ok || matched.ID != "auth-service" {
		t.Errorf("FindProjectByPath subpath failed: ok=%v, matched=%+v", ok, matched)
	}

	// Unregister
	if err := UnregisterProject(context.Background(), home, "auth-service"); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	reg, err = LoadGlobalRegistry(home)
	if err != nil {
		t.Fatalf("load after unregister: %v", err)
	}
	if len(reg.Projects) != 1 || reg.Projects[0].ID != "billing-api" {
		t.Errorf("unexpected projects after unregister: %+v", reg.Projects)
	}
}

func TestConcurrentRegistryMutations(t *testing.T) {
	home := t.TempDir()

	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		idx := i
		go func() {
			defer wg.Done()
			root := filepath.Join(home, "proj", string(rune('A'+idx)))
			if err := os.MkdirAll(root, 0o750); err != nil {
				t.Error(err)
				return
			}
			p := ProjectEntry{RootPath: root}
			if err := RegisterProject(context.Background(), home, p); err != nil {
				t.Errorf("concurrent register: %v", err)
			}
		}()
	}
	wg.Wait()

	reg, err := LoadGlobalRegistry(home)
	if err != nil {
		t.Fatalf("load after concurrency: %v", err)
	}
	if len(reg.Projects) != n {
		t.Errorf("expected %d projects, got %d", n, len(reg.Projects))
	}
}

// A project is its root: a second project with the same folder name gets its
// own ID instead of replacing the first, a name the repository chooses never
// becomes the key, and a project whose folder is gone is dropped.
func TestRegistryKeysByRootNotName(t *testing.T) {
	home := t.TempDir()
	a := filepath.Join(home, "one", "api")
	b := filepath.Join(home, "two", "api")
	gone := filepath.Join(home, "gone")
	for _, d := range []string{a, b, gone} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []ProjectEntry{
		{RootPath: a, Name: "victim"},
		{RootPath: gone},
		{RootPath: b, ID: "victim", Name: "victim"}, // a caller-chosen ID must not win
	} {
		if err := RegisterProject(context.Background(), home, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	// Re-registering a refreshes it and prunes the deleted project.
	if err := RegisterProject(context.Background(), home, ProjectEntry{RootPath: a, Name: "victim"}); err != nil {
		t.Fatal(err)
	}
	reg, err := LoadGlobalRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Projects) != 2 {
		t.Fatalf("projects = %+v; want the two api folders and not the deleted one", reg.Projects)
	}
	ids := map[string]string{}
	for _, p := range reg.Projects {
		if prev, dup := ids[p.ID]; dup {
			t.Fatalf("ID %q is shared by %s and %s", p.ID, prev, p.RootPath)
		}
		ids[p.ID] = p.RootPath
		if p.ID == "victim" {
			t.Errorf("a repository-supplied name became the registry key: %+v", p)
		}
	}
	if pa, ok := FindProjectByPath(reg, a); !ok || CanonicalPath(pa.RootPath) != CanonicalPath(a) {
		t.Errorf("project one/api was replaced: %+v", reg.Projects)
	}
}

func TestRegistryRefusesNewerSchema(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, GlobalRegistryDir), 0o750); err != nil {
		t.Fatal(err)
	}
	newer := []byte(`{"schema_version": 99, "projects": [{"id": "x", "root_path": "/x", "future_field": 1}]}`)
	if err := os.WriteFile(GlobalRegistryPath(home), newer, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadGlobalRegistry(home); !errors.Is(err, ErrRegistryNewer) {
		t.Fatalf("load: err = %v, want ErrRegistryNewer", err)
	}
	err := RegisterProject(context.Background(), home, ProjectEntry{RootPath: t.TempDir()})
	if !errors.Is(err, ErrRegistryNewer) {
		t.Fatalf("register: err = %v, want ErrRegistryNewer", err)
	}
	got, _ := os.ReadFile(GlobalRegistryPath(home))
	if string(got) != string(newer) {
		t.Fatal("a newer registry was overwritten")
	}
}

func TestRegistryLockWaitHonoursContext(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, GlobalRegistryDir), 0o750); err != nil {
		t.Fatal(err)
	}
	held, err := fs.TryLock(GlobalRegistryLockPath(home))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Unlock() }()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = MutateGlobalRegistry(ctx, home, func(*GlobalRegistry) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("waited %s past a 100ms context", time.Since(start))
	}
}

// RecordIndexed matches by root only: a repository whose configured name
// equals another project's ID must not update that project.
func TestRecordIndexedMatchesByRootOnly(t *testing.T) {
	home := t.TempDir()
	a := filepath.Join(t.TempDir(), "api")
	b := filepath.Join(t.TempDir(), "other")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if err := RegisterProject(ctx, home, ProjectEntry{RootPath: a}); err != nil {
		t.Fatal(err)
	}
	if err := RecordIndexed(ctx, home, ProjectEntry{RootPath: b, Name: "api", LastIndexedAt: "t1", DBPath: "/b.db"}); err != nil {
		t.Fatal(err)
	}
	reg, err := LoadGlobalRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Projects) != 2 {
		t.Fatalf("projects = %d, want 2", len(reg.Projects))
	}
	pa, _ := FindProjectByPath(reg, a)
	if pa.LastIndexedAt != "" || pa.DBPath == "/b.db" {
		t.Errorf("project a was updated by b's index: %+v", *pa)
	}
	pb, _ := FindProjectByPath(reg, b)
	if pb.ID == pa.ID || pb.LastIndexedAt != "t1" {
		t.Errorf("project b = %+v", *pb)
	}
}
