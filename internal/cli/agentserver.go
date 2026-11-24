package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sutrakam/graphlume/internal/capability/hotspots"
	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/graph"
	devmcp "github.com/sutrakam/graphlume/internal/mcp"
	"github.com/sutrakam/graphlume/internal/parse"
	"github.com/sutrakam/graphlume/internal/registry"
)

// auditLogEnv names the environment variable that turns the audit log on for
// every `graphlume serve` and `graphlume tool call` on a machine, without a flag in
// each agent's configuration.
const auditLogEnv = "GRAPHLUME_AUDIT_LOG"

// agentServerOptions configures the tool server behind `serve` and `tool`.
type agentServerOptions struct {
	// Profile overrides mcp.profile from .graphlume.yml when set.
	Profile config.MCPProfile
	// AuditPath is where tool calls are recorded. Empty falls back to
	// GRAPHLUME_AUDIT_LOG; empty there too means no log.
	AuditPath string
	// CheckEdits re-indexes files changed in the working tree before every
	// tool call (editRefresher). `serve` sets it; the portal and
	// `graphlume tool` do not, the portal because it promises never to
	// cause a write.
	CheckEdits bool
	// AllowEdits permits write modifications to source files on disk.
	AllowEdits bool
	// ListAllProjects is set for a local stdio server only; see
	// mcp.Deps.ListAllProjects.
	ListAllProjects bool
	// Estate is the estate file from serve --estate. Empty falls back to
	// estate: in user config, then in .graphlume.yml; see serve_estate.go.
	Estate string
}

// openAgentServer opens the committed index read-only and builds the tool
// server over it. The returned function releases everything it opened.
//
// `graphlume serve`, the REST endpoint and `graphlume tool` all come through here, so
// the three front ends cannot be wired differently: same index handle, same
// history source, same language table, same audit log.
//
// The audit log is a flag and an environment variable and deliberately NOT a
// key in .graphlume.yml. That file is repository content, and a repository must
// not be able to switch off the record of what an agent read from it.
func openAgentServer(ctx context.Context, g *Global, opts agentServerOptions) (*devmcp.Server, func(), error) {
	cfg := g.Config()
	if opts.Profile != "" {
		if !opts.Profile.Valid() {
			return nil, nil, fmt.Errorf("--profile %q: want minimal, standard, full, compact or frontdoor", opts.Profile)
		}
		cfg.MCPProfile = opts.Profile
	}
	if opts.AllowEdits {
		cfg.AllowEdits = true
	}

	dbPath := g.DBPath()
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("no index at %s — run 'graphlume init' and 'graphlume index' first", dbPath)
	}

	// Read-only: an agent session must never be able to mutate the index it
	// is querying.
	db, err := openReadOnly(ctx, dbPath)
	if err != nil {
		return nil, nil, err
	}
	closers := []func(){func() { _ = db.Close() }}
	cleanup := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}

	deps := devmcp.Deps{
		Registry:  registry.New(db),
		Graph:     graph.New(db, graph.WithCSR()),
		Config:    cfg,
		Judgeable: bindsReferences,
		MetaDir:   filepath.Dir(dbPath),

		ListAllProjects: opts.ListAllProjects,
	}
	if opts.CheckEdits {
		r := newEditRefresher(g, dbPath)
		deps.Refresher = r
		closers = append(closers, r.Close)
	}
	estate, err := openServeEstate(cfg, opts.Estate)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	if estate != nil {
		deps.Estate = estate
		closers = append(closers, func() { _ = estate.Close() })
	}
	if h := openHistory(g); h.repo != nil {
		deps.History = hotspotHistory{h}
		deps.Owners = h
	}

	// The read-only reports (policy, arch, deps, vulns, sbom, tour, adr and
	// lessons) are the commands of those names, run in this process.
	deps.Reports = newReportRunner(cfg.RepoRoot)

	auditPath := opts.AuditPath
	if auditPath == "" {
		auditPath = strings.TrimSpace(os.Getenv(auditLogEnv))
	}
	if auditPath != "" {
		f, err := openAuditFile(auditPath)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		closers = append(closers, func() { _ = f.Close() })
		deps.Audit = devmcp.NewAuditLog(f)
	}

	srv, err := devmcp.New(deps)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return srv, cleanup, nil
}

// openAuditFile opens the audit log for appending, readable only by the user:
// it records which symbols and tasks an agent asked about, which is a map of
// what someone is working on.
func openAuditFile(path string) (*os.File, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil { //nolint:gosec // G703: the path is the user's own flag or environment value
			return nil, fmt.Errorf("create audit log directory: %w", err)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // G304: the path is the user's own flag or environment value
	if err != nil {
		return nil, fmt.Errorf("open audit log %s: %w", path, err)
	}
	return f, nil
}

// bindsReferences reports whether the index binds cross-file references for a
// language. In the parsed tier it records definitions only, so every one of
// them has no caller by construction; see internal/parse/tiers.go.
func bindsReferences(language string) bool {
	return parse.TierFor(language) != parse.TierParsed
}

// hotspotHistory adapts the CLI's git history reader to hotspots.History.
//
// The reader already rebases commit paths from the worktree root onto the
// project root (see gitHistory), which is the translation hotspots.Commit
// requires and the reason it is a distinct type.
type hotspotHistory struct{ h *gitHistory }

func (a hotspotHistory) Commits(ctx context.Context, limit int) ([]hotspots.Commit, error) {
	cs, err := a.h.Commits(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]hotspots.Commit, 0, len(cs))
	for _, c := range cs {
		out = append(out, hotspots.Commit{
			SHA: c.SHA, When: c.When, Paths: c.Paths, ShallowBoundary: c.ShallowBoundary, Truncated: c.Truncated, Merge: c.Merge,
		})
	}
	return out, nil
}
