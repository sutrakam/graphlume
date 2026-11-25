package git

import "testing"

// Both walks mark the merge, and only the merge: a count of how often a file
// changed skips it, as `git log --name-only` lists no paths for one.
func TestBothWalksMarkTheMergeCommit(t *testing.T) {
	goGit, binary := walkBoth(t, equivalenceFixture(t).open(), 100)
	for name, cs := range map[string][]Commit{"go-git": goGit, "binary": binary} {
		var merges []string
		for _, c := range cs {
			if c.Merge {
				merges = append(merges, c.Subject)
			}
		}
		if len(merges) != 1 || merges[0] != "merge side" {
			t.Errorf("%s walk: merges %q, want only \"merge side\"", name, merges)
		}
	}
}
