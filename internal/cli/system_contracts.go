package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/sutrakam/graphlume/internal/analyze/api"
	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/multirepo"
)

// newSystemContractsCmd checks each member's outbound HTTP calls against the
// routes the members declare.
//
// A subcommand of `system` rather than a section of it or a command of its
// own. It needs exactly what `system` needs — the member list, the read-only
// opens, the unreadable-member verdict — so it shares those flags and that
// code instead of restating them. It is not a section, because the system
// graph answers "what depends on what" and prints its edges first, while this
// answers "what no longer lines up" and has no edges to print; folded into one
// report, each would bury the other.
func newSystemContractsCmd(_ *Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "contracts",
		Short: "Check each member's HTTP calls against the routes the members declare",
		Long: `Compares the outbound HTTP calls indexed in every member with the routes
indexed in every member, and reports what does not line up:

  method mismatch          the path of a call matches a route, and no route
                           with that path accepts the method the call uses
  path-variable mismatch   a route starts where the call starts, and the two
                           differ in how many path segments follow
  no matching route        nothing any member declares matches the call: a
                           broken call, or a call out of this system
  base path unverified     the call and a route agree only if a mount point,
                           gateway prefix or client base URL is assumed that
                           no index holds
  routes no call reaches   informational, and the weakest section

It takes the members exactly as 'graphlume system' does:

  graphlume system contracts --member web=../web/.graphlume/index.db \
                              --member orders=../orders/.graphlume/index.db

  graphlume system contracts --members ./graphlume.system.yml

HOW FAR TO TRUST A FINDING. Each carries a confidence, and none is ever
'exact' or above 'high': only a compiler index can make 'exact', and none
spans two repositories. A literal path that meets a literal route is 'high'; a
match that needed a path variable to absorb a segment is 'medium'; anything
resting on the literal prefix of a run-time target, or on an assumed base
path, is 'low'. When part of the route side was not read — a member whose
index is missing, partial or stale, or a member holding a language routes are
not extracted from — every finding loses one tier and the report says which
member cost it.

WHAT IS COMPARED, AND WHAT IS NOT. The method, the path, and the number of
path segments. Path variables are matched across syntaxes ({id}, :id, <id>).
A host is shown but never used, because no index records which member a
hostname is. A target that begins with a run-time value, typically a base URL
from configuration, keeps none of its path in the index, and is listed under
'not compared' rather than guessed at. Query, header and body parameters are
not in the index on either side and are not compared.

The report begins with the members and, for each, the languages its outbound
calls were and were not read in: calls are extracted from Go and the
JavaScript family only, so a Java or Python member makes calls this report
cannot see, and says so. Calls made from test files are counted and set
aside, because a test calling a server it started is not a client of this
system. The report ends with the routes no indexed call reaches, which is a
list of leads and never a list of dead code.

Every finding names the file and line of the call, and of each route it is
about. Each section lists at most --limit rows, strongest first, and counts
what it cut.

Exit status is 0 when every member was read, and 2 when one was not. Findings
do not change it: they are heuristic leads for a person or an agent to check,
not a gate.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSystemContracts(cmd)
		},
	}
	cmd.Flags().IntP("limit", "n", multirepo.DefaultMaxContractRows,
		"maximum rows to list in each section")
	return cmd
}

func runSystemContracts(cmd *cobra.Command) error {
	limit, _ := cmd.Flags().GetInt("limit")
	if limit <= 0 {
		return fmt.Errorf("system contracts: --limit must be positive, got %d", limit)
	}

	members, closeAll, err := openSystemMembers(cmd)
	if err != nil {
		return err
	}
	defer closeAll()

	var stale time.Duration
	if d, _ := cmd.Flags().GetDuration("stale-after"); d > 0 {
		stale = d
	}
	fed, err := newContractFederator(limit, stale)
	if err != nil {
		return fmt.Errorf("system contracts: %w", err)
	}
	rep, err := fed.Contracts(cmd.Context(), members)
	if err != nil {
		return err
	}

	if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
		if err := emitJSON(cmd.OutOrStdout(), rep); err != nil {
			return err
		}
	} else if err := multirepo.RenderContracts(cmd.OutOrStdout(), rep); err != nil {
		return fmt.Errorf("system contracts: render report: %w", err)
	}

	if !rep.Complete() {
		// The same verdict, and the same code, as `system`: the report is on
		// stdout and names the missing members itself.
		return &ExitCodeError{Code: systemExitIncomplete, Silent: true,
			Err: fmt.Errorf("system contracts: %d member(s) could not be read; every count is a floor",
				len(rep.UnavailableMembers))}
	}
	return nil
}

// newContractFederator builds the federator `system contracts` and the policy
// contracts rule both use, so the two cannot disagree about which clients and
// frameworks the extraction covers or which paths count as tests. A zero
// staleAfter keeps the federator's default.
func newContractFederator(limit int, staleAfter time.Duration) (*multirepo.Federator, error) {
	clients := api.SupportedClients()
	clientNames := make([]string, 0, len(clients))
	for _, c := range clients {
		clientNames = append(clientNames, string(c))
	}
	frameworks := api.SupportedFrameworks()
	frameworkNames := make([]string, 0, len(frameworks))
	for _, f := range frameworks {
		frameworkNames = append(frameworkNames, string(f))
	}

	opts := []multirepo.Option{
		multirepo.WithRunningVersion(Version),
		multirepo.WithMaxContractRows(limit),
		multirepo.WithExtractionCoverage(clientNames, frameworkNames),
		multirepo.WithTestPaths(capability.IsTestPath),
	}
	if staleAfter > 0 {
		opts = append(opts, multirepo.WithStaleAfter(staleAfter))
	}
	return multirepo.New(opts...)
}
