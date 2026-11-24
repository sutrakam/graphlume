package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Shallow reports whether the repository was cloned with truncated history
// (`git clone --depth`, which is what most CI checkouts do).
//
// A history walk cannot tell this from the commits it returns: a shallow
// clone's oldest commit looks exactly like a repository's first one. Anything
// that draws a conclusion from how much history exists has to ask, or it
// reports "the whole history is three commits" for a repository with ten
// thousand.
//
// False for a directory that is not a repository, and false when the answer
// cannot be read: "not known to be shallow" is the only safe default, since
// the callers use true to add a warning.
func (r *Repo) Shallow() bool {
	if !r.isRepo {
		return false
	}
	if r.repo != nil {
		boundary, err := r.repo.Storer.Shallow()
		return err == nil && len(boundary) > 0
	}
	// Degraded mode: go-git declined the repository, so read the file git
	// itself records the boundary in. Empty means not shallow.
	if r.gitDir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(r.gitDir, "shallow"))
	return err == nil && !info.IsDir() && info.Size() > 0
}

// shallowCut is what a walk needs to know about a shallow clone's boundary:
// which parents were never fetched. It is empty for a full clone.
type shallowCut struct {
	// missing holds the parent hashes named by a boundary commit and absent
	// from the object store.
	missing map[plumbing.Hash]bool
}

// boundary reports whether any parent of c was never fetched.
func (s shallowCut) boundary(c *object.Commit) bool {
	for _, h := range c.ParentHashes {
		if s.missing[h] {
			return true
		}
	}
	return false
}

// firstParentMissing reports whether the parent a commit is diffed against was
// never fetched.
func (s shallowCut) firstParentMissing(c *object.Commit) bool {
	return len(c.ParentHashes) > 0 && s.missing[c.ParentHashes[0]]
}

// shallowCut reads the clone boundary git recorded in `.git/shallow`.
//
// A parent is listed as missing only when it is named by a commit in that file
// AND absent from the object store. Both conditions matter. The file alone is
// not enough: after a later, deeper fetch of another branch a boundary commit's
// parent can be present, and then it is real history that should be read. And
// absence alone is not enough: a missing parent that no boundary accounts for
// is a damaged repository, which must stay an error and not be reported as a
// clone depth.
func (v *objectView) shallowCut() (shallowCut, error) {
	boundary, err := v.repo.Storer.Shallow()
	if err != nil {
		return shallowCut{}, fmt.Errorf("git: read the shallow-clone boundary: %w", err)
	}
	cut := shallowCut{}
	for _, h := range boundary {
		c, err := v.repo.CommitObject(h)
		if err != nil {
			if errors.Is(err, plumbing.ErrObjectNotFound) {
				continue // a boundary entry for a commit this clone does not hold
			}
			return shallowCut{}, fmt.Errorf("git: read shallow-boundary commit %s: %w", h, err)
		}
		for _, p := range c.ParentHashes {
			herr := v.repo.Storer.HasEncodedObject(p)
			if herr == nil {
				continue
			}
			if !errors.Is(herr, plumbing.ErrObjectNotFound) {
				return shallowCut{}, fmt.Errorf("git: look up parent %s of %s: %w", p, h, herr)
			}
			if cut.missing == nil {
				cut.missing = map[plumbing.Hash]bool{}
			}
			cut.missing[p] = true
		}
	}
	return cut, nil
}

// walk opens a newest-first walk back from HEAD that stops at a shallow
// clone's boundary. It returns a nil iterator and a nil error for a repository
// with no commits.
//
// Committer-time order rather than go-git's default depth-first order:
// depth-first on a merge-heavy history returns one long ancestor chain before
// the recent commits on other parents, so "the 20 newest commits" would not
// actually be the 20 newest.
//
// This does what Repository.Log does with one difference. go-git's iterator
// loads every parent of a commit before returning it and knows nothing about
// `.git/shallow`, so on a shallow clone it failed with "object not found" on
// the boundary commit and returned nothing at all, which is how `graphlume
// hotspots` failed on every `git clone --depth N` checkout. Handing it the
// missing parents as already seen makes it return the boundary commit and
// stop there, which is what `git log` does.
func (v *objectView) walk() (object.CommitIter, shallowCut, error) {
	head, err := v.repo.Head()
	if err != nil {
		if errors.Is(err, plumbing.ErrReferenceNotFound) {
			return nil, shallowCut{}, nil
		}
		return nil, shallowCut{}, fmt.Errorf("git: open commit log: %w", err)
	}
	start, err := v.repo.CommitObject(head.Hash())
	if err != nil {
		return nil, shallowCut{}, fmt.Errorf("git: open commit log: %w", err)
	}
	cut, err := v.shallowCut()
	if err != nil {
		return nil, shallowCut{}, err
	}
	return object.NewCommitIterCTime(start, cut.missing, nil), cut, nil
}
