package cli

import (
	stdctx "context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sutrakam/graphlume/internal/multirepo"
	"github.com/sutrakam/graphlume/internal/store"
)

// linkRow is one contract link as `workspace links` reports it.
type linkRow struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Kind       string `json:"kind"`
	Key        string `json:"key"`
	Rule       string `json:"rule"`
	Confidence string `json:"confidence"`
	Stale      bool   `json:"stale"`
}

// linksResult is the structured outcome of `workspace links`.
type linksResult struct {
	Service         string    `json:"service"`
	Direction       string    `json:"direction"` // downstream | upstream
	Links           []linkRow `json:"links"`
	Truncated       bool      `json:"truncated,omitempty"`
	FanoutTruncated bool      `json:"fanout_truncated,omitempty"`
	StaleServices   []string  `json:"stale_services,omitempty"`
}

func newWorkspaceLinksCmd(g *Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "links <service>",
		Short: "Show the cross-service contract links recorded for one service",
		Long: `Lists what a service depends on (its outbound calls answered by another
member's routes, the tables it shares), or with --upstream, which members depend
on it. Read from the workspace database 'graphlume workspace sync' writes.

A link is marked stale when either member has been re-indexed since it was last
synced: the link may describe a run that no longer exists, so re-run
'graphlume workspace sync'. Every link is a heuristic match and its confidence
is never 'exact'.

--key narrows the answer to one contract key as recorded on the queried
service's side (for example --key 'GET /api/orders').`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkspaceLinks(cmd, g, args[0])
		},
	}
	cmd.Flags().Bool("upstream", false, "show the members that depend on this service instead of what it depends on")
	cmd.Flags().String("key", "", "only links whose contract key on this service's side equals this")
	cmd.Flags().Int("limit", 50, "most links to print")
	return cmd
}

func runWorkspaceLinks(cmd *cobra.Command, g *Global, service string) error {
	ctx := cmd.Context()

	dbPath, _ := cmd.Flags().GetString("workspace-db")
	if dbPath == "" {
		dbPath = workspaceDBPath(g.Config().RepoRoot)
	}
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("no workspace database at %s. Run 'graphlume workspace sync' first "+
			"(or point --workspace-db at the database it wrote)", dbPath)
	}
	wdb, err := store.OpenWorkspaceDBReadOnly(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("workspace links: %w", err)
	}
	defer func() { _ = wdb.Close() }()

	known, err := workspaceServiceIDs(ctx, wdb.DB)
	if err != nil {
		return fmt.Errorf("workspace links: %w", err)
	}
	if !containsString(known, service) {
		return fmt.Errorf("service %q is not in the workspace database %s (known: %s). "+
			"Run 'graphlume workspace sync' to add it", service, dbPath, strings.Join(known, ", "))
	}

	pool := newMemberPool()
	defer func() { _ = pool.Close() }()
	mgr := multirepo.NewWorkspaceManager(wdb).WithPositionLookup(func(ctx stdctx.Context, id string) (multirepo.MemberPosition, bool) {
		var path string
		if err := wdb.QueryRowContext(ctx, `SELECT db_path FROM workspace_services WHERE service_id = ?`, id).Scan(&path); err != nil {
			return multirepo.MemberPosition{}, false
		}
		h, err := pool.Acquire(ctx, id, path)
		if err != nil {
			// An unreadable member is "unknown", not stale: claiming it moved
			// would be a guess, and the sync command says why it is unreadable.
			return multirepo.MemberPosition{}, false
		}
		defer h.Release()
		run, err := h.Source.Run(ctx, h.RunID)
		if err != nil || run == nil {
			return multirepo.MemberPosition{}, false
		}
		// The mutation counter too: an incremental re-index keeps the run id.
		// And the start time: a rebuilt index restarts at run 1.
		return multirepo.MemberPosition{RunStartedUnix: run.StartedUnix, RunID: h.RunID,
			MutationSeq: h.MutationSeq, Digest: run.RegistryDigest}, true
	})

	upstream, _ := cmd.Flags().GetBool("upstream")
	key, _ := cmd.Flags().GetString("key")
	limit, _ := cmd.Flags().GetInt("limit")
	if limit <= 0 {
		limit = 50
	}

	res := linksResult{Service: service, Direction: "downstream"}
	var page multirepo.LinkPage
	if upstream {
		res.Direction = "upstream"
		page, err = mgr.FindUpstreamLinks(ctx, service, key, limit)
	} else {
		page, err = mgr.FindDownstreamLinks(ctx, service, key, limit)
	}
	if err != nil {
		return fmt.Errorf("workspace links: %w", err)
	}

	keys := map[int64]string{}
	keyOf := func(id int64) string {
		if k, ok := keys[id]; ok {
			return k
		}
		var k string
		_ = wdb.QueryRowContext(ctx, `SELECT canonical_key FROM exported_contracts WHERE id = ?`, id).Scan(&k)
		keys[id] = k
		return k
	}
	res.Links = []linkRow{}
	for _, l := range page.Links {
		// The key shown is the caller's side for a downstream query and the
		// route's side for an upstream one: the queried service's own contract.
		contractID := l.SrcContractID
		if upstream {
			contractID = l.DstContractID
		}
		res.Links = append(res.Links, linkRow{
			From: l.SrcServiceID, To: l.DstServiceID, Kind: l.LinkKind, Key: keyOf(contractID),
			Rule: l.MatchRule, Confidence: l.Confidence, Stale: l.Stale,
		})
	}
	res.Truncated, res.FanoutTruncated, res.StaleServices = page.Truncated, page.FanoutTruncated, page.StaleServices

	out := cmd.OutOrStdout()
	if g.JSON {
		return emitJSON(out, res)
	}
	renderLinks(out, res, limit)
	return nil
}

func workspaceServiceIDs(ctx stdctx.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT service_id FROM workspace_services ORDER BY service_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func containsString(list []string, s string) bool {
	i := sort.SearchStrings(list, s)
	return i < len(list) && list[i] == s
}

func renderLinks(out interface{ Write([]byte) (int, error) }, res linksResult, limit int) {
	if len(res.Links) == 0 {
		what := "depends on another member"
		if res.Direction == "upstream" {
			what = "is depended on by another member"
		}
		_, _ = fmt.Fprintf(out, "No links recorded: %s %s in the last sync.\n", res.Service, what)
	} else {
		w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "FROM\t->\tTO\tKIND\tKEY / RULE\tCONFIDENCE\tSTALE")
		for _, l := range res.Links {
			stale := ""
			if l.Stale {
				stale = "stale"
			}
			_, _ = fmt.Fprintf(w, "%s\t->\t%s\t%s\t%s (%s)\t%s\t%s\n",
				l.From, l.To, l.Kind, l.Key, l.Rule, l.Confidence, stale)
		}
		_ = w.Flush()
	}
	if res.Truncated {
		_, _ = fmt.Fprintf(out, "\nNote: more links exist than the --limit of %d; raise it or narrow with --key.\n", limit)
	}
	if res.FanoutTruncated {
		_, _ = fmt.Fprintf(out, "\nNote: %s's links were capped at sync time because a contract matched more "+
			"counterparts than the cap, so this list is incomplete.\n", res.Service)
	}
	if len(res.StaleServices) > 0 {
		_, _ = fmt.Fprintf(out, "\nNote: %s re-indexed since the last sync, so links touching %s may be out of date. "+
			"Re-run 'graphlume workspace sync'.\n", plural(len(res.StaleServices), "service was", "services were"),
			strings.Join(res.StaleServices, ", "))
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
