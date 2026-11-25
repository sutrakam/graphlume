// Package fs is the acquisition layer's front door: it decides which files in a
// repository are worth indexing and fingerprints them.
//
// Two properties govern the design.
//
//  1. THE REPOSITORY IS UNTRUSTED. Everything this package reads — directory
//     entries, symlink targets, .gitignore rules, .graphlume.yml globs — is
//     attacker-controlled the moment someone clones a hostile repo. So symlinks
//     are never followed out of the root, config Exclude patterns cannot be
//     negated away, and ignore-file parsing is fuzzed. Spec §33.
//
//  2. OUTPUT MUST BE BYTE-FOR-BYTE REPRODUCIBLE. A determinism gate downstream
//     compares two index runs of the same tree. The walk is concurrent, so
//     discovery order is nondeterministic by construction; results are
//     therefore sorted by path before they leave Walk. Nothing else in the
//     pipeline is allowed to depend on arrival order.
//
// Note the import alias: this package is named fs, so the standard library's
// io/fs is imported as iofs.
package fs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/langdetect"
)

// DefaultMaxFileSize is the largest file that will be hashed and offered to the
// parsers. Generated bundles and checked-in datasets dominate the tail of every
// real repository and yield nothing useful per byte spent.
//
// It is the same number as parse.DefaultMaxSourceBytes on purpose: there is one
// size limit. A file under it is read, hashed, parsed and text-indexed; a file
// over it is recorded as skipped with reason too_large and its size, and is
// named by `graphlume index` and `graphlume status`. A repository changes the
// limit with index.max_file_bytes in .graphlume.yml (config.Config.MaxFileBytes).
const DefaultMaxFileSize int64 = config.DefaultMaxFileBytes

// binarySniffLen is git's own heuristic window: a NUL byte within the first
// 8000 bytes means binary. Matching git matters more than being clever here,
// because users reason about GraphLume's file set by comparing it to git's.
const binarySniffLen = 8000

// Skip reasons. These are stable identifiers, not prose: they end up in the
// index and in `graphlume doctor` output, so changing one is a schema change.
const (
	SkipTooLarge      = "too_large"
	SkipBinary        = "binary"
	SkipSymlink       = "symlink"
	SkipSymlinkEscape = "symlink_escapes_root"
	SkipIrregular     = "irregular"
	SkipUnreadable    = "unreadable"
)

// Discovered is one file found by the walk, with everything needed to decide
// whether it changed since the last run. Field names and JSON tags mirror
// model.File so the registry layer can map across without translation.
type Discovered struct {
	// Path is repo-relative and ALWAYS uses forward slashes, on every OS. The
	// content hash of an index must not differ between a Windows and a Linux
	// run of the same commit.
	Path     string `json:"path"`
	Language string `json:"language"` // "" when unrecognised

	// DetectedBy is how Language was decided: one of the langdetect.Method
	// values. "extension_ambiguous" marks a language that is a guess from an
	// extension several languages share, which is the first thing to check
	// when such a file then parses into nothing.
	DetectedBy string `json:"detected_by,omitempty"`

	// Recognised names what the content is when no grammar exists for it
	// ("coq", "mumps"). Language is then empty and the file is not parsed.
	Recognised string `json:"recognised,omitempty"`

	// ContentHash is the hex sha256 of the file's bytes. Empty for skipped
	// files: we deliberately do not read a file we have decided not to index,
	// so claiming a hash for it would be a fabrication.
	ContentHash string `json:"content_hash,omitempty"`

	SizeBytes   int64 `json:"size_bytes"`
	ModTimeUnix int64 `json:"mod_time_unix"`

	Skipped    bool   `json:"skipped"`
	SkipReason string `json:"skip_reason,omitempty"`

	// Unread is true when this walk did not open the file: its size and times
	// matched what an earlier pass recorded, so ContentHash (or the binary
	// skip) is that pass's and not a fresh reading. Only WalkTrusting sets
	// it; see trust.go for the rule and what it does not cover.
	Unread bool `json:"-"`
}

// Option adjusts a Walker. The knobs here are operational rather than
// repository-scoped: a hostile repo must not be able to spawn workers, and the
// size ceiling it CAN set (index.max_file_bytes) is validated against a hard
// maximum in config.Config.Validate before it reaches this package.
type Option func(*Walker)

// WithMaxFileSize overrides DefaultMaxFileSize. A value <= 0 restores the
// default rather than disabling the limit, because an unbounded limit is a
// memory-exhaustion primitive.
func WithMaxFileSize(n int64) Option {
	return func(w *Walker) {
		if n <= 0 {
			n = DefaultMaxFileSize
		}
		w.maxFileSize = n
	}
}

// WithWorkers overrides the concurrency bound, which defaults to
// runtime.NumCPU(). Mainly for tests that want a deterministic single-threaded
// walk.
func WithWorkers(n int) Option {
	return func(w *Walker) {
		if n < 1 {
			n = 1
		}
		w.workers = n
	}
}

// Walker discovers indexable files beneath a repository root. It is safe for
// concurrent use and holds no state between calls to Walk.
type Walker struct {
	// root is absolute and fully symlink-resolved. Resolving once, here, is
	// what makes the containment check in withinRoot meaningful: on macOS
	// /tmp is itself a symlink to /private/tmp, so an unresolved root would
	// make every file under it look like an escape.
	root string

	include []pattern
	exclude []pattern

	// detector decides each file's language. It carries the repository's
	// index.language_overrides, which is why it belongs to the walker rather
	// than being a package-level function: the same file can be a different
	// language in a different repository.
	detector *langdetect.Detector

	// includeHidden is the subset of include whose globs name a hidden
	// component, which is the only way to bring back something the hidden-file
	// rule leaves out. See hidden.go.
	includeHidden []pattern

	maxFileSize int64
	workers     int
}

// NewWalker compiles cfg's include/exclude globs and validates root.
func NewWalker(root string, cfg config.Config, opts ...Option) (*Walker, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("walker: root must not be empty")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("walker: resolve root %q: %w", root, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("walker: resolve symlinks in root %q: %w", abs, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("walker: stat root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("walker: root %q is not a directory", resolved)
	}

	w := &Walker{
		root:        filepath.Clean(resolved),
		maxFileSize: DefaultMaxFileSize,
		workers:     runtime.NumCPU(),
	}
	if cfg.MaxFileBytes > 0 {
		w.maxFileSize = cfg.MaxFileBytes
	}

	include := cfg.Include
	if len(include) == 0 {
		include = []string{"**"}
	}
	for _, g := range include {
		if p, ok := compilePattern(g, ""); ok {
			w.include = append(w.include, p)
			if !p.negate && namesHidden(g) {
				w.includeHidden = append(w.includeHidden, p)
			}
		}
	}
	if len(w.include) == 0 {
		// A config whose include list compiled to nothing would silently index
		// an empty repository. Fail loudly instead.
		return nil, fmt.Errorf("walker: index.include %v contains no usable pattern", cfg.Include)
	}

	for _, g := range cfg.Exclude {
		p, ok := compilePattern(g, "")
		if !ok {
			continue
		}
		// SECURITY: exclusions are additive defaults that a repository must
		// never be able to remove. .graphlume.yml is attacker-controlled and its
		// exclude list is appended to the defaults (see config.applyRepo), so a
		// repo could otherwise smuggle in "!.git/**" and have GraphLume index
		// the object store. This is the enforcement point.
		//
		// Negated entries are discarded rather than coerced into positive ones:
		// rewriting "!**" as "**" would honour the letter of the rule while
		// handing a repository a way to blank its own index. An exclude list
		// has no use for negation in the first place.
		if p.negate {
			continue
		}
		w.exclude = append(w.exclude, p)
	}

	overrides := make([]langdetect.Override, 0, len(cfg.LanguageOverrides))
	for _, o := range cfg.LanguageOverrides {
		ov := langdetect.Override{Pattern: o.Pattern, Language: o.Language}
		// Compiled by the matcher index.include and index.exclude use, so one
		// glob dialect covers the whole file. A negated pattern has no meaning
		// in a map from files to languages and is left unusable, which New
		// reports.
		if p, ok := compilePattern(o.Pattern, ""); ok && !p.negate {
			ov.Match = func(rel string) bool { return p.match(rel, false) }
		}
		overrides = append(overrides, ov)
	}
	det, err := langdetect.New(overrides)
	if err != nil {
		return nil, fmt.Errorf("walker: %w", err)
	}
	w.detector = det

	for _, opt := range opts {
		opt(w)
	}
	return w, nil
}

// Detect decides the language of one file from its path and content, exactly
// as Walk does for the same bytes.
//
// For callers that already hold a file's content: incremental re-indexing
// reads a changed file once and must not arrive at a different language than
// the full walk did. src may be the whole file; only its head is examined. The
// directory is listed only for a header whose content settles nothing.
func (w *Walker) Detect(rel string, src []byte) langdetect.Result {
	head := src
	if len(head) > langdetect.HeadBytes {
		head = head[:langdetect.HeadBytes]
	}
	if head == nil {
		head = []byte{}
	}
	return w.detector.Detect(rel, head, func() langdetect.Neighbours {
		var n langdetect.Neighbours
		dir := filepath.Join(w.root, filepath.FromSlash(path.Dir(rel)))
		entries, err := os.ReadDir(dir)
		if err != nil {
			return n
		}
		observeNeighbours(&n, entries)
		return n
	})
}

// observeNeighbours summarises the files of one directory. Every entry that
// is not a directory counts, ignored or not: what a header sits beside is a
// fact about the directory, and the full walk and a single-file re-index must
// see the same one.
func observeNeighbours(n *langdetect.Neighbours, entries []iofs.DirEntry) {
	for _, e := range entries {
		if !e.IsDir() {
			n.Observe(e.Name())
		}
	}
}

// Root reports the absolute, symlink-resolved repository root.
func (w *Walker) Root() string { return w.root }

// MaxFileSize reports the size limit this walker enforces, so the parsers can
// be given the same one and a skipped file can be recorded with the limit it
// exceeded.
func (w *Walker) MaxFileSize() int64 { return w.maxFileSize }

// Walk discovers every indexable file beneath the root.
//
// The returned slice is sorted by Path and includes skipped files, so callers
// can distinguish "not present" from "present but not indexed" — the difference
// between a deleted file and an oversized one.
func (w *Walker) Walk(ctx context.Context) ([]Discovered, error) {
	return w.WalkTrusting(ctx, nil)
}

// WalkStream streams every discovered file beneath the root to fn as it is found,
// avoiding buffering 1,000,000 files in a single contiguous slice in memory.
//
// Unlike Walk, the order is NOT sorted: directories are read in parallel, so a
// caller that needs a deterministic order must impose one itself. fn is called
// from one goroutine at a time, never while the walker holds its own lock, so
// it may block (for example on a channel send) without stalling bookkeeping.
// An error from fn stops the walk and is returned.
func (w *Walker) WalkStream(ctx context.Context, fn func(Discovered) error) error {
	return w.WalkStreamTrusting(ctx, nil, fn)
}

// WalkStreamTrusting streams discovered files with trust-cache acceleration.
func (w *Walker) WalkStreamTrusting(ctx context.Context, trust *Trust, fn func(Discovered) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if fn == nil {
		return errors.New("walker: WalkStream requires a non-nil callback")
	}

	// A callback error cancels the walk; the caller's own cancellation still
	// reaches it through the parent.
	walkCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	st := &walkState{w: w, ctx: walkCtx, done: walkCtx.Done(), trust: trust, onEmit: fn, stop: cancel}
	if n := w.workers - 1; n > 0 {
		st.sem = make(chan struct{}, n)
	}

	st.walkDir(w.root, "", ruleSet{})
	st.wg.Wait()

	return st.firstErr()
}

// WalkTrusting is Walk for a tree an earlier pass has already read. A file
// whose size and times match what that pass recorded, and whose times are old
// enough to be believed, is returned with the recorded hash and Unread set,
// without being opened. Everything else is read and hashed exactly as Walk
// does. A nil trust is Walk.
//
// The rule, its reasoning and its limits are in trust.go. The saving is the
// difference between one stat and one open, read and close per file, which is
// most of what a pass over an unchanged tree costs.
func (w *Walker) WalkTrusting(ctx context.Context, trust *Trust) ([]Discovered, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	st := &walkState{w: w, ctx: ctx, done: ctx.Done(), trust: trust}
	// One slot per worker beyond the caller's own goroutine, which does real
	// work rather than blocking on a queue.
	if n := w.workers - 1; n > 0 {
		st.sem = make(chan struct{}, n)
	}

	st.walkDir(w.root, "", ruleSet{})
	st.wg.Wait()

	if err := st.firstErr(); err != nil {
		return nil, err
	}

	slices.SortFunc(st.out, func(a, b Discovered) int { return strings.Compare(a.Path, b.Path) })
	return st.out, nil
}

type walkState struct {
	w    *Walker
	ctx  context.Context
	done <-chan struct{} // ctx.Done(), hoisted so the hot path never calls it

	// trust is read-only for the whole walk, so workers share it unlocked.
	trust *Trust

	sem chan struct{}
	wg  sync.WaitGroup

	// mu guards both out and err. Results arrive from every worker, and the
	// first error wins so a cancelled walk reports context.Canceled rather
	// than whichever goroutine noticed last.
	mu     sync.Mutex
	out    []Discovered
	err    error
	onEmit func(Discovered) error

	// emitMu serialises onEmit calls without holding mu, so a callback that
	// blocks does not stall error bookkeeping. stop cancels the walk when the
	// callback fails.
	emitMu sync.Mutex
	stop   context.CancelFunc
}

func (st *walkState) fail(err error) {
	st.mu.Lock()
	if st.err == nil {
		st.err = err
	}
	st.mu.Unlock()
}

func (st *walkState) firstErr() error {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.err
}

func (st *walkState) emit(d Discovered) {
	if st.onEmit != nil {
		st.emitMu.Lock()
		err := st.onEmit(d)
		st.emitMu.Unlock()
		if err != nil {
			st.fail(err)
			if st.stop != nil {
				st.stop()
			}
		}
		return
	}
	st.mu.Lock()
	st.out = append(st.out, d)
	st.mu.Unlock()
}

// cancelled is checked once per directory entry, from every worker. A plain
// ctx.Err() would take the context's mutex on each call and turn cancellation
// checking into the walk's contention point; a non-blocking receive on Done
// does not.
func (st *walkState) cancelled() bool {
	select {
	case <-st.done:
		return true
	default:
		return false
	}
}

// walkDir processes one directory. rel is its repo-relative slash path ("" for
// the root) and rules are the ignore rules inherited from its ancestors.
func (st *walkState) walkDir(dir, rel string, rules ruleSet) {
	if st.cancelled() {
		st.fail(st.ctx.Err())
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		// One unreadable directory must not sink the run, but it must not
		// vanish either: an index that silently omits a subtree looks identical
		// to one where the subtree was deleted. Record it and continue.
		if rel == "" {
			st.fail(fmt.Errorf("read repository root %q: %w", dir, err))
			return
		}
		st.emit(Discovered{Path: rel, Skipped: true, SkipReason: SkipUnreadable})
		return
	}

	rules = loadDirRules(rules, dir, rel, entries)

	// Computed at most once per directory, and only if a header in it needs
	// placing by what it sits beside. walkDir handles a directory's entries on
	// one goroutine, so the memo needs no lock.
	var (
		neighbours     langdetect.Neighbours
		haveNeighbours bool
	)
	siblings := func() langdetect.Neighbours {
		if !haveNeighbours {
			observeNeighbours(&neighbours, entries)
			haveNeighbours = true
		}
		return neighbours
	}

	for _, entry := range entries {
		if st.cancelled() {
			st.fail(st.ctx.Err())
			return
		}

		name := entry.Name()
		childRel := name
		if rel != "" {
			childRel = rel + "/" + name
		}
		childPath := filepath.Join(dir, name)
		isSymlink := entry.Type()&iofs.ModeSymlink != 0
		isDir := entry.IsDir() && !isSymlink

		// Config exclusions are checked first and cannot be negated; see
		// NewWalker. Checking them before the repo's own ignore rules also
		// means a repo cannot make GraphLume spend time inside node_modules.
		if st.w.configExcluded(childRel, isDir) {
			continue
		}
		// Hidden entries that are editor or tool state, by the rule in
		// hidden.go. Checked here, before the symlink and ignore-file logic,
		// for the same reason config exclusions are: nothing a repository
		// commits should be able to make the walk enter `.git`.
		if st.w.hiddenExcluded(childRel, name, isDir) {
			continue
		}

		if isSymlink {
			st.emit(st.symlink(childPath, childRel))
			continue
		}

		if isDir {
			if rules.prunes(childRel) {
				continue
			}
			st.descend(childPath, childRel, rules)
			continue
		}

		if rules.ignored(childRel, false) {
			continue
		}
		if !st.w.included(childRel) {
			continue
		}

		switch {
		case entry.Type()&iofs.ModeSymlink != 0:
			st.emit(st.symlink(childPath, childRel))
		case entry.Type().IsRegular():
			st.emit(st.w.inspect(childPath, childRel, entry, siblings, st.trust))
		default:
			// Sockets, devices and FIFOs. Opening a FIFO blocks until a writer
			// appears, which would hang the indexer indefinitely on a
			// repository that commits one; never open them.
			st.emit(Discovered{Path: childRel, Skipped: true, SkipReason: SkipIrregular})
		}
	}
}

// descend visits a subdirectory, on a new goroutine when a worker slot is free.
//
// Trying the semaphore non-blockingly and otherwise recursing inline is what
// keeps this deadlock-free: a bounded pool fed from a channel can wedge when
// every worker is blocked publishing more work, whereas inline recursion always
// makes progress.
func (st *walkState) descend(dir, rel string, rules ruleSet) {
	select {
	case st.sem <- struct{}{}:
		st.wg.Add(1)
		go func() {
			defer st.wg.Done()
			defer func() { <-st.sem }()
			st.walkDir(dir, rel, rules)
		}()
	default:
		st.walkDir(dir, rel, rules)
	}
}

// symlink classifies a symbolic link. Links are never followed.
//
// PATH TRAVERSAL IS IN THE THREAT MODEL. A repository can commit a symlink
// named `config.go` pointing at /etc/passwd or ../../secrets; following it
// would hash and index host data, and an agent would then happily quote it. We
// resolve the target purely to report *why* the link was skipped, and the
// containment test uses the symlink-resolved root so that macOS's /tmp ->
// /private/tmp does not read as an escape.
//
// Links that stay inside the root are skipped too. Following them cannot leak
// data, but it creates cycles and indexes the same content under two paths,
// and git does not follow them either.
func (st *walkState) symlink(full, rel string) Discovered {
	d := Discovered{Path: rel, Skipped: true, SkipReason: SkipSymlink}
	// A link is never opened, so its language is whatever the name says.
	d.setDetection(st.w.detector.Detect(rel, nil, nil))

	target, err := filepath.EvalSymlinks(full)
	if err != nil {
		// Broken or cyclic link. Unresolvable is not the same as escaping, but
		// both are skipped.
		d.SkipReason = SkipUnreadable
		return d
	}
	if !withinRoot(st.w.root, target) {
		d.SkipReason = SkipSymlinkEscape
	}
	return d
}

// withinRoot reports whether target is root or lies beneath it. Both arguments
// must already be absolute, cleaned and symlink-resolved.
func withinRoot(root, target string) bool {
	target = filepath.Clean(target)
	if target == root {
		return true
	}
	return strings.HasPrefix(target, root+string(filepath.Separator))
}

// setDetection records what language detection concluded.
func (d *Discovered) setDetection(r langdetect.Result) {
	d.Language = r.Language
	d.DetectedBy = string(r.Method)
	d.Recognised = r.Recognised
}

// headPool recycles the buffers that carry a file's first bytes from the
// hashing loop to language detection. Only files whose extension is ambiguous
// or unknown take one, and nothing keeps it past inspect.
var headPool = sync.Pool{New: func() any {
	b := make([]byte, 0, langdetect.HeadBytes)
	return &b
}}

// inspect stats, sizes, sniffs, hashes and identifies a regular file.
//
// The language is decided from bytes the hashing loop has already read:
// detection never opens a file a second time. A file that is skipped before or
// during the read is identified by name alone. With a trust it stops after the
// stat when the file provably has not changed.
func (w *Walker) inspect(full, rel string, entry iofs.DirEntry, siblings func() langdetect.Neighbours, trust *Trust) Discovered {
	d := Discovered{Path: rel}

	info, err := entry.Info()
	if err != nil {
		d.setDetection(w.detector.Detect(rel, nil, siblings))
		d.Skipped, d.SkipReason = true, SkipUnreadable
		return d
	}
	d.SizeBytes = info.Size()
	d.ModTimeUnix = info.ModTime().Unix()

	if d.SizeBytes > w.maxFileSize {
		d.setDetection(w.detector.Detect(rel, nil, siblings))
		d.Skipped, d.SkipReason = true, SkipTooLarge
		return d
	}

	if trust != nil {
		changed, hasChange := changeTimeUnix(info)
		if known, ok := trust.unchanged(rel, d.SizeBytes, d.ModTimeUnix, changed, hasChange); ok {
			// Not opened, so identified by name alone. Nothing re-parses an
			// unchanged file, and the language the index holds for it stands.
			d.setDetection(w.detector.Detect(rel, nil, nil))
			d.Unread = true
			d.ContentHash = known.ContentHash
			if known.ContentHash == "" {
				d.Skipped, d.SkipReason = true, known.SkipReason
			}
			return d
		}
	}

	var head *[]byte
	if w.detector.NeedsContent(rel) {
		head = headPool.Get().(*[]byte) //nolint:errcheck,forcetypeassert // the pool only ever holds *[]byte
		*head = (*head)[:0]
		defer headPool.Put(head)
	}

	sum, binary, err := hashFileHead(full, w.maxFileSize, head)
	switch {
	case errors.Is(err, errTooLarge):
		d.Skipped, d.SkipReason = true, SkipTooLarge
	case err != nil:
		d.Skipped, d.SkipReason = true, SkipUnreadable
	case binary:
		d.Skipped, d.SkipReason = true, SkipBinary
	default:
		d.ContentHash = sum
	}

	if head != nil && !d.Skipped {
		d.setDetection(w.detector.Detect(rel, *head, siblings))
	} else {
		d.setDetection(w.detector.Detect(rel, nil, siblings))
	}
	return d
}

// errTooLarge signals that a file outgrew the limit between stat and read.
var errTooLarge = errors.New("file exceeds size limit")

// hashFile returns the hex sha256 of a file, or reports it as binary.
//
// Size is re-checked while streaming rather than trusted from the earlier
// stat: a file can grow between the two, whether through an ordinary
// concurrent write or a deliberate one, and the stat'd size is not a bound on
// what we are about to buffer.
//
// A binary file stops the read at the sniff window instead of hashing to the
// end. There is nothing to gain from the digest of a file we will not index,
// and repositories that commit hundreds of megabytes of binaries are common.
func hashFile(name string, limit int64) (sum string, binary bool, err error) {
	return hashFileHead(name, limit, nil)
}

// readBufPool recycles the 32 KB streaming read buffers used during file hashing
// to avoid allocating a fresh 32 KB slice on every indexed file.
var readBufPool = sync.Pool{New: func() any {
	b := make([]byte, 32*1024)
	return &b
}}

// hashFileHead is hashFile that also copies the first langdetect.HeadBytes of
// the file into head, when head is not nil.
//
// Detection needs a few kilobytes of a file this loop is reading anyway.
// Handing them over from here is what keeps "look at the content" from
// meaning "read every ambiguous file twice".
func hashFileHead(name string, limit int64, head *[]byte) (sum string, binary bool, err error) {
	f, err := os.Open(name) //nolint:gosec // name comes from a walked directory entry
	if err != nil {
		return "", false, err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	bufPtr := readBufPool.Get().(*[]byte) //nolint:errcheck,forcetypeassert // pool only holds *[]byte
	defer readBufPool.Put(bufPtr)
	buf := *bufPtr
	var total, sniffed int64

	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if sniffed < binarySniffLen {
				window := chunk
				if sniffed+int64(len(window)) > binarySniffLen {
					window = window[:binarySniffLen-sniffed]
				}
				if bytes.IndexByte(window, 0) >= 0 {
					return "", true, nil
				}
				sniffed += int64(len(window))
			}
			if head != nil && len(*head) < langdetect.HeadBytes {
				take := chunk
				if room := langdetect.HeadBytes - len(*head); len(take) > room {
					take = take[:room]
				}
				*head = append(*head, take...)
			}
			total += int64(n)
			if total > limit {
				return "", false, errTooLarge
			}
			h.Write(chunk)
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			return "", false, rerr
		}
	}
	return hex.EncodeToString(h.Sum(nil)), false, nil
}

// configExcluded reports whether a config Exclude glob covers the entry. For
// directories it also honours the "dir/**" form so the subtree is pruned rather
// than walked and discarded.
func (w *Walker) configExcluded(rel string, isDir bool) bool {
	for i := range w.exclude {
		if isDir {
			if w.exclude[i].coversDir(rel) {
				return true
			}
			continue
		}
		if w.exclude[i].match(rel, false) {
			return true
		}
	}
	return false
}

// included reports whether a file matches at least one Include glob.
//
// Include is only applied to files. A restrictive pattern such as "**/*.go"
// matches no directory, so testing directories against it would prune the
// entire tree at depth one.
func (w *Walker) included(rel string) bool {
	for i := range w.include {
		if w.include[i].match(rel, false) {
			return true
		}
	}
	return false
}

// Excluded reports whether rel, repository-relative and slash-separated, is
// left out by the configured index.exclude patterns: the same rule the walk
// applies, so a caller planning work on a directory skips exactly what the
// index skips.
func (w *Walker) Excluded(rel string, isDir bool) bool {
	return w.configExcluded(rel, isDir)
}
