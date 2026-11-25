package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/store"
)

// An index artifact is a committed index as CI built it, packed so a developer
// or an agent can download it instead of indexing locally. It is two files:
// index.db, a consistent snapshot of the database, and manifest.json, which
// says what the snapshot is and carries its SHA-256.
//
// Transport is a GitHub Actions artifact named after the commit
// (graphlume-index-<full sha>), fetched with the caller's own `gh` login.
// There is no registry and no object store: whoever can read the repository's
// Actions artifacts can read the index, which is the access the index's
// contents (a structural map of the same source) already warrant.
const (
	indexArtifactFormat = "graphlume-index-artifact/v1"
	indexArtifactDB     = "index.db"
	indexArtifactMani   = "manifest.json"
	indexArtifactPrefix = "graphlume-index-"

	// defaultPullLookback bounds how many first-parent commits `index pull`
	// walks back looking for an artifact when the requested commit has none.
	defaultPullLookback = 50
)

// Size caps on what a downloaded artifact may contain. They are variables so
// the tests can lower them instead of building a multi-gigabyte zip.
//
// 4 GiB for the database is far above a real index (the largest measured is
// a few hundred megabytes) and far below what would fill a laptop disk from a
// hostile download. The manifest is a page of JSON, so 1 MiB is generous.
var (
	maxArtifactDBBytes       int64 = 4 << 30
	maxArtifactManifestBytes int64 = 1 << 20
)

// indexManifest is manifest.json. The field order is the declaration order, so
// two exports of the same snapshot differ only in created_at.
type indexManifest struct {
	Format           string `json:"format"`
	SchemaVersion    int    `json:"schema_version"`
	Commit           string `json:"commit"`
	Dirty            bool   `json:"dirty,omitempty"`
	CommittedRunID   int64  `json:"committed_run_id"`
	RegistryDigest   string `json:"registry_digest"`
	GraphlumeVersion string `json:"graphlume_version"`
	CreatedAt        string `json:"created_at"`
	Files            int64  `json:"files"`
	Symbols          int64  `json:"symbols"`
	Edges            int64  `json:"edges"`
	SHA256           string `json:"sha256"`
	Size             int64  `json:"size"`
}

// ---------------------------------------------------------------- export

func newIndexExportCmd(g *Global) *cobra.Command {
	var (
		out        string
		allowDirty bool
	)
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Write a shareable snapshot of the index (index.db and manifest.json)",
		Long: `Writes DIR/index.db, a consistent snapshot of the committed index, and
DIR/manifest.json, which names the commit it describes, the schema version, the
row counts and the SHA-256 of index.db. CI uploads that directory as a GitHub
Actions artifact named graphlume-index-<commit sha>; 'graphlume index pull'
downloads it.

The snapshot is taken with SQLite's VACUUM INTO, which reads one transaction's
view of the database. A concurrent indexer cannot tear it, and the live -wal
file is never copied.

The commit recorded is git HEAD. The export refuses a working tree with
uncommitted changes, and an index that was last built at a different commit,
because an artifact keyed by a commit that its contents do not match is worse
than no artifact. --allow-dirty overrides both and records "dirty": true.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if out == "" {
				return fmt.Errorf("%w: --out DIR is required", errInvalidArgument)
			}
			m, err := exportIndexArtifact(cmd.Context(), g, out, allowDirty)
			if err != nil {
				return err
			}
			if g.JSON {
				return emitJSON(cmd.OutOrStdout(), m)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"Exported the index at %s to %s (%d files, %d symbols, %d edges, %s, sha256 %s)\n",
				shortSHA(m.Commit), out, m.Files, m.Symbols, m.Edges, humanBytes(m.Size), m.SHA256[:12])
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "directory to write index.db and manifest.json into (required)")
	cmd.Flags().BoolVar(&allowDirty, "allow-dirty", false,
		"export even when the working tree has uncommitted changes or the index was built at another commit (recorded as dirty)")
	return cmd
}

func exportIndexArtifact(ctx context.Context, g *Global, outDir string, allowDirty bool) (*indexManifest, error) {
	root := g.Config().RepoRoot
	dbPath := g.DBPath()
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no index at %s — run 'graphlume index' first", dbPath)
	}

	head := headRevision(ctx, root)
	if head == "" {
		return nil, errors.New("index export needs a git checkout with at least one commit: the artifact is named after the commit it describes")
	}
	dirty, err := worktreeDirty(ctx, root)
	if err != nil {
		return nil, err
	}
	if dirty && !allowDirty {
		return nil, errors.New("the working tree has uncommitted changes, so the index does not describe " + shortSHA(head) +
			"; commit or stash them, or pass --allow-dirty to export anyway (the manifest will say dirty)")
	}

	src, err := openReadOnly(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = src.Close() }()

	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return nil, fmt.Errorf("create %s: %w", outDir, err)
	}
	tmpDB := filepath.Join(outDir, ".index.db.tmp")
	_ = os.Remove(tmpDB)
	_ = os.Remove(tmpDB + "-wal")
	_ = os.Remove(tmpDB + "-shm")
	defer func() {
		_ = os.Remove(tmpDB)
		_ = os.Remove(tmpDB + "-wal")
		_ = os.Remove(tmpDB + "-shm")
	}()

	// VACUUM INTO reads a single consistent snapshot and writes a fresh,
	// compacted file. Copying index.db instead would race a writer and miss
	// whatever still sits in the -wal file.
	if _, err := src.ExecContext(ctx, `VACUUM INTO ?`, tmpDB); err != nil {
		return nil, fmt.Errorf("snapshot the index: %w", err)
	}
	_ = src.Close()

	// Everything the manifest states is read back from the snapshot, not from
	// the live database, so the counts cannot disagree with the file they
	// describe if an indexer committed in between.
	snap, err := openReadOnly(ctx, tmpDB)
	if err != nil {
		return nil, fmt.Errorf("read back the snapshot: %w", err)
	}
	reg := registry.New(snap)
	run, err := reg.CurrentRun(ctx)
	if err != nil {
		_ = snap.Close()
		return nil, fmt.Errorf("the index has no committed run (run 'graphlume index' to finish building it): %w", err)
	}
	counts, err := reg.Counts(ctx, run.ID)
	if err != nil {
		_ = snap.Close()
		return nil, err
	}
	schema, err := store.SchemaVersion(ctx, snap.DB)
	_ = snap.Close()
	if err != nil {
		return nil, err
	}
	_ = os.Remove(tmpDB + "-wal")
	_ = os.Remove(tmpDB + "-shm")

	stale := run.GitRevision != head
	if stale && !allowDirty {
		return nil, fmt.Errorf("the index was built at %s but HEAD is %s; run 'graphlume index' to bring it up to date, "+
			"or pass --allow-dirty to export it anyway", orNone(shortSHA(run.GitRevision)), shortSHA(head))
	}

	sum, size, err := hashFile(tmpDB)
	if err != nil {
		return nil, err
	}
	m := &indexManifest{
		Format:           indexArtifactFormat,
		SchemaVersion:    schema,
		Commit:           head,
		Dirty:            dirty || stale,
		CommittedRunID:   run.ID,
		RegistryDigest:   run.RegistryDigest,
		GraphlumeVersion: Version,
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
		Files:            counts.Files,
		Symbols:          counts.Symbols,
		Edges:            counts.Edges,
		SHA256:           sum,
		Size:             size,
	}

	if err := os.Chmod(tmpDB, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmpDB, filepath.Join(outDir, indexArtifactDB)); err != nil {
		return nil, fmt.Errorf("place index.db: %w", err)
	}
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeAtomic(filepath.Join(outDir, indexArtifactMani), append(body, '\n')); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}
	return m, nil
}

func hashFile(path string) (sum string, size int64, err error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// worktreeDirty reports uncommitted changes to tracked files or untracked,
// non-ignored files. The index directory is excluded: it is ignored by its own
// .gitignore, but a project that never ran init has none, and an export must
// not call a tree dirty because of the index it is exporting.
func worktreeDirty(ctx context.Context, root string) (bool, error) {
	out, err := gitOutput(ctx, root, "status", "--porcelain", "--untracked-files=normal", "--", ".", ":(exclude)"+IndexDir)
	if err != nil {
		return false, fmt.Errorf("check the working tree: %w", err)
	}
	return strings.TrimSpace(out) != "", nil
}

func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...) //nolint:gosec // fixed git subcommands; root is the project directory
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func orNone(s string) string {
	if s == "" {
		return "no recorded commit"
	}
	return s
}

// ------------------------------------------------------------------ pull

type pullReport struct {
	Commit          string `json:"commit"`
	Requested       string `json:"requested"`
	CommitsBehind   int    `json:"commits_behind"`
	ArtifactID      int64  `json:"artifact_id"`
	IndexPath       string `json:"index_path"`
	SchemaVersion   int    `json:"schema_version"`
	Files           int64  `json:"files"`
	Symbols         int64  `json:"symbols"`
	Edges           int64  `json:"edges"`
	ReplacedExisted bool   `json:"replaced_existing"`
	Updated         bool   `json:"updated"`
}

func newIndexPullCmd(g *Global) *cobra.Command {
	var (
		commit   string
		repo     string
		force    bool
		update   bool
		lookback int
	)
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "Download the CI-built index for a commit instead of indexing locally",
		Long: `Downloads the index CI built for a commit (the Actions artifact
graphlume-index-<sha>), verifies it and installs it as this project's index.

It uses your own GitHub CLI login: run 'gh auth login' once. Nothing is sent
anywhere but GitHub, and no token is read or printed by graphlume.

The download is treated as untrusted. Before anything is installed it must
(1) contain only index.db and manifest.json, within the size caps (index.db at
most 4 GiB, the manifest 1 MiB); (2) match the SHA-256 and size in its manifest;
(3) name the commit that was asked for; (4) open read-only at exactly this
build's schema version, with no tables, triggers or views this build does not
create; (5) pass PRAGMA integrity_check; and (6) pass the graph invariants
'doctor --verify-index' runs. Only then is it moved into place, atomically.

The default commit is HEAD, and the repository is the origin remote. When that
commit has no artifact (CI has not finished, or the artifact expired), the
newest artifact among the previous commits on the first-parent line is used
(up to --lookback commits), and the command says how far behind it is.
'graphlume index' then catches the rest up incrementally, or pass --update to
run it right away.

An existing index is never replaced without --force. With --force it is kept as
index.db.bak until the new one is in place.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if lookback < 0 {
				return fmt.Errorf("%w: --lookback must not be negative", errInvalidArgument)
			}
			rep, err := pullIndexArtifact(cmd, g, pullOptions{
				Commit: commit, Repo: repo, Force: force, Lookback: lookback,
			})
			if err != nil {
				return err
			}
			if update {
				if err := runIncrementalAfterPull(cmd, g); err != nil {
					return err
				}
				rep.Updated = true
			}
			if g.JSON {
				return emitJSON(cmd.OutOrStdout(), rep)
			}
			w := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(w, "Installed the CI-built index for %s at %s (%d files, %d symbols, %d edges)\n",
				shortSHA(rep.Commit), rep.IndexPath, rep.Files, rep.Symbols, rep.Edges)
			if rep.CommitsBehind > 0 {
				_, _ = fmt.Fprintf(w, "No artifact exists for %s; this one is %d %s behind it. "+
					"Run 'graphlume index' to catch up incrementally (or use --update).\n",
					shortSHA(rep.Requested), rep.CommitsBehind, plural(rep.CommitsBehind, "commit", "commits"))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&commit, "commit", "", "commit to fetch the index for (default: HEAD)")
	cmd.Flags().StringVar(&repo, "repo", "", "GitHub repository as OWNER/NAME (default: the origin remote)")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing index (kept as index.db.bak until the new one is installed)")
	cmd.Flags().BoolVar(&update, "update", false, "after installing, run an incremental 'graphlume index' to catch up with the working tree")
	cmd.Flags().IntVar(&lookback, "lookback", defaultPullLookback, "how many earlier first-parent commits to search when the commit has no artifact")
	return cmd
}

type pullOptions struct {
	Commit   string
	Repo     string
	Force    bool
	Lookback int
}

// ghArtifact is the part of a GitHub artifact listing entry we use.
type ghArtifact struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Expired     bool   `json:"expired"`
	SizeInBytes int64  `json:"size_in_bytes"`
	CreatedAt   string `json:"created_at"`
	WorkflowRun struct {
		HeadSHA string `json:"head_sha"`
	} `json:"workflow_run"`
}

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

func pullIndexArtifact(cmd *cobra.Command, g *Global, opt pullOptions) (*pullReport, error) {
	ctx := cmd.Context()
	root := g.Config().RepoRoot
	dbPath := projectIndexPath(root)

	// Refuse before any network traffic: nobody should wait for a download
	// only to be told the index they already have is in the way.
	if fileExists(dbPath) && !opt.Force {
		return nil, fmt.Errorf("an index already exists at %s; pass --force to replace it "+
			"(the current one is kept as %s until the new one is installed)", dbPath, filepath.Base(dbPath)+".bak")
	}

	gh, err := newGHClient(ctx)
	if err != nil {
		return nil, err
	}

	repo := opt.Repo
	if repo == "" {
		repo, gh.host, err = originRepo(ctx, root)
		if err != nil {
			return nil, err
		}
	} else if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(repo) {
		return nil, fmt.Errorf("%w: --repo %q: want OWNER/NAME", errInvalidArgument, repo)
	}

	want, history, err := resolvePullCommit(ctx, root, opt.Commit, opt.Lookback)
	if err != nil {
		return nil, err
	}

	art, behind, found, sawExpired, err := findArtifact(ctx, gh, repo, want, history)
	if err != nil {
		return nil, err
	}
	if !found {
		if sawExpired {
			return nil, fmt.Errorf("the index artifact for %s has expired (GitHub deletes artifacts after their retention period); "+
				"re-run the CI index job for that commit, or run 'graphlume index' to build locally", shortSHA(want))
		}
		return nil, fmt.Errorf("no index artifact %s%s found in %s, nor for the %d earlier first-parent commits; "+
			"CI has not uploaded one (see the upload-index input of the GraphLume action) — run 'graphlume index' to build locally",
			indexArtifactPrefix, shortSHA(want), repo, len(history))
	}
	artCommit := want
	if behind > 0 {
		artCommit = history[behind-1]
	}

	stageParent := filepath.Dir(dbPath)
	if err := os.MkdirAll(stageParent, 0o750); err != nil {
		return nil, fmt.Errorf("create %s: %w", stageParent, err)
	}
	if err := writeIfAbsent(filepath.Join(stageParent, ".gitignore"), "*\n"); err != nil {
		return nil, err
	}
	// Staged inside the index directory so the final rename never crosses a
	// filesystem, which would turn the atomic install into a copy.
	stage, err := os.MkdirTemp(stageParent, ".pull-")
	if err != nil {
		return nil, fmt.Errorf("create a staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()

	zipPath := filepath.Join(stage, "artifact.zip")
	if err := gh.download(ctx, repo, art.ID, zipPath); err != nil {
		return nil, err
	}
	extractDir := filepath.Join(stage, "x")
	if err := os.Mkdir(extractDir, 0o700); err != nil {
		return nil, err
	}
	m, err := extractIndexArtifact(zipPath, extractDir)
	if err != nil {
		return nil, err
	}
	if m.Commit != artCommit {
		return nil, fmt.Errorf("refusing the download: its manifest says it describes %s but the artifact was looked up for %s",
			shortSHA(m.Commit), shortSHA(artCommit))
	}
	candidate := filepath.Join(extractDir, indexArtifactDB)
	counts, err := verifyIndexCandidate(cmd, candidate, m)
	if err != nil {
		return nil, err
	}

	unlock, err := lockIndex(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	defer unlock()
	replaced := fileExists(dbPath)
	if replaced && !opt.Force {
		return nil, fmt.Errorf("an index appeared at %s while downloading; pass --force to replace it", dbPath)
	}
	if err := installIndex(ctx, candidate, dbPath); err != nil {
		return nil, err
	}
	return &pullReport{
		Commit: artCommit, Requested: want, CommitsBehind: behind, ArtifactID: art.ID,
		IndexPath: dbPath, SchemaVersion: m.SchemaVersion,
		Files: counts.Files, Symbols: counts.Symbols, Edges: counts.Edges,
		ReplacedExisted: replaced,
	}, nil
}

// runIncrementalAfterPull is `graphlume index` run in-process on the freshly
// installed database, for --update.
func runIncrementalAfterPull(cmd *cobra.Command, g *Global) error {
	idx := newIndexCmd(g)
	idx.SetContext(cmd.Context())
	idx.SetOut(cmd.OutOrStdout())
	idx.SetErr(cmd.ErrOrStderr())
	if err := idx.RunE(idx, nil); err != nil {
		return fmt.Errorf("the index was installed, but updating it failed: %w", err)
	}
	return nil
}

// resolvePullCommit returns the full SHA to fetch and the first-parent commits
// before it (nearest first, at most lookback of them). The history is empty
// when the commit is not in the local repository, so only an exact match works.
func resolvePullCommit(ctx context.Context, root, requested string, lookback int) (string, []string, error) {
	rev := requested
	if rev == "" {
		rev = "HEAD"
	}
	out, err := gitOutput(ctx, root, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	sha := strings.TrimSpace(out)
	if err != nil || sha == "" {
		if fullSHA.MatchString(strings.ToLower(requested)) {
			return strings.ToLower(requested), nil, nil
		}
		if requested == "" {
			return "", nil, errors.New("pull needs a git checkout with a commit, or an explicit --commit <full sha>")
		}
		return "", nil, fmt.Errorf("--commit %q is not a commit in this repository; give a full 40-character SHA to fetch one that is not checked out", requested)
	}
	var history []string
	if lookback > 0 {
		list, err := gitOutput(ctx, root, "rev-list", "--first-parent", fmt.Sprintf("--max-count=%d", lookback+1), sha)
		if err == nil {
			for _, l := range strings.Fields(list) {
				if l != sha {
					history = append(history, l)
				}
			}
		}
	}
	return sha, history, nil
}

// findArtifact looks for the artifact of want, then of each earlier commit in
// order. behind is 0 for an exact match and otherwise the 1-based position of
// the match in history.
func findArtifact(ctx context.Context, gh *ghClient, repo, want string, history []string) (art ghArtifact, behind int, found, sawExpired bool, err error) {
	candidates := append([]string{want}, history...)
	for i, sha := range candidates {
		a, ok, expired, e := gh.lookup(ctx, repo, sha)
		if e != nil {
			return ghArtifact{}, 0, false, sawExpired, e
		}
		if expired {
			sawExpired = true
		}
		if ok {
			return a, i, true, sawExpired, nil
		}
	}
	return ghArtifact{}, 0, false, sawExpired, nil
}

// ------------------------------------------------------------- gh access

type ghClient struct {
	path string
	host string
}

// newGHClient finds gh and checks it is logged in. Both failures name the one
// command that fixes them, because "gh: exit status 1" sends nobody anywhere.
func newGHClient(ctx context.Context) (*ghClient, error) {
	p, err := exec.LookPath("gh")
	if err != nil {
		return nil, errors.New("the GitHub CLI (gh) is not installed or not on PATH; install it from https://cli.github.com, " +
			"then run 'gh auth login' — or run 'graphlume index' to build the index locally")
	}
	c := &ghClient{path: p}
	if _, _, err := c.run(ctx, "", "auth", "status"); err != nil {
		return nil, errors.New("the GitHub CLI is not logged in; run 'gh auth login', then try again " +
			"(graphlume uses your gh login and never reads or prints the token)")
	}
	return c, nil
}

func (c *ghClient) run(ctx context.Context, outFile string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, c.path, args...) //nolint:gosec // gh is the user's own CLI; args are fixed API paths
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1")
	if c.host != "" && c.host != "github.com" {
		cmd.Env = append(cmd.Env, "GH_HOST="+c.host)
	}
	var so, se bytes.Buffer
	cmd.Stderr = &se
	if outFile != "" {
		f, ferr := os.OpenFile(filepath.Clean(outFile), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if ferr != nil {
			return "", "", ferr
		}
		defer func() { _ = f.Close() }()
		// The cap applies while writing, not after: a hostile or runaway
		// response must not fill the disk before anyone looks at its size.
		cmd.Stdout = &capWriter{w: f, remaining: maxArtifactDBBytes + maxArtifactManifestBytes + (1 << 20)}
	} else {
		cmd.Stdout = &so
	}
	err = cmd.Run()
	return so.String(), se.String(), err
}

type capWriter struct {
	w         io.Writer
	remaining int64
}

var errCapExceeded = errors.New("download is larger than the size cap")

func (c *capWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > c.remaining {
		return 0, errCapExceeded
	}
	c.remaining -= int64(len(p))
	return c.w.Write(p)
}

// lookup finds the newest unexpired artifact for sha. expired reports that
// artifacts for sha exist but all have expired.
func (c *ghClient) lookup(ctx context.Context, repo, sha string) (a ghArtifact, ok, expired bool, err error) {
	name := indexArtifactPrefix + sha
	stdout, stderr, err := c.run(ctx, "", "api", fmt.Sprintf("repos/%s/actions/artifacts?name=%s&per_page=100", repo, name))
	if err != nil {
		return ghArtifact{}, false, false, ghAPIError(repo, stderr, err)
	}
	var resp struct {
		Artifacts []ghArtifact `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(stdout), &resp); err != nil {
		return ghArtifact{}, false, false, fmt.Errorf("could not read GitHub's artifact listing: %w", err)
	}
	var best *ghArtifact
	for i := range resp.Artifacts {
		x := &resp.Artifacts[i]
		if x.Name != name {
			continue
		}
		// An artifact is only as trustworthy as the run that produced it. The
		// name is chosen by the uploader, so the run's own head commit must
		// agree with it.
		if x.WorkflowRun.HeadSHA != "" && !strings.EqualFold(x.WorkflowRun.HeadSHA, sha) {
			continue
		}
		if x.Expired {
			expired = true
			continue
		}
		if best == nil || x.CreatedAt > best.CreatedAt {
			best = x
		}
	}
	if best == nil {
		return ghArtifact{}, false, expired, nil
	}
	return *best, true, expired, nil
}

func (c *ghClient) download(ctx context.Context, repo string, id int64, dst string) error {
	_, stderr, err := c.run(ctx, dst, "api", fmt.Sprintf("repos/%s/actions/artifacts/%d/zip", repo, id))
	if err != nil {
		if errors.Is(err, errCapExceeded) || strings.Contains(err.Error(), errCapExceeded.Error()) {
			return fmt.Errorf("refusing the download: it is larger than the %s cap", humanBytes(maxArtifactDBBytes))
		}
		if strings.Contains(stderr, "410") || strings.Contains(strings.ToLower(stderr), "expired") {
			return errors.New("the index artifact has expired; re-run the CI index job, or run 'graphlume index' to build locally")
		}
		return ghAPIError(repo, stderr, err)
	}
	return nil
}

// ghAPIError turns a failed gh call into advice. Only the first line of gh's
// stderr is shown: it carries the HTTP status and message, never a credential.
func ghAPIError(repo, stderr string, err error) error {
	first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(stderr), "\n", 2)[0])
	lower := strings.ToLower(stderr)
	switch {
	case strings.Contains(lower, "gh auth login") || strings.Contains(lower, "http 401"):
		return errors.New("the GitHub CLI is not authorised; run 'gh auth login' (or 'gh auth refresh') and try again")
	case strings.Contains(lower, "http 404"):
		return fmt.Errorf("GitHub says %s was not found, or your gh login cannot read its Actions artifacts "+
			"(needs repo access and the 'repo' scope for private repositories): %s", repo, first)
	}
	if first == "" {
		first = err.Error()
	}
	return fmt.Errorf("gh api failed: %s", first)
}

var remoteRepo = regexp.MustCompile(`^(?:https?://(?:[^@/]+@)?|ssh://(?:[^@/]+@)?|[^@/\s]+@)([^/:]+)[:/](?:\d+/)?([^/\s]+)/([^/\s]+?)(?:\.git)?/?$`)

// originRepo reads OWNER/NAME and the host from the origin remote.
func originRepo(ctx context.Context, root string) (repo, host string, err error) {
	out, err := gitOutput(ctx, root, "remote", "get-url", "origin")
	if err != nil {
		return "", "", errors.New("could not read the origin remote; pass --repo OWNER/NAME")
	}
	m := remoteRepo.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return "", "", fmt.Errorf("could not read OWNER/NAME from the origin remote %q; pass --repo OWNER/NAME", strings.TrimSpace(out))
	}
	return m[2] + "/" + m[3], m[1], nil
}

// ------------------------------------------------------------ extraction

// extractIndexArtifact unpacks the downloaded zip into dir and returns its
// manifest after checking the database against it.
//
// The zip is hostile input. Only the two expected names are accepted, so a
// path with "..", an absolute path, a subdirectory, a symlink or any extra
// entry is refused rather than sanitised: nothing legitimate produces them.
// File contents are written to names this function chooses, never to a name
// taken from the archive, and the size caps are enforced on the bytes actually
// read, since the sizes a zip declares are the attacker's to state.
func extractIndexArtifact(zipPath, dir string) (*indexManifest, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("the downloaded artifact is not a valid zip: %w", err)
	}
	defer func() { _ = zr.Close() }()

	seen := map[string]bool{}
	for _, f := range zr.File {
		name := f.Name
		if name != indexArtifactDB && name != indexArtifactMani {
			return nil, fmt.Errorf("refusing the artifact: unexpected entry %q (only %s and %s are allowed)",
				name, indexArtifactDB, indexArtifactMani)
		}
		if seen[name] {
			return nil, fmt.Errorf("refusing the artifact: %q appears twice", name)
		}
		seen[name] = true
		if f.Mode()&os.ModeType != 0 || f.FileInfo().IsDir() {
			return nil, fmt.Errorf("refusing the artifact: %q is not a regular file", name)
		}
		limit := maxArtifactDBBytes
		if name == indexArtifactMani {
			limit = maxArtifactManifestBytes
		}
		if f.UncompressedSize64 > uint64(limit) { //nolint:gosec // limit is positive
			return nil, fmt.Errorf("refusing the artifact: %s declares a size over the %s cap", name, humanBytes(limit))
		}
		// name is one of two fixed file names (checked above), so it cannot leave dir.
		if err := extractEntry(f, filepath.Join(dir, name), limit); err != nil { //nolint:gosec // G305: see above
			return nil, err
		}
	}
	if !seen[indexArtifactDB] || !seen[indexArtifactMani] {
		return nil, fmt.Errorf("refusing the artifact: it must contain both %s and %s", indexArtifactDB, indexArtifactMani)
	}

	raw, err := os.ReadFile(filepath.Join(dir, indexArtifactMani)) //nolint:gosec // G304: a fixed name in the extraction directory
	if err != nil {
		return nil, err
	}
	var m indexManifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("refusing the artifact: manifest.json is not valid: %w", err)
	}
	if m.Format != indexArtifactFormat {
		return nil, fmt.Errorf("refusing the artifact: its format is %q, this build reads %q — upgrade graphlume", m.Format, indexArtifactFormat)
	}
	if !fullSHA.MatchString(m.Commit) {
		return nil, errors.New("refusing the artifact: the manifest names no valid commit")
	}
	sum, size, err := hashFile(filepath.Join(dir, indexArtifactDB))
	if err != nil {
		return nil, err
	}
	if size != m.Size {
		return nil, fmt.Errorf("refusing the artifact: index.db is %d bytes but the manifest says %d", size, m.Size)
	}
	if !strings.EqualFold(sum, m.SHA256) {
		return nil, fmt.Errorf("refusing the artifact: SHA-256 mismatch (index.db is %s, the manifest says %s); the download is corrupt or was altered",
			shortSHA(sum), shortSHA(m.SHA256))
	}
	return &m, nil
}

func extractEntry(f *zip.File, dst string, limit int64) (err error) {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("read %s from the artifact: %w", f.Name, err)
	}
	defer func() { _ = rc.Close() }()
	out, err := os.OpenFile(filepath.Clean(dst), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	n, err := io.Copy(out, io.LimitReader(rc, limit+1))
	if err != nil {
		return fmt.Errorf("unpack %s: %w", f.Name, err)
	}
	if n > limit {
		return fmt.Errorf("refusing the artifact: %s expands past the %s cap", f.Name, humanBytes(limit))
	}
	return nil
}

// ---------------------------------------------------------- verification

// schemaMismatchError carries the typed store error so callers can still
// errors.As it, with advice worded for a download rather than a local index.
type schemaMismatchError struct {
	msg   string
	cause error
}

func (e *schemaMismatchError) Error() string { return e.msg }
func (e *schemaMismatchError) Unwrap() error { return e.cause }

// verifyIndexCandidate decides whether a downloaded database may be installed.
// The file is opened read-only throughout, and nothing here writes to it.
func verifyIndexCandidate(cmd *cobra.Command, path string, m *indexManifest) (registry.Counts, error) {
	ctx := cmd.Context()
	var zero registry.Counts

	db, err := openReadOnly(ctx, path)
	if err != nil {
		var newer *store.ErrSchemaNewer
		var older *store.ErrSchemaOutdated
		switch {
		case errors.As(err, &newer):
			return zero, &schemaMismatchError{cause: err, msg: fmt.Sprintf(
				"the downloaded index uses schema v%d, newer than this build's v%d: upgrade graphlume, "+
					"or run 'graphlume index' to build locally", newer.Found, newer.Latest)}
		case errors.As(err, &older):
			return zero, &schemaMismatchError{cause: err, msg: fmt.Sprintf(
				"the downloaded index uses schema v%d, older than this build's v%d: CI ran an older graphlume — "+
					"re-run it with this version, or run 'graphlume index' to build locally", older.Found, older.Wanted)}
		}
		return zero, fmt.Errorf("the downloaded index cannot be opened: %w", err)
	}
	defer func() { _ = db.Close() }()

	found, err := store.SchemaVersion(ctx, db.DB)
	if err != nil {
		return zero, err
	}
	if found != m.SchemaVersion {
		return zero, fmt.Errorf("refusing the artifact: the manifest says schema v%d but the database is v%d", m.SchemaVersion, found)
	}
	if err := checkSchemaObjects(ctx, db); err != nil {
		return zero, err
	}

	// integrity_check, not quick_check: this runs once per download, and it is
	// the one step that looks at index content for structural damage a
	// matching checksum cannot reveal (the checksum comes from the same zip).
	var verdict string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check(1)`).Scan(&verdict); err != nil {
		return zero, fmt.Errorf("the downloaded index failed its integrity check: %w", err)
	}
	if verdict != "ok" {
		return zero, fmt.Errorf("the downloaded index failed its integrity check: %s", verdict)
	}

	reg := registry.New(db)
	run, err := reg.CurrentRun(ctx)
	if err != nil {
		return zero, fmt.Errorf("the downloaded index has no committed run: %w", err)
	}
	counts, err := reg.Counts(ctx, run.ID)
	if err != nil {
		return zero, err
	}
	if counts.Files != m.Files || counts.Symbols != m.Symbols || counts.Edges != m.Edges {
		return zero, fmt.Errorf("refusing the artifact: the manifest says %d files, %d symbols, %d edges but the database holds %d, %d, %d",
			m.Files, m.Symbols, m.Edges, counts.Files, counts.Symbols, counts.Edges)
	}
	_ = db.Close()

	// The same invariant gate as 'doctor --verify-index'.
	var bad []string
	for _, c := range verifyGraph(cmd, path) {
		if c.Status == "fail" {
			bad = append(bad, c.Name+": "+c.Detail)
		}
	}
	if len(bad) > 0 {
		return zero, fmt.Errorf("the downloaded index failed the graph invariants (%s); it was not installed — "+
			"run 'graphlume index' to build locally", strings.Join(bad, "; "))
	}
	return counts, nil
}

// checkSchemaObjects refuses a database that carries schema objects this build
// does not create. The installed file is later opened for writing, and a
// trigger or view planted in it would run then, with this process's rights, so
// a read-only open during verification is not enough on its own.
//
// Tables and indexes only need to be known by name (an index set aside by an
// interrupted first index is reported by doctor, not refused here); triggers,
// views and virtual tables are executable and must match this build's text.
func checkSchemaObjects(ctx context.Context, db *store.DB) error {
	fresh, err := openMigratedMemory(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = fresh.Close() }()
	want, err := schemaObjects(ctx, fresh)
	if err != nil {
		return err
	}
	have, err := schemaObjects(ctx, db)
	if err != nil {
		return err
	}
	for key, sqlText := range have {
		wantSQL, known := want[key]
		if !known {
			return fmt.Errorf("refusing the artifact: it contains %s, which this build does not create", key)
		}
		executable := strings.HasPrefix(key, "trigger ") || strings.HasPrefix(key, "view ") ||
			strings.Contains(strings.ToUpper(sqlText), "VIRTUAL")
		if executable && normSQL(sqlText) != normSQL(wantSQL) {
			return fmt.Errorf("refusing the artifact: %s differs from this build's definition", key)
		}
	}
	return nil
}

func normSQL(s string) string { return strings.Join(strings.Fields(s), " ") }

func schemaObjects(ctx context.Context, db *store.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT type, name, COALESCE(sql, '') FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, fmt.Errorf("read the schema: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var typ, name, sqlText string
		if err := rows.Scan(&typ, &name, &sqlText); err != nil {
			return nil, err
		}
		out[typ+" "+name] = sqlText
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- install

// installIndex moves a verified database into place. The old index, if any, is
// kept as index.db.bak until the new one is renamed in, and put back if that
// fails, so a failed pull never leaves the project without an index.
func installIndex(ctx context.Context, candidate, dbPath string) error {
	bak := dbPath + ".bak"
	hadOld := fileExists(dbPath)
	if hadOld {
		// Fold the old index's -wal into it first so the backup is the whole
		// index. Best effort: a damaged old index is being replaced anyway.
		checkpointForBackup(ctx, dbPath)
		_ = os.Remove(bak)
		if err := os.Rename(dbPath, bak); err != nil {
			return fmt.Errorf("set the existing index aside: %w", err)
		}
	}
	// A -wal or -shm left from the old index must not be paired with the new
	// file: SQLite would replay the old log into it.
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	if err := os.Rename(candidate, dbPath); err != nil {
		if hadOld {
			_ = os.Rename(bak, dbPath)
		}
		return fmt.Errorf("install the index: %w", err)
	}
	_ = os.Remove(candidate + "-wal")
	_ = os.Remove(candidate + "-shm")
	if hadOld {
		_ = os.Remove(bak)
	}
	return nil
}
