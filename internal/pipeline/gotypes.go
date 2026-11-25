package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/resolve/gotypes"
	"github.com/sutrakam/graphlume/internal/resolve/scip"
	"github.com/sutrakam/graphlume/internal/resolve/scipimport"
)

// GoTypesOptions configures the built-in Go type check.
type GoTypesOptions struct {
	// Root is the repository root on disk.
	Root string
	// Env overrides the environment lookups (GOROOT, PATH, GOMODCACHE,
	// GOPATH); nil means the process environment.
	Env func(string) string
	// Timeout and MemoryLimit override the package defaults when non-zero.
	Timeout     time.Duration
	MemoryLimit uint64
}

// GoTypesOutcome is what CheckGoTypes did, for resolve to print.
type GoTypesOutcome struct {
	// Ran is false when there was nothing to check; Reason says why.
	Ran    bool
	Reason string

	Check  gotypes.Stats
	Import scipimport.ImportResult
	Err    error
}

// CheckGoTypes type-checks the Go packages of the repository with the standard
// library's own type checker and lands what it proves as exact edges.
//
// It runs after the heuristic pass, so an edge the heuristic found is raised
// to exact where the compiler agrees, edges the heuristic could not bind are
// added, and an edge the compiler no longer proves is retracted. A failure of
// any kind is returned in the outcome and never as an error: the heuristic
// graph is already complete and a type checker that could not run must not
// take resolve down with it.
func CheckGoTypes(ctx context.Context, reg *registry.Registry, runID int64, opts GoTypesOptions) GoTypesOutcome {
	var out GoTypesOutcome
	files, err := reg.Files(ctx, runID)
	if err != nil {
		out.Err = fmt.Errorf("read indexed files: %w", err)
		return out
	}
	var goFiles []string
	for _, f := range files {
		if f.Language == "go" && f.ParseStatus != "failed" && f.ParseStatus != "skipped" {
			goFiles = append(goFiles, f.Path)
		}
	}
	if len(goFiles) == 0 {
		out.Reason = "no Go files indexed"
		return out
	}
	res, err := gotypes.Check(ctx, gotypes.Options{
		Root: opts.Root, Files: goFiles, Env: opts.Env,
		Timeout: opts.Timeout, MemoryLimit: opts.MemoryLimit,
	})
	if err != nil {
		out.Err = err
		return out
	}
	out.Check = res.Stats
	if res.Stats.Packages == 0 {
		out.Reason = "Go files found but no go.mod covers them"
		return out
	}
	out.Ran = true
	dir := filepath.Join(opts.Root, filepath.FromSlash(CompilerIndexDir))
	if err := os.MkdirAll(dir, 0o750); err == nil {
		data, _ := json.Marshal(map[string]any{
			"stubbed_imports":     res.Stats.MissingCount,
			"packages_with_stubs": res.Stats.WithStubs,
			"packages":            res.Stats.Packages,
			"files_checked":       res.Stats.FilesChecked,
			"missing_imports":     res.Stats.MissingImports,
		})
		_ = os.WriteFile(filepath.Join(dir, "gotypes.json"), data, 0o600)
	}
	imp := scipimport.NewImporter(reg, runID, scip.Options{RepoRoot: opts.Root})
	facts := res.CallSites()
	sites := make([]scipimport.CallSite, len(facts))
	for i, f := range facts {
		sites[i] = scipimport.CallSite{Path: f.Path, Line: f.Line, Name: f.Name, Known: f.Known, DstFQN: f.DstFQN}
	}
	out.Import, out.Err = imp.ImportFacts(ctx, res.FileFacts(), sites, gotypes.Tool, gotypes.Detail)
	return out
}
