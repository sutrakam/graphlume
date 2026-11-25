package ownership

import "context"

// Commit is one commit, reduced to what a routing decision needs.
//
// A near-copy of internal/acquire/git.Commit, restated here rather than
// imported, for two reasons that are both about what a port is for.
//
// The first is dependency direction. internal/acquire/git is an acquisition
// adapter that pulls go-git into everything it touches; a capability that named
// its types in its own signatures would drag a git implementation into the
// dependency graph of every consumer and every test double. docs/development.md §1 puts
// the interface at the consumer, and this is the consumer.
//
// The second is the one that would actually cause a bug. Paths here must already
// be in the INDEX's path space — repo-relative to the same root model.File.Path
// is relative to. A git worktree root and a GraphLume project root are frequently
// not the same directory (a service inside a monorepo is the ordinary case), and
// a git path that has not been rebased onto the project root silently matches
// nothing: every person would rank at zero and the report would look like a
// repository nobody has ever touched. Accepting the adapter's type directly
// would make that translation easy to forget; a distinct type makes the caller
// perform it.
type Commit struct {
	SHA         string   `json:"sha"`
	AuthorName  string   `json:"author_name"`
	AuthorEmail string   `json:"author_email"`
	When        int64    `json:"when"`    // author time, Unix seconds
	Subject     string   `json:"subject"` // first line of the message
	Paths       []string `json:"paths"`   // repo-relative, index path space

	// ShallowBoundary marks a commit at the edge of a shallow clone: a parent
	// of it was never fetched, so the history ends here because of how the
	// repository was cloned and not because the project began here. An
	// adapter must carry it across, since a commit list shows no other sign:
	// a shallow clone's oldest commit looks exactly like a first commit.
	ShallowBoundary bool `json:"shallow_boundary,omitempty"`

	// Truncated marks the last commit of a walk that stopped because its time
	// budget ran out: older commits inside the requested limit exist and were
	// not read. Like ShallowBoundary, the list shows no other sign, and a
	// short list would otherwise read as the beginning of history.
	Truncated bool `json:"truncated,omitempty"`

	// Merge marks a commit with more than one parent; see
	// acquiregit.Commit.Merge. Its Paths repeat changes other commits in the
	// walk already carry.
	Merge bool `json:"merge,omitempty"`
}

// History is the read surface this package needs: recent commits with the paths
// they touched.
//
// One method. Everything else a routing report might want — per-file last
// modification, blame, a path-scoped log — is either derivable from this or too
// expensive to be worth exposing, and every method added here is a method a fake
// must implement before it can assert anything about ranking.
//
// limit is a bound, not a preference: reading paths costs one tree diff per
// commit, and an unbounded walk over a large history is the failure mode the
// whole design avoids. An implementation that cannot honour a bound should
// return an error rather than reading everything.
type History interface {
	Commits(ctx context.Context, limit int) ([]Commit, error)
}
