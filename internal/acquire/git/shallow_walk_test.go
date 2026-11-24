package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
)

// cutBelow turns the fixture into what `git clone --depth` leaves behind: the
// commit `boundary` is recorded in .git/shallow and its parent `gone` is
// removed from the object store.
//
// Built by hand rather than by cloning, for the reason fixture_test.go gives:
// a shallow clone over the file transport needs the `git` binary. The fixture
// writes loose objects, so removing one is removing a file.
func (f *fixture) cutBelow(boundary, gone plumbing.Hash) {
	f.t.Helper()
	hex := gone.String()
	if err := os.Remove(filepath.Join(f.dir, ".git", "objects", hex[:2], hex[2:])); err != nil {
		f.t.Fatalf("remove parent object: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, ".git", "shallow"), []byte(boundary.String()+"\n"), 0o600); err != nil {
		f.t.Fatalf("write shallow file: %v", err)
	}
}

// The defect: on any `git clone --depth N` checkout the walk failed with
// "git: walk commits: object not found" and returned nothing, so every history
// command failed on a CI checkout. It must return what is there and say that
// the clone, not the repository, is where it ends.
func TestRecentCommitsStopsAtAShallowBoundary(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	var hashes []plumbing.Hash
	for i := 0; i < 5; i++ {
		f.write("a.go", filler(i))
		hashes = append(hashes, f.commit("commit"))
	}
	// Keep the newest three; the boundary is the third from the top.
	f.cutBelow(hashes[2], hashes[1])

	r := f.open()
	if !r.Shallow() {
		t.Fatal("fixture is not shallow")
	}
	commits, err := r.RecentCommits(context.Background(), 100)
	if err != nil {
		t.Fatalf("a shallow clone must be read up to its boundary, not failed: %v", err)
	}
	if len(commits) != 3 {
		t.Fatalf("got %d commits, want the 3 the clone holds", len(commits))
	}
	if !ReachedShallowBoundary(commits) {
		t.Error("the walk ended at the clone boundary and did not say so")
	}
	for i, c := range commits {
		wantBoundary := i == 2
		if c.ShallowBoundary != wantBoundary {
			t.Errorf("commit %d: ShallowBoundary = %v, want %v", i, c.ShallowBoundary, wantBoundary)
		}
	}
	// The two commits with a parent still say what they changed. The boundary
	// commit cannot: its parent is not there to diff against, and listing its
	// whole tree would credit its author with every file.
	if len(commits[0].Paths) != 1 || len(commits[1].Paths) != 1 {
		t.Errorf("paths above the boundary = %v, %v; want one each", commits[0].Paths, commits[1].Paths)
	}
	if len(commits[2].Paths) != 0 {
		t.Errorf("boundary commit Paths = %v, want none", commits[2].Paths)
	}

	meta, err := r.RecentCommitsMeta(context.Background(), 100)
	if err != nil || len(meta) != 3 || !ReachedShallowBoundary(meta) {
		t.Errorf("RecentCommitsMeta: %d commits, boundary %v, err %v", len(meta), ReachedShallowBoundary(meta), err)
	}
}

// A walk whose limit ends above the boundary was cut by the limit. Calling
// that "shallow" would send the user to fetch history the answer never needed.
func TestAWalkThatEndsAboveTheBoundaryIsNotMarkedShallow(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	var hashes []plumbing.Hash
	for i := 0; i < 5; i++ {
		f.write("a.go", filler(i))
		hashes = append(hashes, f.commit("commit"))
	}
	f.cutBelow(hashes[1], hashes[0])

	commits, err := f.open().RecentCommits(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 2 || ReachedShallowBoundary(commits) {
		t.Errorf("got %d commits, boundary %v; want 2 and false", len(commits), ReachedShallowBoundary(commits))
	}
}

// A full clone never reports a boundary, including at its first commit, which
// is the commit a shallow boundary is indistinguishable from by shape.
func TestAFullHistoryHasNoShallowBoundary(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	for i := 0; i < 3; i++ {
		f.write("a.go", filler(i))
		f.commit("commit")
	}
	commits, err := f.open().RecentCommits(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 3 || ReachedShallowBoundary(commits) {
		t.Errorf("got %d commits, boundary %v; want 3 and false", len(commits), ReachedShallowBoundary(commits))
	}
	if len(commits[2].Paths) != 1 {
		t.Errorf("a real first commit lists what it added; got %v", commits[2].Paths)
	}
}

// A parent that is missing with no boundary to account for it is a damaged
// repository. That must stay an error: reporting it as a clone depth would
// hide the damage behind advice that cannot fix it.
func TestAMissingParentWithoutABoundaryIsStillAnError(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	var hashes []plumbing.Hash
	for i := 0; i < 3; i++ {
		f.write("a.go", filler(i))
		hashes = append(hashes, f.commit("commit"))
	}
	hex := hashes[0].String()
	if err := os.Remove(filepath.Join(f.dir, ".git", "objects", hex[:2], hex[2:])); err != nil {
		t.Fatal(err)
	}
	if _, err := f.open().RecentCommits(context.Background(), 100); err == nil {
		t.Error("a missing parent with no shallow boundary was read as an ordinary end of history")
	}
}

func TestFileLastModifiedInAShallowClone(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.write("old.go", filler(1))
	first := f.commit("add old")
	f.write("other.go", filler(2))
	second := f.commit("add other")
	f.write("new.go", filler(3))
	third := f.commit("add new")
	f.cutBelow(second, first)

	r := f.open()
	rev, err := r.FileLastModified(context.Background(), "new.go")
	if err != nil || rev.SHA != third.String() {
		t.Errorf("a change inside the fetched history: got %q, %v; want %s", rev.SHA, err, third)
	}
	// old.go is in every tree the clone holds and was changed in none of the
	// commits whose parent is available.
	if _, err := r.FileLastModified(context.Background(), "old.go"); !errors.Is(err, ErrShallowHistory) {
		t.Errorf("a path last changed before the boundary: err = %v, want ErrShallowHistory", err)
	}
}

// `HEAD~N` past the boundary is the commonest way a CI checkout meets its own
// depth: `impact --base HEAD~20`. It must say that it is the clone, and what
// to run, not "object not found".
func TestARevisionBeyondTheShallowBoundaryIsExplained(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	var hashes []plumbing.Hash
	for i := 0; i < 4; i++ {
		f.write("a.go", filler(i))
		hashes = append(hashes, f.commit("commit"))
	}
	f.cutBelow(hashes[1], hashes[0])
	r := f.open()
	ctx := context.Background()

	if sha, err := r.ResolveRevision(ctx, "HEAD~2"); err != nil || sha != hashes[1].String() {
		t.Errorf("a revision inside the clone: %q, %v", sha, err)
	}
	if _, err := r.ResolveRevision(ctx, "HEAD~3"); !errors.Is(err, ErrShallowHistory) {
		t.Errorf("ResolveRevision past the boundary: err = %v, want ErrShallowHistory", err)
	}
	if _, err := r.ChangedSince(ctx, "HEAD~3", "HEAD"); !errors.Is(err, ErrShallowHistory) {
		t.Errorf("ChangedSince past the boundary: err = %v, want ErrShallowHistory", err)
	}
	// A name that never existed is not a depth problem and must not be
	// explained as one.
	if _, err := r.ResolveRevision(ctx, "no-such-branch"); err == nil || errors.Is(err, ErrShallowHistory) {
		t.Errorf("an unknown name: err = %v, want an ordinary resolution failure", err)
	}
}
