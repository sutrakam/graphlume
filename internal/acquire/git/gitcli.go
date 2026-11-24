package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// History through the git binary, for repositories go-git declines.
//
// go-git refuses a repository that uses an extension it does not implement,
// and `extensions.worktreeConfig` is the common one: git turns it on for
// repositories with linked worktrees. Such a repository opens in degraded mode
// (HEAD is read from the files), and before this every history-backed answer
// on it was empty: hotspots, bus factor, owners, blame and co-change. Measured
// on a 25,946-file monorepo with seven worktrees, all five reported
// "No history was read".
//
// The binary is used only in that case, and only to read. Every option that
// can make `git log` run a program the repository configures is turned off:
// fsmonitor hooks, signature verification (gpg), external diff drivers and
// textconv filters. Paths follow the go-git walk's rules exactly: diffs
// against the first parent only, renames not detected, and no paths for a
// commit at a shallow clone's boundary (see Commit.Paths).

// gitBinary is the git executable, or "" when none is on PATH. A variable so
// a test can make it absent.
var gitBinary = func() string {
	p, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	return p
}

// binaryProbe remembers whether the git binary accepts a repository, so the
// check runs once per Repo rather than once per history call.
type binaryProbe struct {
	once sync.Once
	ok   bool
}

// historyViaBinary reports whether history for this repository is read with
// the git binary: go-git declined it, a git executable is available, and git
// itself accepts the directory. The last check matters: a .git with no object
// store is refused by go-git and by git alike, and must stay "not a
// repository" rather than read as one with no commits.
func (r *Repo) historyViaBinary() bool {
	if r.repo != nil || r.degraded == "" {
		return false
	}
	return r.binaryAccepts()
}

// binaryAccepts reports whether a git executable is on PATH and accepts this
// repository. The check runs once per Repo.
func (r *Repo) binaryAccepts() bool {
	if !r.isRepo || r.probe == nil || gitBinary() == "" {
		return false
	}
	r.probe.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := r.gitReadOnly(ctx, "rev-parse", "--git-dir")
		r.probe.ok = err == nil
	})
	return r.probe.ok
}

// historyWalkEnv, set to "go-git", keeps the bulk commit walk on go-git even
// when a git binary is available. It exists so the two readers can be compared
// on the same repository; nothing sets it in normal use.
const historyWalkEnv = "GRAPHLUME_HISTORY_WALK"

// walkViaBinary reports whether the bulk commit walk (RecentCommits and its
// variants) uses the git binary.
//
// It does whenever one is available, not only for repositories go-git
// declines. go-git reads every tree object through its own packfile lookup,
// and on a repository with many packfiles that lookup is the whole cost: 46 s
// for 200 commits on a large never-repacked monorepo, where `git log
// --name-only` read 500 in under 2 s. The answer is the same (both diff
// against the first parent, without rename detection, and leave a shallow
// boundary commit without paths), and TestRecentCommitsBinaryMatchesGoGit
// compares them commit by commit.
func (r *Repo) walkViaBinary() bool {
	if r.historyViaBinary() {
		return true
	}
	if r.repo == nil || os.Getenv(historyWalkEnv) == "go-git" {
		return false
	}
	return r.binaryAccepts()
}

// HistoryAvailable reports whether commit history can be read at all.
func (r *Repo) HistoryAvailable() bool {
	return r.isRepo && (r.repo != nil || r.historyViaBinary())
}

// HistorySource says how history is read: "go-git", "git binary" or "".
func (r *Repo) HistorySource() string {
	switch {
	case r.isRepo && r.repo != nil:
		return "go-git"
	case r.historyViaBinary():
		return "git binary"
	}
	return ""
}

// gitReadOnly runs a read-only git command in the repository and returns its
// standard output.
func (r *Repo) gitReadOnly(ctx context.Context, args ...string) ([]byte, error) {
	bin := gitBinary()
	if bin == "" {
		return nil, errors.New("git: no git executable on PATH")
	}
	full := append([]string{
		"-C", r.root,
		"-c", "core.fsmonitor=false",
		"-c", "log.showSignature=false",
		"--no-pager",
	}, args...)
	cmd := exec.CommandContext(ctx, bin, full...) //nolint:gosec // G204: fixed git subcommands with arguments this package builds
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(firstLineOf(stderr.String())))
	}
	return out, nil
}

// gitStream starts a read-only git command and returns its standard output as
// a stream. wait reaps the process and reports its failure, if any; call it
// exactly once, after the stream is read or the context cancelled.
func (r *Repo) gitStream(ctx context.Context, args ...string) (io.Reader, func() error, error) {
	bin := gitBinary()
	if bin == "" {
		return nil, nil, errors.New("git: no git executable on PATH")
	}
	full := append([]string{
		"-C", r.root,
		"-c", "core.fsmonitor=false",
		"-c", "log.showSignature=false",
		"-c", "core.quotePath=false",
		"--no-pager",
	}, args...)
	cmd := exec.CommandContext(ctx, bin, full...) //nolint:gosec // G204: fixed git subcommands with arguments this package builds
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	wait := func() error {
		// Drain what is left so the process is never blocked on a full pipe
		// when it was not cancelled.
		_, _ = io.Copy(io.Discard, out)
		if err := cmd.Wait(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(firstLineOf(stderr.String())))
		}
		return nil
	}
	return out, wait, nil
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

const (
	recordSep = "\x1e"
	fieldSep  = "\x1f"
	bodyEnd   = "\x1d"
)

// recentCommitsCLI is recentCommits read with the git binary.
//
// The output is read as it streams, one commit record at a time, so a walk
// that runs past its deadline stops there with what it has (complete=false)
// instead of buffering a log the caller can no longer use. skip is passed to
// git as --skip, which counts in the same order the walk returns.
func (r *Repo) recentCommitsCLI(ctx context.Context, limit int, withPaths bool, skip int, deadline time.Time) ([]Commit, bool, error) {
	if _, err := r.gitReadOnly(ctx, "rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return nil, true, nil // no commits yet, the same answer the go-git walk gives
	}
	args := []string{"log", "-n", strconv.Itoa(limit),
		"--no-show-signature", "--no-ext-diff", "--no-textconv", "--no-renames",
		"--format=" + recordSep + "%H" + fieldSep + "%an" + fieldSep + "%ae" + fieldSep + "%at" + fieldSep + "%P" + fieldSep + "%B" + bodyEnd,
	}
	if skip > 0 {
		args = append(args, "--skip="+strconv.Itoa(skip))
	}
	if withPaths {
		args = append(args, "--name-only", "--diff-merges=first-parent")
	}
	args = append(args, "HEAD", "--")

	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, wait, err := r.gitStream(wctx, args...)
	if err != nil {
		return nil, false, err
	}
	boundary := r.shallowSet()
	commits := make([]Commit, 0, min(limit, 1024))
	complete := true

	rd := bufio.NewReaderSize(stream, 256*1024)
	// Each record starts with recordSep, so a record is complete when the next
	// one begins or the stream ends.
	if _, err := rd.ReadString(recordSep[0]); err != nil && !errors.Is(err, io.EOF) {
		cancel()
		_ = wait()
		return nil, false, fmt.Errorf("git log: read: %w", err)
	}
	for {
		rec, rerr := rd.ReadString(recordSep[0])
		rec = strings.TrimSuffix(rec, recordSep)
		if strings.TrimSpace(rec) != "" {
			c, perr := parseRecord(rec, withPaths, boundary)
			if perr != nil {
				cancel()
				_ = wait()
				return nil, false, perr
			}
			commits = append(commits, c)
		}
		if rerr != nil {
			if !errors.Is(rerr, io.EOF) {
				cancel()
				_ = wait()
				return nil, false, fmt.Errorf("git log: read: %w", rerr)
			}
			break
		}
		if err := ctx.Err(); err != nil {
			cancel()
			_ = wait()
			return nil, false, err
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			// The record just parsed is whole; the one being read is not.
			complete = false
			cancel()
			_ = wait()
			return commits, complete, nil
		}
	}
	if err := wait(); err != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return nil, false, err
	}
	return commits, complete, nil
}

// parseRecord parses one `git log` record written by the format above.
func parseRecord(rec string, withPaths bool, boundary map[string]bool) (Commit, error) {
	head, rest, _ := strings.Cut(rec, bodyEnd)
	f := strings.SplitN(head, fieldSep, 6)
	if len(f) < 6 {
		return Commit{}, fmt.Errorf("git log: unexpected record %q", firstLineOf(rec))
	}
	when, _ := strconv.ParseInt(f[3], 10, 64)
	c := Commit{
		SHA: f[0], AuthorName: f[1], AuthorEmail: f[2], When: when,
		Subject:         subject(f[5]),
		Constraints:     extractAntiRegressionConstraints(f[5]),
		ShallowBoundary: boundary[f[0]],
		// %P lists the parents a shallow clone fetched; a boundary commit
		// shows none, and is not a merge for that.
		Merge: len(strings.Fields(f[4])) > 1,
	}
	// A boundary commit has no parent to diff against; git would list its
	// whole tree as "added", which credits one author with everything.
	if withPaths && !c.ShallowBoundary {
		c.Paths = parsePaths(rest)
	}
	return c, nil
}

func parsePaths(block string) []string {
	var paths []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(block))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		p := strings.TrimSpace(sc.Text())
		// A path with a quote, a backslash or a control character is still
		// C-quoted under core.quotePath=false; the go-git walk reports it raw.
		if len(p) >= 2 && p[0] == '"' && p[len(p)-1] == '"' {
			if uq, err := strconv.Unquote(p); err == nil {
				p = uq
			}
		}
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// fileLastModifiedCLI is FileLastModified read with the git binary.
func (r *Repo) fileLastModifiedCLI(ctx context.Context, path string) (FileRevision, error) {
	out, err := r.gitReadOnly(ctx, "log", "-1", "--no-show-signature", "--format=%H"+fieldSep+"%at", "HEAD", "--", path)
	if err != nil {
		return FileRevision{}, err
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		if r.Shallow() {
			return FileRevision{}, ErrShallowHistory
		}
		return FileRevision{}, ErrNoHistory
	}
	sha, ts, _ := strings.Cut(line, fieldSep)
	when, _ := strconv.ParseInt(ts, 10, 64)
	return FileRevision{SHA: sha, When: when}, nil
}

// shallowSet is the set of commits at a shallow clone's boundary, read from
// the shallow file git keeps (in the common directory for a linked worktree).
func (r *Repo) shallowSet() map[string]bool {
	set := map[string]bool{}
	if r.gitDir == "" {
		return set
	}
	dirs := []string{r.gitDir}
	if raw, err := os.ReadFile(filepath.Join(r.gitDir, "commondir")); err == nil { //nolint:gosec // G304: a fixed file inside the repository's git directory
		common := strings.TrimSpace(string(raw))
		if !filepath.IsAbs(common) {
			common = filepath.Join(r.gitDir, common)
		}
		dirs = append(dirs, common)
	}
	for _, d := range dirs {
		raw, err := os.ReadFile(filepath.Join(d, "shallow")) //nolint:gosec // G304: a fixed file inside the repository's git directory
		if err != nil {
			continue
		}
		for _, l := range strings.Fields(string(raw)) {
			set[l] = true
		}
	}
	return set
}
