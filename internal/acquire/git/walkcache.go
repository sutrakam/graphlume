package git

import "sync"

// walkMemo remembers the last RecentCommits walk of one Repo.
//
// A commit never changes, so a walk from one HEAD is the same walk until HEAD
// moves. A long-lived Repo (the MCP server's, the web portal's) is asked for the
// same window by several tools in a row, and without this each of them paid
// for the walk again. Keyed on HEAD, replaced rather than merged when HEAD
// moves, and a walk with a larger limit answers a smaller one by truncation
// because commits come newest first.
type walkMemo struct {
	mu      sync.Mutex
	head    string
	limit   int
	commits []Commit
}

// get returns a copy of the remembered walk for head, cut to limit.
func (m *walkMemo) get(head string, limit int) ([]Commit, bool) {
	if head == "" {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// A walk that returned fewer commits than its limit reached the start of
	// history, and answers any limit.
	if m.head != head || (m.limit < limit && len(m.commits) >= m.limit) {
		return nil, false
	}
	cs := m.commits
	if len(cs) > limit {
		cs = cs[:limit]
	}
	return append([]Commit(nil), cs...), true
}

// put remembers a complete walk. A walk with a smaller limit than the one
// already held for the same HEAD does not replace it.
func (m *walkMemo) put(head string, limit int, commits []Commit) {
	if head == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.head == head && m.limit >= limit {
		return
	}
	m.head, m.limit, m.commits = head, limit, append([]Commit(nil), commits...)
}
