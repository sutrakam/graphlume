package git

import (
	"context"
	"reflect"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// equivalenceFixture builds a history with every shape the two walks must
// agree on: adds, edits, deletes, a rename, nested directories, a non-ASCII
// and a quoted file name, a commit body with a constraint line, and a merge
// whose second parent changed files the first did not.
func equivalenceFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.write("a.go", "package a\n")
	f.write("pkg/b.go", "package pkg\n")
	f.commit("add a and b")
	f.write("pkg/deep/c.go", filler(1))
	f.commit("add c\n\nFIX: keep c in sync with b")
	f.rename("pkg/deep/c.go", "pkg/deep/c2.go")
	f.commit("rename c")
	f.write("docs/naïve résumé.md", "unicode\n")
	f.write(`odd "quoted" name.txt`, "quotes\n")
	f.commit("odd names")
	f.remove("a.go")
	f.commit("drop a")
	base := f.commit("empty commit")

	// A side line and a merge of it.
	f.write("side.go", "package side\n")
	side := f.commit("side work")
	f.write("main.go", "package main\n")
	f.seq++
	sig := &object.Signature{Name: "Merger", Email: "m@example.com", When: time.Unix(1700000000+int64(f.seq), 0).UTC()}
	if _, err := f.wt.Commit("merge side", &gogit.CommitOptions{
		Author: sig, Committer: sig, Parents: []plumbing.Hash{base, side},
	}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	for i := 0; i < 12; i++ {
		f.write("pkg/b.go", "package pkg\n// "+filler(i)[:20]+"\n")
		f.commit("edit b")
	}
	return f
}

func walkBoth(t *testing.T, r *Repo, limit int) (goGit, binary []Commit) {
	t.Helper()
	ctx := context.Background()
	t.Setenv(historyWalkEnv, "go-git")
	goGit, _, err := r.recentCommits(ctx, limit, true, WalkOptions{})
	if err != nil {
		t.Fatalf("go-git walk: %v", err)
	}
	t.Setenv(historyWalkEnv, "")
	if !r.walkViaBinary() {
		t.Skip("no git binary on PATH")
	}
	binary, _, err = r.recentCommits(ctx, limit, true, WalkOptions{})
	if err != nil {
		t.Fatalf("binary walk: %v", err)
	}
	return normalize(goGit), normalize(binary)
}

// normalize treats an empty list and no list as the same answer, which they
// are to every consumer.
func normalize(cs []Commit) []Commit {
	for i := range cs {
		if len(cs[i].Paths) == 0 {
			cs[i].Paths = nil
		}
		if len(cs[i].Constraints) == 0 {
			cs[i].Constraints = nil
		}
	}
	return cs
}

// The git binary walk replaces go-git's for speed; it must return the same
// commits, in the same order, with the same paths, authors and constraints.
func TestRecentCommitsBinaryMatchesGoGit(t *testing.T) {
	r := equivalenceFixture(t).open()
	for _, limit := range []int{1, 5, 100} {
		g, b := walkBoth(t, r, limit)
		if len(g) == 0 {
			t.Fatalf("limit %d: go-git walk returned nothing", limit)
		}
		if !reflect.DeepEqual(g, b) {
			for i := range g {
				if i >= len(b) || !reflect.DeepEqual(g[i], b[i]) {
					var bi Commit
					if i < len(b) {
						bi = b[i]
					}
					t.Fatalf("limit %d: commit %d differs\n go-git: %+v\n binary: %+v", limit, i, g[i], bi)
				}
			}
			t.Fatalf("limit %d: binary walk returned %d commits, go-git %d", limit, len(b), len(g))
		}
	}
}

// Skip continues a walk in the walk's own order, on both readers, and a
// budget that has already run out still returns at least one whole commit and
// says the walk is incomplete.
func TestRecentCommitsWithinSkipAndBudget(t *testing.T) {
	r := equivalenceFixture(t).open()
	ctx := context.Background()
	// The unmemoised walk: the memo would answer the later calls from the
	// first one and test nothing.
	walk := func(limit int, opts WalkOptions) ([]Commit, bool, error) {
		cs, complete, err := r.recentCommits(ctx, limit, true, opts)
		return normalize(cs), complete, err
	}
	for _, mode := range []string{"go-git", ""} {
		t.Run("walk="+mode, func(t *testing.T) {
			t.Setenv(historyWalkEnv, mode)
			if mode == "" && !r.walkViaBinary() {
				t.Skip("no git binary on PATH")
			}
			all, complete, err := walk(100, WalkOptions{})
			if err != nil || !complete {
				t.Fatalf("full walk: complete=%v err=%v", complete, err)
			}
			first, _, err := walk(7, WalkOptions{})
			if err != nil {
				t.Fatal(err)
			}
			rest, complete, err := walk(100-7, WalkOptions{Skip: 7})
			if err != nil || !complete {
				t.Fatalf("continued walk: complete=%v err=%v", complete, err)
			}
			if got := append(first, rest...); !reflect.DeepEqual(got, all) {
				t.Fatalf("first 7 + skip 7 = %d commits, want the %d of one walk in the same order", len(got), len(all))
			}

			part, complete, err := walk(100, WalkOptions{Budget: time.Nanosecond})
			if err != nil {
				t.Fatal(err)
			}
			if complete || len(part) == 0 || len(part) >= len(all) {
				t.Fatalf("exhausted budget: %d of %d commits, complete=%v; want a non-empty partial walk", len(part), len(all), complete)
			}
			if !reflect.DeepEqual(part, all[:len(part)]) {
				t.Fatal("a partial walk is not the newest part of the full walk")
			}
		})
	}
}

// RecentCommits remembers its walk per HEAD: a second call with the same or a
// smaller limit returns the same commits without walking.
func TestRecentCommitsMemo(t *testing.T) {
	f := equivalenceFixture(t)
	r := f.open()
	ctx := context.Background()
	want, err := r.RecentCommits(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := r.walks.get(mustHead(t, r), 4); !ok || !reflect.DeepEqual(got, want[:4]) {
		t.Fatalf("memo for a smaller limit: ok=%v", ok)
	}
	again, err := r.RecentCommits(ctx, 4)
	if err != nil || !reflect.DeepEqual(again, want[:4]) {
		t.Fatalf("second call: %v", err)
	}
	// A new commit moves HEAD and the memo no longer answers.
	f.write("new.go", "package n\n")
	f.commit("new")
	moved, err := r.RecentCommits(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if moved[0].SHA == want[0].SHA {
		t.Fatal("RecentCommits answered from the memo after HEAD moved")
	}
}

func mustHead(t *testing.T, r *Repo) string {
	t.Helper()
	h, err := r.HeadSHA(context.Background())
	if err != nil || h == "" {
		t.Fatalf("HeadSHA: %q %v", h, err)
	}
	return h
}
