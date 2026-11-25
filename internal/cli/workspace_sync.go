package cli

import (
	stdctx "context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sutrakam/graphlume/internal/acquire/workspace"
	"github.com/sutrakam/graphlume/internal/multirepo"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/store"
)

// Limits SyncService enforces and refuses a whole member for. They are mirrored
// here, rather than exported from internal/multirepo, so that one oversized or
// odd row is counted as skipped instead of failing every other contract of the
// member: a service with a single 2 KB route must not vanish from the workspace.
const (
	maxSyncContracts   = 100_000
	maxContractKeySize = 1024
)

// workspaceDBPath is where the federation database lives for a workspace root.
func workspaceDBPath(root string) string {
	return filepath.Join(root, IndexDir, "workspace.db")
}

// memberStoreOpener is the production multirepo.StoreOpener: it opens one
// member's index read-only through the same door `system` uses, so a query
// never migrates or writes another checkout's index.
//
// The schema check comes from openReadOnly and is returned unwrapped, which is
// what lets the pool set MemberOpenError.SchemaMismatch and the caller tell
// "re-index with this build" from "the file is missing".
func memberStoreOpener(ctx stdctx.Context, dbPath string) (*multirepo.OpenedStore, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, err
	}
	db, err := openReadOnly(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	probe, err := multirepo.NewRunProbe(ctx, db.DB)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	// The probe is the single definition of "the committed run", so the run a
	// sync reads and the run staleness later compares against cannot disagree.
	runID, seq, err := probe(ctx)
	if err != nil {
		_ = db.Close()
		return nil, err // multirepo.ErrNoCommittedRun when nothing is committed
	}
	return &multirepo.OpenedStore{
		Source:      registry.New(db),
		RunID:       runID,
		MutationSeq: seq,
		Close:       db.Close,
		Probe:       probe,
	}, nil
}

// newMemberPool builds the pool the workspace commands read members through.
// The janitor is off: these are short-lived commands, and a background
// goroutine would outlive nothing it was meant to serve.
func newMemberPool() *multirepo.HotStorePool {
	cfg := multirepo.DefaultPoolConfig()
	cfg.DisableJanitor = true
	return multirepo.NewHotStorePool(cfg, memberStoreOpener)
}

// syncTarget is one member the sync will try, with the reason it already
// cannot be when resolving its location failed.
type syncTarget struct {
	ID     string
	Role   string
	Root   string
	DBPath string
	Err    string
}

// syncMemberResult is the outcome for one member.
type syncMemberResult struct {
	Service        string `json:"service"`
	RelPath        string `json:"rel_path,omitempty"`
	Status         string `json:"status"` // synced | up_to_date | failed
	Contracts      int    `json:"contracts"`
	Skipped        int    `json:"skipped,omitempty"`
	LinksTruncated bool   `json:"links_truncated,omitempty"`
	RunID          int64  `json:"run_id,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

// syncResult is the structured outcome of `workspace sync`.
type syncResult struct {
	WorkspaceRoot string             `json:"workspace_root"`
	WorkspaceDB   string             `json:"workspace_db"`
	Members       []syncMemberResult `json:"members"`
	Synced        int                `json:"synced"`
	UpToDate      int                `json:"up_to_date"`
	Failed        int                `json:"failed"`
}

func newWorkspaceSyncCmd(g *Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Export each member's HTTP routes, calls and tables into the workspace database",
		Long: `Reads each member's committed index read-only and records its boundary
contracts in the workspace database, then links contracts that meet across
members: an outbound call to the route another member declares, or a table one
member writes and another reads. 'graphlume workspace links' reads the result.

Members come from --members (an estate file, the format 'graphlume system' reads)
and --member name=path, or, with neither, from the services 'graphlume workspace
detect' finds under the project root. One member is enough: the links appear as
the other members are synced.

The workspace root is the estate file's directory with --members, else the
project root. The database defaults to <workspace root>/.graphlume/workspace.db.
A member outside the workspace root is reported as failed, because the database
stores each member relative to that root.

Every contract is a heuristic (confidence medium, source heuristic): matching
'/users/{id}' against '/users/:id' is a guess no compiler proved, so nothing
here is ever 'exact'. A failed member does not stop the others; the command
reports them all and then exits 1.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWorkspaceSync(cmd, g)
		},
	}
	cmd.Flags().StringArray("member", nil, "a member as name=path (repository directory or its index.db), repeatable")
	cmd.Flags().String("members", "", "read the members from this estate file (YAML: members: [{name, index}])")
	return cmd
}

// resolveSyncTargets decides which members to sync and the workspace root the
// relative paths are taken against.
func resolveSyncTargets(cmd *cobra.Command, g *Global) (targets []syncTarget, wsRoot string, err error) {
	declared, err := declaredMembers(cmd)
	if err != nil {
		return nil, "", err
	}

	if len(declared) > 0 {
		wsRoot = g.Config().RepoRoot
		if file, _ := cmd.Flags().GetString("members"); file != "" {
			abs, absErr := filepath.Abs(file)
			if absErr != nil {
				return nil, "", fmt.Errorf("workspace sync: resolve estate file %q: %w", file, absErr)
			}
			// Paths in the estate file resolve against the file, so the
			// members live relative to it as well.
			wsRoot = filepath.Dir(abs)
		}
		members, err := normalizeMembers(declared)
		if err != nil {
			return nil, "", err
		}
		for _, m := range members {
			t := syncTarget{ID: m.Name, Role: "service"}
			root, db, mErr := memberIndexFile(m)
			if mErr != nil {
				t.Err = mErr.Error()
			} else {
				t.Root, t.DBPath = root, db
			}
			targets = append(targets, t)
		}
		return targets, wsRoot, nil
	}

	root := g.Config().RepoRoot
	topo, err := workspace.DetectWorkspace(root)
	if err != nil {
		return nil, "", fmt.Errorf("detect workspace %s: %w", root, err)
	}
	if !topo.IsWorkspace || len(topo.Services) == 0 {
		return nil, "", fmt.Errorf("no members to sync: %s is not a partitioned workspace and no members were named.\n"+
			"  graphlume workspace sync --members ./estate.yml\n"+
			"  graphlume workspace sync --member orders=../orders --member web=../web", root)
	}
	for _, s := range topo.Services {
		memberRoot := filepath.Join(topo.Root, s.RelPath)
		role := s.Role
		switch role {
		case "service", "shared_lib", "gateway", "infra":
		default:
			role = "service"
		}
		targets = append(targets, syncTarget{ID: s.ID, Role: role, Root: memberRoot, DBPath: projectIndexPath(memberRoot)})
	}
	return targets, topo.Root, nil
}

func runWorkspaceSync(cmd *cobra.Command, g *Global) error {
	ctx := cmd.Context()
	targets, wsRoot, err := resolveSyncTargets(cmd, g)
	if err != nil {
		return err
	}
	wsRoot = filepath.Clean(wsRoot)

	dbPath, _ := cmd.Flags().GetString("workspace-db")
	if dbPath == "" {
		dbPath = workspaceDBPath(wsRoot)
	}
	if dbPath, err = filepath.Abs(dbPath); err != nil {
		return fmt.Errorf("workspace sync: resolve workspace db: %w", err)
	}

	wdb, err := store.InitWorkspaceDB(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("workspace sync: %w", err)
	}
	defer func() { _ = wdb.Close() }()
	mgr := multirepo.NewWorkspaceManager(wdb)

	pool := newMemberPool()
	defer func() { _ = pool.Close() }()

	res := syncResult{WorkspaceRoot: wsRoot, WorkspaceDB: dbPath}
	for _, t := range targets {
		r := syncOneMember(ctx, mgr, wdb, pool, wsRoot, t)
		switch r.Status {
		case "synced":
			res.Synced++
		case "up_to_date":
			res.UpToDate++
		default:
			res.Failed++
		}
		res.Members = append(res.Members, r)
	}

	out := cmd.OutOrStdout()
	if g.JSON {
		if err := emitJSON(out, res); err != nil {
			return err
		}
	} else {
		renderSyncResult(out, res)
	}
	if res.Failed > 0 {
		// The report already names each failure; an "error:" line under it
		// would repeat it. The code is what lets CI notice a partial sync.
		return &ExitCodeError{Code: 1, Silent: true,
			Err: fmt.Errorf("workspace sync: %d member(s) failed", res.Failed)}
	}
	return nil
}

func syncOneMember(ctx stdctx.Context, mgr *multirepo.WorkspaceManager, wdb *store.DB,
	pool *multirepo.HotStorePool, wsRoot string, t syncTarget) syncMemberResult {
	r := syncMemberResult{Service: t.ID}
	fail := func(format string, a ...any) syncMemberResult {
		r.Status, r.Reason = "failed", fmt.Sprintf(format, a...)
		return r
	}
	if t.Err != "" {
		return fail("%s", t.Err)
	}

	// SyncService refuses a path that escapes the workspace, and storing one
	// would make the database depend on where it was built, so say so here in
	// words about the member instead of surfacing the validator's.
	rel, relErr := filepath.Rel(wsRoot, t.Root)
	if relErr != nil || !filepath.IsLocal(rel) {
		return fail("member root %s is outside the workspace root %s; the workspace database stores members "+
			"relative to its root, so run sync from a directory that contains every member (or use --members "+
			"with an estate file next to them)", t.Root, wsRoot)
	}
	r.RelPath = rel

	h, err := pool.Acquire(ctx, t.ID, filepath.Clean(t.DBPath))
	if err != nil {
		return fail("%s", multirepo.DescribeOpenFailure(t.DBPath, err))
	}
	defer h.Release()
	r.RunID = h.RunID

	run, err := h.Source.Run(ctx, h.RunID)
	if err != nil {
		return fail("read run %d of %s: %v", h.RunID, t.DBPath, err)
	}
	contracts, skipped, err := exportContracts(ctx, h.Source, h.RunID, t.ID)
	if err != nil {
		return fail("read contracts from %s: %v", t.DBPath, err)
	}
	r.Contracts, r.Skipped = len(contracts), skipped

	schema, err := store.LatestVersion()
	if err != nil {
		return fail("read schema version: %v", err)
	}
	err = mgr.SyncService(ctx, multirepo.WorkspaceService{
		ServiceID:      t.ID,
		RelPath:        rel,
		Role:           t.Role,
		DBPath:         filepath.Clean(t.DBPath),
		RunStartedUnix: run.StartedUnix,
		CommittedRunID: h.RunID,
		MutationSeq:    h.MutationSeq,
		SchemaVersion:  schema,
		Digest:         run.RegistryDigest,
	}, contracts)
	switch {
	case errors.Is(err, multirepo.ErrStaleSync):
		// Another sync already recorded a newer state. Not a failure: the
		// workspace is ahead of this member, not behind it.
		r.Status, r.Reason, r.Contracts, r.Skipped = "up_to_date", "already up to date (workspace holds a newer sync)", 0, 0
		return r
	case err != nil:
		return fail("%v", err)
	}

	var trunc int
	if err := wdb.QueryRowContext(ctx,
		`SELECT links_truncated FROM workspace_services WHERE service_id = ?`, t.ID).Scan(&trunc); err == nil {
		r.LinksTruncated = trunc != 0
	}
	r.Status = "synced"
	return r
}

// exportContracts turns a member's endpoints, outbound calls and table usage
// into workspace contracts. Rows SyncService would refuse are counted as
// skipped rather than failing the member.
func exportContracts(ctx stdctx.Context, src multirepo.MemberSource, runID int64, serviceID string) ([]multirepo.ExportedContract, int, error) {
	var out []multirepo.ExportedContract
	skipped := 0
	add := func(c multirepo.ExportedContract) {
		key := strings.TrimSpace(c.CanonicalKey)
		if key == "" || len(key) > maxContractKeySize || len(out) >= maxSyncContracts {
			skipped++
			return
		}
		c.CanonicalKey, c.ServiceID = key, serviceID
		// Heuristic by construction: the index records what the source text
		// says, not what a compiler proved, and "exact" would be refused.
		c.Source, c.Confidence = "heuristic", "medium"
		out = append(out, c)
	}

	endpoints, err := src.EndpointReport(ctx, runID)
	if err != nil {
		return nil, 0, fmt.Errorf("endpoints: %w", err)
	}
	for _, e := range endpoints {
		add(multirepo.ExportedContract{Direction: "provides", ContractKind: "http_route",
			CanonicalKey: e.Signature, SymbolFQN: e.Handler, FilePath: e.FilePath, Line: e.Line})
	}

	calls, err := src.ClientCallReport(ctx, runID)
	if err != nil {
		return nil, 0, fmt.Errorf("client calls: %w", err)
	}
	for _, c := range calls {
		// A call assembled at run time has no path to match on.
		if c.Dynamic || strings.TrimSpace(c.Path) == "" {
			skipped++
			continue
		}
		add(multirepo.ExportedContract{Direction: "consumes", ContractKind: "http_route",
			CanonicalKey: callKey(c.Method, c.Path), SymbolFQN: c.Caller, FilePath: c.FilePath, Line: c.Line})
	}

	tables, err := src.TableUsageReport(ctx, runID)
	if err != nil {
		return nil, 0, fmt.Errorf("tables: %w", err)
	}
	for _, tb := range tables {
		if tb.Writers > 0 {
			add(multirepo.ExportedContract{Direction: "writes", ContractKind: "db_table", CanonicalKey: tb.Table})
		}
		if tb.Readers > 0 {
			add(multirepo.ExportedContract{Direction: "reads", ContractKind: "db_table", CanonicalKey: tb.Table})
		}
	}
	return out, skipped, nil
}

// callKey is the route key a call is matched on: "METHOD /path", or just
// "/path" when the call names no verb. The scheme, host, query and fragment are
// dropped because the host is not knowable from the text and the query is an
// argument rather than part of the route, the same reduction `system` applies.
func callKey(method, path string) string {
	p := strings.TrimSpace(path)
	if i := strings.Index(p, "://"); i >= 0 {
		rest := p[i+3:]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			p = rest[j:]
		} else {
			p = "/"
		}
	}
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if m := strings.ToUpper(strings.TrimSpace(method)); m != "" {
		return m + " " + p
	}
	return p
}

func renderSyncResult(out interface{ Write([]byte) (int, error) }, res syncResult) {
	w := func(format string, a ...any) { _, _ = fmt.Fprintf(out, format, a...) }
	w("Workspace: %s\nDatabase:  %s\n\n", res.WorkspaceRoot, res.WorkspaceDB)
	names := make([]syncMemberResult, len(res.Members))
	copy(names, res.Members)
	sort.SliceStable(names, func(i, j int) bool { return names[i].Service < names[j].Service })
	for _, m := range names {
		switch m.Status {
		case "synced":
			line := fmt.Sprintf("synced %d contract(s)", m.Contracts)
			if m.Skipped > 0 {
				line += fmt.Sprintf(", %d skipped (dynamic, empty or oversized)", m.Skipped)
			}
			if m.LinksTruncated {
				line += "; links truncated (a contract matched more counterparts than the cap)"
			}
			w("  %-20s %s\n", m.Service, line)
		case "up_to_date":
			w("  %-20s %s\n", m.Service, m.Reason)
		default:
			w("  %-20s failed: %s\n", m.Service, m.Reason)
		}
	}
	w("\n%d synced, %d already up to date, %d failed\n", res.Synced, res.UpToDate, res.Failed)
}
