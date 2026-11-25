package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
)

// shallowFixture is whoFixture as `git clone --depth 3` would leave it: the
// newest three of its four commits, the third recorded as the boundary and its
// parent absent from the object store. Built by hand because a real shallow
// clone needs the git binary; see whoFixture.
func shallowFixture(t *testing.T) string {
	t.Helper()
	root := whoFixture(t)
	dir := root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		dir = resolved
	}
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	iter, err := repo.Log(&gogit.LogOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var hashes []string // newest first
	for {
		c, err := iter.Next()
		if err != nil {
			break
		}
		hashes = append(hashes, c.Hash.String())
	}
	if len(hashes) != 4 {
		t.Fatalf("fixture has %d commits, want 4", len(hashes))
	}
	boundary, gone := hashes[2], hashes[3]
	if err := os.Remove(filepath.Join(dir, ".git", "objects", gone[:2], gone[2:])); err != nil {
		t.Fatalf("remove the unfetched parent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "shallow"), []byte(boundary+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// Every history command failed on a `git clone --depth N` checkout with
// "git: walk commits: object not found", which is every CI checkout. Each must
// answer from the commits that are there, say how many that is, and never
// describe them as the repository's whole history.
func TestHistoryCommandsReadAShallowCloneUpToItsBoundary(t *testing.T) {
	root := shallowFixture(t)

	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"hotspots"}, []string{"Shallow clone: 3 commit(s) available", "git fetch --unshallow"}},
		{[]string{"bus-factor", "--min-commits", "1"}, []string{"Shallow clone: 3 commit(s) available", "This is a shallow clone"}},
		{[]string{"owners", "Save"}, []string{"Shallow clone: 3 commits available", "git fetch --unshallow"}},
		{[]string{"tool", "call", "hotspots"}, []string{"Shallow clone: 3 commit(s) available"}},
		{[]string{"tool", "call", "owners", "symbol=Save"}, []string{"Shallow clone: 3 commits available"}},
		{[]string{"tool", "call", "busfactor", "min_commits=1"}, []string{"Shallow clone: 3 commit(s) available"}},
	} {
		out, err := run(t, root, tc.args...)
		if err != nil {
			t.Errorf("%v on a shallow clone: %v\n%s", tc.args, err, out)
			continue
		}
		flat := collapse(out)
		for _, want := range tc.want {
			if !strings.Contains(flat, want) {
				t.Errorf("%v: missing %q:\n%s", tc.args, want, out)
			}
		}
		for _, wrong := range []string{
			"whole history (", "The whole history was read", "reachable from HEAD were read", "object not found",
		} {
			if strings.Contains(flat, wrong) {
				t.Errorf("%v: a shallow clone was described with %q:\n%s", tc.args, wrong, out)
			}
		}
	}
}

// The same checkout before it was cut says the opposite, so the wording above
// is a statement about the clone and not something every answer now carries.
func TestHistoryCommandsStillSayWhenTheWholeHistoryWasRead(t *testing.T) {
	root := whoFixture(t)
	for args, want := range map[string]string{
		"hotspots":                   "whole history (4 commits) fits inside the window",
		"bus-factor --min-commits=1": "All 4 commit(s) reachable from HEAD were read",
		"owners Save":                "The whole history was read (4 commits from HEAD)",
	} {
		out, err := run(t, root, strings.Fields(args)...)
		if err != nil {
			t.Errorf("%s: %v\n%s", args, err, out)
			continue
		}
		if flat := collapse(out); !strings.Contains(flat, want) || strings.Contains(flat, "Shallow clone") {
			t.Errorf("%s: want %q and no shallow-clone note:\n%s", args, want, out)
		}
	}
}

// `impact --base HEAD~N` is how a CI job meets its own clone depth. The
// failure must name the cause and the command that fixes it.
func TestAChangeRangeBeyondTheShallowBoundaryIsExplained(t *testing.T) {
	root := shallowFixture(t)
	out, err := run(t, root, "impact", "--base", "HEAD~3", "--head", "HEAD")
	text := out
	if err != nil {
		text += " " + err.Error()
	}
	if flat := collapse(text); !strings.Contains(flat, "shallow clone") || !strings.Contains(flat, "git fetch --unshallow") {
		t.Errorf("a base revision older than the clone depth must say so: err=%v\n%s", err, out)
	}
}
