package multirepo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/sutrakam/graphlume/internal/acquire/fs"
)

const (
	// GlobalRegistryDir is the folder in user home where global state lives.
	GlobalRegistryDir = ".graphlume"
	// GlobalRegistryFileName is the json file containing all known projects.
	GlobalRegistryFileName = "registry.json"
	// GlobalRegistryLockFile is the flock lockfile protecting registry updates.
	GlobalRegistryLockFile = "registry.lock"
	// GlobalRegistryCurrentVersion is the schema version of the registry format.
	GlobalRegistryCurrentVersion = 1
)

// ProjectEntry represents one known project in the global registry.
type ProjectEntry struct {
	// ID is unique in the registry and derived from the root path, never from
	// repository content. Name is the project's own name for display only:
	// it comes from the repository's .graphlume.yml, which is untrusted.
	ID            string   `json:"id"`
	Name          string   `json:"name,omitempty"`
	RootPath      string   `json:"root_path"`
	DBPath        string   `json:"db_path"`
	LockPath      string   `json:"lock_path"`
	Mode          string   `json:"mode"` // "single_repo" or "workspace"
	Languages     []string `json:"languages,omitempty"`
	LastIndexedAt string   `json:"last_indexed_at,omitempty"`
	LastQueriedAt string   `json:"last_queried_at,omitempty"`
	GitHead       string   `json:"git_head,omitempty"`
	SchemaVersion int      `json:"schema_version"`
	WatchPolicy   string   `json:"watch_policy,omitempty"` // "auto_lru", "disabled", "always"
}

// GlobalRegistry is the root structure serialized into ~/.graphlume/registry.json.
type GlobalRegistry struct {
	SchemaVersion int            `json:"schema_version"`
	UpdatedAt     string         `json:"updated_at"`
	Projects      []ProjectEntry `json:"projects"`
}

// GlobalRegistryPath returns the absolute path to ~/.graphlume/registry.json.
func GlobalRegistryPath(home string) string {
	return filepath.Join(home, GlobalRegistryDir, GlobalRegistryFileName)
}

// GlobalRegistryLockPath returns the path to ~/.graphlume/registry.lock.
func GlobalRegistryLockPath(home string) string {
	return filepath.Join(home, GlobalRegistryDir, GlobalRegistryLockFile)
}

// CanonicalPath cleans and resolves symlinks so longest-prefix matching is deterministic
// across macOS /var -> /private/var and Windows drive letters.
func CanonicalPath(p string) string {
	if p == "" {
		return ""
	}
	clean := filepath.Clean(p)
	if abs, err := filepath.Abs(clean); err == nil {
		clean = abs
	}
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		return filepath.Clean(resolved)
	}
	// The path does not exist (yet, or any more). Resolve its longest existing
	// ancestor and keep the rest as written, so a missing file under a
	// symlinked directory (/var -> /private/var on macOS) still compares equal
	// to that directory's canonical form.
	rest := ""
	for dir := clean; ; {
		parent := filepath.Dir(dir)
		rest = filepath.Join(filepath.Base(dir), rest)
		if parent == dir {
			return clean
		}
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(resolved, rest)
		}
		dir = parent
	}
}

// ErrRegistryNewer is returned for a registry written by a newer GraphLume.
// Reading it with this version's fields would drop what it does not know, and
// saving it back would erase that for the newer binary too.
var ErrRegistryNewer = errors.New("global registry was written by a newer GraphLume")

// LoadGlobalRegistry reads the global registry from ~/.graphlume/registry.json.
// If the file does not exist, an empty registry with SchemaVersion=1 is returned.
// A registry of a newer schema version is refused with ErrRegistryNewer; one
// with no version is the first format and is read as version 1.
func LoadGlobalRegistry(home string) (*GlobalRegistry, error) {
	path := GlobalRegistryPath(home)
	data, err := os.ReadFile(path) //nolint:gosec // G304: GraphLume's own registry file under the user's home
	if errors.Is(err, os.ErrNotExist) {
		return &GlobalRegistry{
			SchemaVersion: GlobalRegistryCurrentVersion,
			UpdatedAt:     time.Now().UTC().Format(time.RFC3339),
			Projects:      []ProjectEntry{},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read global registry %s: %w", path, err)
	}

	var reg GlobalRegistry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parse global registry %s: %w", path, err)
	}
	if reg.SchemaVersion > GlobalRegistryCurrentVersion {
		return nil, fmt.Errorf("%w: %s is schema v%d, this binary reads up to v%d; upgrade graphlume",
			ErrRegistryNewer, path, reg.SchemaVersion, GlobalRegistryCurrentVersion)
	}
	if reg.SchemaVersion == 0 {
		reg.SchemaVersion = GlobalRegistryCurrentVersion
	}
	if reg.Projects == nil {
		reg.Projects = []ProjectEntry{}
	}
	return &reg, nil
}

// SaveGlobalRegistry writes the global registry atomically under temp-file + rename.
// It assumes the caller holds the registry lock if running in a mutating context.
func SaveGlobalRegistry(home string, reg *GlobalRegistry) error {
	dir := filepath.Join(home, GlobalRegistryDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create registry dir %s: %w", dir, err)
	}

	reg.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal global registry: %w", err)
	}

	target := GlobalRegistryPath(home)
	tmp := target + fmt.Sprintf(".tmp.%d", time.Now().UnixNano())
	if err := writeSynced(tmp, data); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write temp registry %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("commit registry %s: %w", target, err)
	}
	// The rename is durable only once the directory entry is: without this a
	// crash right after can come back with the old file, or none.
	syncDir(dir)
	return nil
}

// writeSynced writes data to a new file and flushes it to disk before closing,
// so the rename that follows never publishes a file whose bytes are not there.
func writeSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: a temporary file beside the registry, chosen by this package
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// syncDir flushes a directory's entries. Best effort: Windows cannot open a
// directory for syncing and its rename is already journaled by NTFS.
func syncDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	if d, err := os.Open(dir); err == nil { //nolint:gosec // G304: the registry's own directory
		_ = d.Sync()
		_ = d.Close()
	}
}

// registryLockWait bounds how long a mutation waits for another process's
// registry update. Updates are a read, a small edit and a write, so anything
// longer means the holder is stuck and the caller should hear about it.
const registryLockWait = 5 * time.Second

// MutateGlobalRegistry executes fn under an exclusive cross-process lock on
// ~/.graphlume/registry.lock. Waiting for the lock stops when ctx is done.
func MutateGlobalRegistry(ctx context.Context, home string, fn func(reg *GlobalRegistry) error) error {
	dir := filepath.Join(home, GlobalRegistryDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create registry dir %s: %w", dir, err)
	}

	lockPath := GlobalRegistryLockPath(home)
	// TryLock with retries for concurrent processes
	var lock *fs.FileLock
	var err error
	deadline := time.Now().Add(registryLockWait)
	for {
		lock, err = fs.TryLock(lockPath)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrLocked) || time.Now().After(deadline) {
			return fmt.Errorf("acquire registry lock %s: %w", lockPath, err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("acquire registry lock %s: %w", lockPath, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	defer func() {
		_ = lock.Unlock()
	}()

	reg, err := LoadGlobalRegistry(home)
	if err != nil {
		return err
	}

	if err := fn(reg); err != nil {
		return err
	}

	return SaveGlobalRegistry(home, reg)
}

// RegisterProject adds or updates a project entry in ~/.graphlume/registry.json.
func RegisterProject(ctx context.Context, home string, entry ProjectEntry) error {
	if entry.RootPath == "" {
		return errors.New("project root_path is required")
	}
	entry.RootPath = CanonicalPath(entry.RootPath)
	if entry.Mode == "" {
		entry.Mode = "single_repo"
	}
	if entry.WatchPolicy == "" {
		entry.WatchPolicy = "auto_lru"
	}

	return MutateGlobalRegistry(ctx, home, func(reg *GlobalRegistry) error {
		// A project is its root. Matching by ID as well let a second project
		// with the same name (or a repository whose config names itself after
		// another) silently replace the first project's paths.
		kept := make([]ProjectEntry, 0, len(reg.Projects)+1)
		for _, p := range reg.Projects {
			if CanonicalPath(p.RootPath) == entry.RootPath {
				continue // replaced below
			}
			if _, err := os.Stat(p.RootPath); errors.Is(err, os.ErrNotExist) {
				continue // the project's directory is gone: drop the stale entry
			}
			kept = append(kept, p)
		}
		entry.ID = uniqueProjectID(kept, entry.RootPath)
		reg.Projects = append(kept, entry)
		sort.Slice(reg.Projects, func(i, j int) bool {
			return reg.Projects[i].ID < reg.Projects[j].ID
		})
		return nil
	})
}

// RecordIndexed notes that the project at entry.RootPath was just indexed. An
// existing entry for that root keeps its ID and settings and gets the new
// time, head and index path; a root seen for the first time is registered as
// RegisterProject would. The match is by root only: the project's name comes
// from the repository and must not select, or overwrite, another entry.
func RecordIndexed(ctx context.Context, home string, entry ProjectEntry) error {
	if entry.RootPath == "" {
		return errors.New("project root_path is required")
	}
	entry.RootPath = CanonicalPath(entry.RootPath)
	return MutateGlobalRegistry(ctx, home, func(reg *GlobalRegistry) error {
		for i := range reg.Projects {
			p := &reg.Projects[i]
			if CanonicalPath(p.RootPath) != entry.RootPath {
				continue
			}
			p.LastIndexedAt = entry.LastIndexedAt
			if entry.GitHead != "" {
				p.GitHead = entry.GitHead
			}
			if entry.DBPath != "" {
				p.DBPath = entry.DBPath
				p.LockPath = entry.LockPath
			}
			return nil
		}
		if entry.Mode == "" {
			entry.Mode = "single_repo"
		}
		if entry.WatchPolicy == "" {
			entry.WatchPolicy = "auto_lru"
		}
		entry.ID = uniqueProjectID(reg.Projects, entry.RootPath)
		reg.Projects = append(reg.Projects, entry)
		sort.Slice(reg.Projects, func(i, j int) bool { return reg.Projects[i].ID < reg.Projects[j].ID })
		return nil
	})
}

// uniqueProjectID is the root's base name, or the base name and a short hash
// of the root when another registered project already uses that name.
func uniqueProjectID(existing []ProjectEntry, root string) string {
	id := filepath.Base(root)
	for _, p := range existing {
		if p.ID == id {
			sum := sha256.Sum256([]byte(root))
			return id + "-" + hex.EncodeToString(sum[:])[:8]
		}
	}
	return id
}

// UnregisterProject removes a project entry by ID or RootPath.
func UnregisterProject(ctx context.Context, home, idOrPath string) error {
	canon := CanonicalPath(idOrPath)
	return MutateGlobalRegistry(ctx, home, func(reg *GlobalRegistry) error {
		kept := make([]ProjectEntry, 0, len(reg.Projects))
		for _, p := range reg.Projects {
			if p.ID != idOrPath && CanonicalPath(p.RootPath) != canon {
				kept = append(kept, p)
			}
		}
		reg.Projects = kept
		return nil
	})
}

// FindProjectByPath locates the ProjectEntry matching path by longest canonical prefix match.
func FindProjectByPath(reg *GlobalRegistry, targetPath string) (*ProjectEntry, bool) {
	if reg == nil || len(reg.Projects) == 0 || targetPath == "" {
		return nil, false
	}
	target := CanonicalPath(targetPath)

	var best *ProjectEntry
	bestLen := -1

	for i := range reg.Projects {
		p := &reg.Projects[i]
		canonRoot := CanonicalPath(p.RootPath)
		if target == canonRoot {
			return p, true
		}
		if strings.HasPrefix(target, canonRoot+string(filepath.Separator)) {
			if len(canonRoot) > bestLen {
				bestLen = len(canonRoot)
				best = p
			}
		}
	}
	return best, best != nil
}

// FindProjectByID finds a ProjectEntry by its exact identifier.
func FindProjectByID(reg *GlobalRegistry, id string) (*ProjectEntry, bool) {
	if reg == nil {
		return nil, false
	}
	for i := range reg.Projects {
		if reg.Projects[i].ID == id {
			return &reg.Projects[i], true
		}
	}
	return nil, false
}
