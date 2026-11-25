package git

import (
	"os"
	"path/filepath"
	"testing"
)

// A shallow clone's oldest commit looks like a repository's first, so the
// boundary has to be read from the repository rather than inferred.
func TestShallowReadsTheCloneBoundary(t *testing.T) {
	f := newFixture(t)
	f.write("a.go", "package a\n")
	head := f.commit("first")

	full, err := Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if full.Shallow() {
		t.Error("a repository with its whole history reports itself shallow")
	}

	// git records the boundary as one commit id per line in .git/shallow.
	if err := os.WriteFile(filepath.Join(f.dir, ".git", "shallow"), []byte(head.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cut, err := Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cut.Shallow() {
		t.Error("a repository with a shallow boundary does not report it")
	}
}

func TestShallowIsFalseOutsideARepository(t *testing.T) {
	plain, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if plain.Shallow() {
		t.Error("a directory that is not a repository reports itself shallow")
	}
}

// When go-git declines the repository the boundary file is read directly.
func TestShallowInDegradedMode(t *testing.T) {
	gitDir := t.TempDir()
	r := &Repo{root: gitDir, isRepo: true, degraded: "unsupported extension", gitDir: gitDir}
	if r.Shallow() {
		t.Error("no boundary file, but reported shallow")
	}
	boundary := []byte("0123456789012345678901234567890123456789\n")
	if err := os.WriteFile(filepath.Join(gitDir, "shallow"), boundary, 0o600); err != nil {
		t.Fatal(err)
	}
	if !r.Shallow() {
		t.Error("a boundary file in the git directory was not read")
	}
}
