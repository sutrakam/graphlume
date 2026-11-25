package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/sutrakam/graphlume/internal/capability/impact"
	"github.com/sutrakam/graphlume/internal/multirepo"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// newSystemCallersCmd lists a symbol's callers across the estate.
//
// A subcommand of `system`, for the reason `system contracts` is one: it needs
// exactly the member list, the read-only opens and the unreadable-member
// verdict `system` has, and a second spelling of those would drift. It is the
// single-repository `callers` question with one more hop: a call in another
// member into a Go module this member defines.
func newSystemCallersCmd(_ *Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "callers <member>:<symbol>",
		Short: "List a symbol's callers in its own member and, through Go module imports, in the others",
		Long: `Lists the callers of one symbol in one member of the estate: its callers in
that member's own graph, and its callers in every other member that imports a
Go module the member defines.

  graphlume system callers lib:Do --members ./graphlume.system.yml
  graphlume system callers lib:Do --path sub/ --member lib=../lib --member web=../web

Each row names the member, the caller, the file and line of the call, the
confidence and the leg: 'local' for an edge the member's own index recorded,
'go-module' for a call stitched across a repository boundary. A stitched row is
never 'exact' — no compiler checked the two repositories together — and is
'high' at most. When several definitions share the name, the first is answered
and the others are listed; --path chooses among them, as for 'callers'.

Package-qualified calls (lib.Do()) are stitched, and so are method calls whose
receiver's type the source shows is a provider type: a parameter or local of a
stated type (c *lib.Client), a provider variable that declares its type
(lib.Typed.Send()), or a local assigned once from a provider function that
declares its result (c := lib.NewClient()). Chains, fields, reassigned locals
and untyped declarations are not followed, so the answer is a lower bound;
'lib:Client.Send' names a method. Version warnings — the importer requires a release the provider's
index may not be, or a replace directive points elsewhere — are printed under
the rows they concern.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSystemCallers(cmd, args[0])
		},
	}
	cmd.Flags().String("path", "", "disambiguate the symbol by file path substring")
	cmd.Flags().IntP("limit", "n", multirepo.DefaultSystemCallersLimit, "maximum callers to report")
	return cmd
}

// newSystemImpactCmd walks a symbol's blast radius across the estate.
func newSystemImpactCmd(_ *Global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "impact <member>:<symbol>",
		Short: "Walk a symbol's blast radius across repositories through Go module imports",
		Long: `Walks inward from one symbol in one member: to what depends on it inside that
member, to the calls other members make into it through a Go module import,
and on to what depends on those callers inside their own repositories, up to
--depth hops in all.

  graphlume system impact lib:Do --members ./graphlume.system.yml
  graphlume system impact lib:Do --depth 5 --json --members ./graphlume.system.yml

Every row is labelled with its member, the evidence of the hop that reached it,
its depth, the edge it came through ('go-module' for a repository crossing) and
the WEAKEST confidence on its path. A path that crosses a repository is 'high'
at most; a path inside one member keeps that member's recorded grade. The walk
inside each member follows the same edge kinds and the same default confidence
floor as 'graphlume impact' (--min-confidence low restores the rest).

The result is a LOWER BOUND, always: unresolved references, method calls whose
receiver's type the source does not show (chains, fields, reassigned locals,
untyped declarations) and repositories outside the estate are absent. When a bound stops the walk the output says which.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSystemImpact(cmd, args[0])
		},
	}
	cmd.Flags().String("path", "", "disambiguate the symbol by file path substring")
	cmd.Flags().Int("depth", multirepo.DefaultSystemImpactDepth, "hops to walk out from the symbol, crossings included")
	cmd.Flags().IntP("limit", "n", multirepo.DefaultSystemImpactRows, "maximum dependents to report")
	cmd.Flags().String("min-confidence", "",
		fmt.Sprintf("confidence floor for every hop (exact|high|medium|low) (default %s)", impact.DefaultMinConfidence))
	return cmd
}

func runSystemCallers(cmd *cobra.Command, arg string) error {
	limit, _ := cmd.Flags().GetInt("limit")
	if limit <= 0 {
		return fmt.Errorf("system callers: --limit must be positive, got %d", limit)
	}
	members, closeAll, err := openSystemMembers(cmd)
	if err != nil {
		return err
	}
	defer closeAll()

	pick, err := pickSystemSymbol(cmd, members, arg)
	if err != nil {
		return err
	}
	fed, err := multirepo.New(multirepo.WithRunningVersion(Version))
	if err != nil {
		return fmt.Errorf("system callers: %w", err)
	}
	rep, err := fed.SystemCallers(cmd.Context(), members, pick.target, limit)
	if err != nil {
		return err
	}

	if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
		if err := emitJSON(cmd.OutOrStdout(), map[string]any{
			"target": rep.Target, "ambiguous": len(pick.all) > 1, "definitions": pick.locations(),
			"callers": rep.Callers, "truncated": rep.Truncated, "truncation_note": rep.TruncationNote,
			"warnings": rep.Warnings, "unavailable_members": rep.UnavailableMembers, "caveats": rep.Caveats,
		}); err != nil {
			return err
		}
	} else {
		out := cmd.OutOrStdout()
		pick.announce(out)
		if err := multirepo.RenderSystemCallers(out, rep); err != nil {
			return fmt.Errorf("system callers: render: %w", err)
		}
		pick.list(out)
	}
	if !rep.Complete() {
		return &ExitCodeError{Code: systemExitIncomplete, Silent: true,
			Err: fmt.Errorf("system callers: %d member(s) could not be read; the callers are a floor",
				len(rep.UnavailableMembers))}
	}
	return nil
}

func runSystemImpact(cmd *cobra.Command, arg string) error {
	depth, _ := cmd.Flags().GetInt("depth")
	limit, _ := cmd.Flags().GetInt("limit")
	if depth <= 0 {
		return fmt.Errorf("system impact: --depth must be positive, got %d", depth)
	}
	if limit <= 0 {
		return fmt.Errorf("system impact: --limit must be positive, got %d", limit)
	}
	floor := impact.DefaultMinConfidence
	if raw, _ := cmd.Flags().GetString("min-confidence"); raw != "" {
		floor = model.Confidence(raw)
		if !floor.Valid() {
			return fmt.Errorf("--min-confidence %q: want exact, high, medium or low", raw)
		}
	}

	members, closeAll, err := openSystemMembers(cmd)
	if err != nil {
		return err
	}
	defer closeAll()

	pick, err := pickSystemSymbol(cmd, members, arg)
	if err != nil {
		return err
	}
	fed, err := multirepo.New(multirepo.WithRunningVersion(Version))
	if err != nil {
		return fmt.Errorf("system impact: %w", err)
	}
	rep, err := fed.SystemImpact(cmd.Context(), members, pick.target, multirepo.ImpactOptions{
		MaxDepth: depth, MaxRows: limit, Kinds: impact.DefaultKinds, MinConfidence: floor,
	})
	if err != nil {
		return err
	}

	if jsonOut, _ := cmd.Flags().GetBool("json"); jsonOut {
		if err := emitJSON(cmd.OutOrStdout(), map[string]any{
			"target": rep.Target, "ambiguous": len(pick.all) > 1, "definitions": pick.locations(),
			"max_depth": rep.MaxDepth, "min_confidence": rep.MinConfidence,
			"members": rep.Members, "dependents": rep.Rows,
			"truncated": rep.Truncated, "truncation_note": rep.TruncationNote,
			"warnings": rep.Warnings, "unavailable_members": rep.UnavailableMembers, "caveats": rep.Caveats,
		}); err != nil {
			return err
		}
	} else {
		out := cmd.OutOrStdout()
		pick.announce(out)
		if err := multirepo.RenderSystemImpact(out, rep); err != nil {
			return fmt.Errorf("system impact: render: %w", err)
		}
		pick.list(out)
	}
	if !rep.Complete() {
		return &ExitCodeError{Code: systemExitIncomplete, Silent: true,
			Err: fmt.Errorf("system impact: %d member(s) could not be read; the blast radius is a floor",
				len(rep.UnavailableMembers))}
	}
	return nil
}

// systemPick is the symbol a system query is about, and the other
// definitions that shared its name. The resolution itself is
// multirepo.PickSymbol, which the estate MCP tool uses too.
type systemPick struct {
	target multirepo.SymbolRef
	all    []registry.SymbolHit
	name   string
}

// pickSystemSymbol resolves <member>:<symbol> against that member's index,
// with the conventions of the single-repository callers command: exact name
// first, Owner.member as a fallback, --path to choose among several, and the
// first one answered when the choice is left open.
func pickSystemSymbol(cmd *cobra.Command, members []multirepo.Member, arg string) (systemPick, error) {
	path, _ := cmd.Flags().GetString("path")
	p, err := multirepo.PickSymbol(cmd.Context(), members, arg, path, targetLookupLimit)
	if err != nil {
		return systemPick{}, err
	}
	return systemPick{target: p.Target, all: p.All, name: p.Name}, nil
}

func (p systemPick) announce(w io.Writer) {
	if len(p.all) <= 1 {
		return
	}
	fmt.Fprintf(w, "AMBIGUOUS — %s definitions share the name %q in %s. What follows is about the first, %s:%d, "+
		"and says nothing about the others. Pass --path to choose one; they are listed at the end.\n\n",
		definitionCount(len(p.all)), p.name, p.target.Member, p.target.File, p.target.Line)
}

func (p systemPick) list(w io.Writer) {
	if len(p.all) <= 1 {
		return
	}
	fmt.Fprintf(w, "\n%s definitions share the name %q; this answered for the first.\n", definitionCount(len(p.all)), p.name)
	fmt.Fprintln(w, "Pass --path to choose another:")
	for _, s := range p.all {
		fmt.Fprintf(w, "  %s:%d (%s)\n", s.FilePath, s.StartLine, s.Kind)
	}
}

func (p systemPick) locations() []string {
	out := make([]string, 0, len(p.all))
	for _, s := range p.all {
		out = append(out, fmt.Sprintf("%s:%d", s.FilePath, s.StartLine))
	}
	return out
}
