package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/indexer"
	"github.com/sutrakam/graphlume/internal/lsp"
	"github.com/sutrakam/graphlume/internal/pipeline"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/internal/resolve/scip"
	"github.com/sutrakam/graphlume/internal/resolve/scipimport"
	"github.com/sutrakam/graphlume/pkg/model"

	"github.com/spf13/cobra"
)

// resolveBatchSize is how many edges `resolve` buffers before it writes (and
// four times as many reference outcomes). The pipeline's own default is 2,000.
// Every write is a transaction that updates the reference and edge indexes at
// random positions, so a larger batch visits each index page once per batch
// instead of once per few thousand rows: on the 8,800-file server module of
// Elasticsearch the outcome writes took 112 s at 2,000 and 86 s at 20,000.
const resolveBatchSize = 25_000

func newResolveCmd(g *Global) *cobra.Command {
	var minConfidence string
	var noSCIP bool
	var withLSP bool
	var accuracy string
	var allowBuild bool
	var reResolve bool
	var resolveTimeout time.Duration

	cmd := &cobra.Command{
		Use:   "resolve",
		Short: "Bind extracted references to their definitions",
		Long: `Resolves references to symbol definitions, building the call graph.

Indexing extracts references but cannot bind them: doing so requires the
complete symbol table, which does not exist until every file is parsed.

Every binding records the evidence that justified it and a confidence level.
The heuristic pass never produces 'exact' confidence: that is reserved for what
a compiler proved. Two things can: a SCIP index found in the project root, and,
for Go, the standard library's own type checker, which resolve runs by default
(accuracy auto or exact) after the heuristic pass, with no tool to install. It
checks the host platform's view of the code, raises the heuristic edges it
confirms to exact, adds the ones the heuristic could not bind, and withdraws
heuristic call edges it proves false.

For the other languages (accuracy auto or exact), resolve runs the compiler
indexer already installed for each project, as 'graphlume indexers list'
shows it: scip-typescript, scip-python, scip-clang (C and C++, needs a
compile_commands.json), rust-analyzer (a single Cargo package) and scip-java
where the plan allows it. Java without Docker, C/C++ and Rust execute code the
repository chooses, so they run only with --allow-repo-build, which only your
command line sets. When a language with many files has no
indexer installed, resolve says so and prints the command; it never installs. It installs nothing and runs nothing the plan marks blocked. Each index is
written under .graphlume/scip/ and imported; one whose inputs have not changed
since it was written is imported again without being re-run. --accuracy fast
skips all of it.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cfg := g.Config()
			start := time.Now()
			stopProfile := startResolveProfile(os.Getenv(resolveProfileEnv))
			defer stopProfile()

			dbPath := g.DBPath()
			if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("no index at %s — run 'graphlume init' first", dbPath)
			}
			unlock, err := lockIndex(ctx, dbPath)
			if err != nil {
				return err
			}
			defer unlock()
			db, err := openWritable(ctx, g, dbPath)
			if err != nil {
				return err
			}
			defer func() { _ = db.Close() }()

			reg := registry.New(db)
			run, err := reg.CurrentRun(ctx)
			if err != nil {
				// Distinguish "nothing indexed yet" from "the database is
				// locked or corrupt". Collapsing both into one message sent
				// users to re-run indexing when the real problem was a held
				// lock, which indexing would also hit.
				if errors.Is(err, registry.ErrNoCommittedRun) {
					return fmt.Errorf("no committed index — run 'graphlume index' first")
				}
				return fmt.Errorf("read current index revision: %w", err)
			}

			switch accuracy {
			case "", string(config.AccuracyFast), string(config.AccuracyAuto), string(config.AccuracyExact):
			default:
				return fmt.Errorf("--accuracy %q: want fast, auto or exact", accuracy)
			}
			min := model.Confidence(minConfidence)
			if minConfidence != "" && !min.Valid() {
				return fmt.Errorf("--min-confidence %q: want exact, high, medium or low", minConfidence)
			}

			// Resolve rewrites most of the run's edges and sums the file
			// rollups whole when it finishes; until then their triggers need
			// not record each write.
			if err := reg.MarkFileRollupStale(ctx, run.ID); err != nil {
				return err
			}

			if reResolve {
				refsReset, edgesDropped, err := reg.ResetHeuristicResolutions(ctx, run.ID)
				if err != nil {
					return fmt.Errorf("reset heuristic resolutions: %w", err)
				}
				if !g.Quiet {
					fmt.Fprintf(cmd.OutOrStdout(), "Reset %d heuristic reference(s), dropped %d heuristic edge(s) for re-resolution\n", refsReset, edgesDropped)
				}
			}

			// Deployment units join to endpoints in the same pass, and the
			// summary is captured rather than dropped: "0 endpoints bound" and
			// "no manifests declare a build context" are different facts, and a
			// user who cannot tell them apart will read the first as a bug.
			var infra pipeline.InfraBindSummary
			var eventsMerged int
			ui := NewUI(cmd.OutOrStdout())
			var bar *progressBar
			if !g.Quiet && !g.JSON {
				bar = newProgressBar(cmd.OutOrStdout(), "Binding cross-file references", false)
			}
			stats, err := pipeline.Resolve(ctx, reg, run.ID, pipeline.ResolveOptions{
				MinConfidence: min,
				BatchSize:     resolveBatchSize,
				OnProgress: func(done int64) {
					if bar != nil {
						bar.Update(done, run.FilesIndexed)
					}
				},
				OnInfraUnits:   func(s pipeline.InfraBindSummary) { infra = s },
				OnEventsMerged: func(n int) { eventsMerged = n },
			})
			if bar != nil {
				bar.Finish("Bound references across codebase files")
			}
			if err != nil {
				return err
			}
			rs, err := reg.ResolutionStats(ctx, run.ID)
			if err != nil {
				return err
			}
			external, err := reg.ExternalReferences(ctx, run.ID)
			if err != nil {
				return err
			}

			// Table accesses bind in the same pass, for the same reason
			// references do: the table a query names may be declared by a file
			// parsed long after the query was. Doing it here rather than in a
			// command of its own means a user who runs `resolve` gets a
			// complete graph, instead of a graph that is complete except for
			// the part nobody remembered to run.
			access, err := reg.BindTableAccesses(ctx, run.ID)
			if err != nil {
				return err
			}
			cfgUse, err := reg.BindConfigUsages(ctx, run.ID)
			if err != nil {
				return err
			}
			if !g.Quiet && !g.JSON && (access.Bound > 0 || cfgUse.Bound > 0) {
				ui.Success("Linked %d database queries and %d configuration keys", access.Bound, cfgUse.Bound)
			}

			// SCIP auto-discovery: an index.scip in the project root or .graphlume/
			// upgrades heuristic edges to 'exact'. The outcome is always reported,
			// including failure, because a skipped upgrade is otherwise
			// indistinguishable from "no SCIP index was present".
			scipOut := scipAutoOutcome{}
			if !noSCIP {
				scipOut = autoImportSCIP(ctx, g, reg, run.ID, cfg.RepoRoot)
			}

			// The compiler pass for Go, built in: the standard library's own
			// type checker, no tool to install. `fast` skips it, and so does a
			// repository with no Go.
			mode := cfg.Accuracy
			if accuracy != "" {
				mode = config.AccuracyMode(accuracy)
			}
			var lspOut lsp.BridgeOutcome
			var goOut pipeline.GoTypesOutcome
			var ciOut pipeline.CompilerIndexOutcome
			if mode != config.AccuracyFast {
				if withLSP {
					lspOut = lsp.RunLSPBridge(ctx, reg, run.ID, lsp.BridgeOptions{
						Root:           cfg.RepoRoot,
						Logger:         g.Log(),
						AllowRepoBuild: allowBuild,
					})
				}
				goOut = pipeline.CheckGoTypes(ctx, reg, run.ID, pipeline.GoTypesOptions{Root: cfg.RepoRoot})
				if !g.Quiet && !g.JSON && goOut.Ran {
					ui.Success("go/types: %d package(s) checked (%d exact edges confirmed/added)", goOut.Check.Packages, goOut.Import.Edges.Upgraded+goOut.Import.Edges.Inserted)
				}
				// The same for every other language that has a compiler
				// indexer installed: run it, or reuse its last index when
				// nothing it reads has changed, and import what it left.
				ciCfg := cfg
				ciCfg.Accuracy = mode
				ciOut = pipeline.RunCompilerIndexers(ctx, reg, run.ID, pipeline.CompilerIndexOptions{
					Root: cfg.RepoRoot, Config: ciCfg, Logger: g.Log(), AllowRepoBuild: allowBuild,
					Timeout: resolveTimeout,
					OnStepStart: func(step indexer.Step, current, total int) {
						if !g.Quiet && !g.JSON {
							ui.Info("[%d/%d] Running %s on %s...", current, total, step.Indexer, step.Project.Dir)
						}
					},
					OnStepDone: func(step indexer.Step, current, total int, d time.Duration, cached bool, err error) {
						if g.Quiet || g.JSON {
							return
						}
						if cached {
							ui.Success("[%d/%d] %s on %s (reused cached SCIP index)", current, total, step.Indexer, step.Project.Dir)
						} else if err != nil {
							ui.Warn("[%d/%d] %s on %s: %s", current, total, step.Indexer, step.Project.Dir, err.Error())
						} else {
							ui.Success("[%d/%d] %s on %s finished in %s", current, total, step.Indexer, step.Project.Dir, d.Round(10*time.Millisecond))
						}
					},
				})
			}

			ciMode := cfg
			ciMode.Accuracy = mode
			sugg := suggestedIndexers(ctx, reg, run.ID, ciMode)

			// Counted from the table, after every pass above has written to
			// it, so the edge figures describe the graph a query will now
			// read. See registry.ReferenceEdgeTiers for why they are not the
			// per-tier reference counts.
			edgeTiers, edgeTotal, err := reg.ReferenceEdgeTiers(ctx, run.ID)
			if err != nil {
				return err
			}

			// Every pass above has written; store the figures every relationship
			// answer opens with, so a question does not recompute them.
			if err := reg.RefreshRunStats(ctx, run.ID); err != nil {
				return err
			}
			// And the file rollups the Map reads, summed from the same edges.
			rollupStart := time.Now()
			if err := reg.RefreshFileRollup(ctx, run.ID); err != nil {
				return err
			}
			g.Log().Debug("file rollup summed", "duration_ms", time.Since(rollupStart).Milliseconds())
			// Rewriting references and edges frees pages; give them back
			// when there are enough to be worth it.
			reclaimed := g.compactPolicy().after(ctx, reg)

			if g.JSON {
				return emitJSON(cmd.OutOrStdout(), map[string]any{
					"project": cfg.ProjectName, "index_run": run.ID,
					"references": rs.Total, "resolved": rs.Resolved,
					"ambiguous": rs.Ambiguous, "unresolved": rs.Unresolved,
					// external is the part of unresolved that was shown to
					// leave the repository; instantiations the part of
					// resolved recorded as INSTANTIATES rather than CALLS.
					"external":       external,
					"instantiations": stats.Instantiations,
					"events_merged":  eventsMerged,
					// A8 — how much of `ambiguous` is "too common to weigh"
					// rather than "two candidates and no evidence". See
					// infer.Stats.
					"cap_exceeded": stats.CapExceeded,
					"cap_dropped":  stats.CapDropped,
					// binding_rate is the published coverage number, and the
					// only rate emitted here (capability.CoverageFloor says
					// why). ResolutionStats.Rate is deliberately absent: two
					// rates in one payload is how a consumer ends up quoting
					// the one this project does not stand behind.
					//
					// Two different things were published under "edges" and
					// "by_confidence": the edges this pass wrote, and the
					// REFERENCES this pass bound per tier. Both keys keep their
					// meaning, and the graph's own figures are added beside
					// them under names that say what they count.
					"edges":    stats.Edges,
					"bindable": rs.Bindable(), "binding_rate": rs.BindingRate(),
					"by_confidence":       stats.ByConfidence,
					"edges_total":         edgeTotal,
					"edges_by_confidence": edgeTiers,
					"table_accesses": map[string]any{
						"total": access.Total, "bound": access.Bound,
						"unbound": access.Unbound, "edges": access.Edges,
						"unbound_tables": access.UnboundTables,
					},
					"config_usages": map[string]any{
						"total": cfgUse.Total, "bound": cfgUse.Bound,
						"unbound": cfgUse.Unbound, "edges": cfgUse.Edges,
					},
					"deployment_units": map[string]any{
						"units": infra.Units, "with_context": infra.WithContext,
						"endpoints": infra.Endpoints, "bound": infra.Bound,
						"ambiguous": infra.Ambiguous, "edges": infra.Edges,
					},
					"scip":               scipOut.json(),
					"lsp_bridge":         lspBridgeJSON(lspOut),
					"go_types":           goTypesJSON(mode, goOut),
					"compiler_indexers":  compilerIndexersJSON(mode, ciOut),
					"suggested_indexers": sugg,
					"reclaimed_bytes":    reclaimed,
					"duration_ms":        time.Since(start).Milliseconds(),
				})
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Resolved %s (revision %d)\n", cfg.ProjectName, run.ID)
			fmt.Fprintf(out, "  References:  %d\n", rs.Total)
			// The percentage is over BINDABLE references, not over all of
			// them, so this command publishes the same number the MCP tools
			// and the portal do — see capability.CoverageFloor. Ambiguous and
			// Unresolved are printed immediately below, which is the condition
			// ResolutionStats.Bindable places on that denominator.
			fmt.Fprintf(out, "  Resolved:    %d (%.1f%% of the %d that name something this index defines)\n",
				rs.Resolved, rs.BindingRate()*100, rs.Bindable())
			// The tiers of the REFERENCES this pass bound. They were printed
			// under "Edges", where they summed to the resolved references and
			// not to the edge count beside them: 2,564 / 367 / 1 under
			// "Edges: 2342" on sinatra. A reference's tier is known only to the
			// pass that binds it, so a later pass with nothing new to bind
			// prints none.
			var boundNow int64
			for _, n := range stats.ByConfidence {
				boundNow += n
			}
			if boundNow > 0 {
				label := "by confidence:"
				if boundNow != rs.Resolved {
					label = fmt.Sprintf("%d bound in this pass, by confidence:", boundNow)
				}
				fmt.Fprintf(out, "    %s\n", label)
				for _, c := range []model.Confidence{model.ConfidenceHigh, model.ConfidenceMedium, model.ConfidenceLow} {
					if n := stats.ByConfidence[c]; n > 0 {
						fmt.Fprintf(out, "      %-8s %d\n", string(c)+":", n)
					}
				}
			}
			fmt.Fprintf(out, "  Ambiguous:   %d\n", rs.Ambiguous)
			// A8 — name the part of `Ambiguous` that is a cap drop. Without
			// this a name with 200 definitions and a name with two are the
			// same number, and only the second is a disambiguation problem.
			if stats.CapDropped > 0 {
				fmt.Fprintf(out, "    %d had more candidates than the cap allows and were left unbound", stats.CapDropped)
				// Only some paths can rescue an over-cap name by its
				// receiver; where none did, saying "rescued 0" was noise.
				if rescued := stats.CapExceeded - stats.CapDropped; rescued > 0 {
					fmt.Fprintf(out, " (%d more exceeded it and the receiver narrowed them to a binding)", rescued)
				}
				fmt.Fprintln(out)
			}
			// What "unresolved" is, and only that. The resolver records it when
			// no definition with that name is in the index. That covers calls
			// into libraries, and it equally covers calls to code in this
			// repository that the index holds no definition for: a language
			// whose definitions are not extracted, a macro, a file that failed
			// to parse. On redis at least 6,806 of the references labelled
			// "calls outside this repository" were calls to its own functions.
			fmt.Fprintf(out, "  Unresolved:  %d  (no definition of that name is indexed: code outside this\n", rs.Unresolved)
			fmt.Fprintf(out, "               repository, or code in it that the index did not extract)\n")
			// The part the resolver can SHOW leaves the repository: a call
			// qualified by the standard library, a declared dependency, or a
			// type imported from one.
			if external > 0 {
				fmt.Fprintf(out, "    %d shown to leave this repository (a standard-library or dependency call)\n", external)
			}
			if eventsMerged > 0 {
				// `index` counts an event topic once per file that declares
				// it; resolution folds them into one node, so the symbol
				// count here is lower than the one `index` printed.
				fmt.Fprintf(out, "  Events:      %d duplicate event-topic symbol(s) merged into one node per topic (symbol count is now %d lower than `index` printed)\n",
					eventsMerged, eventsMerged)
			}
			if stats.Instantiations > 0 {
				fmt.Fprintf(out, "  Constructs:  %d of the resolved calls create a type (INSTANTIATES, not CALLS)\n", stats.Instantiations)
			}
			// Edge rows, from the table: CALLS, INSTANTIATES, REFERENCES,
			// EXTENDS and IMPLEMENTS, the kinds this pass writes. Fewer than
			// the resolved references because references between the same two
			// definitions share one.
			fmt.Fprintf(out, "  Edges:       %d", edgeTotal)
			switch {
			case edgeTotal > 0 && stats.Edges == edgeTotal:
				fmt.Fprintf(out, "  (one per caller and definition; references between the same pair share one)\n")
			case edgeTotal > 0 && stats.Edges > 0:
				fmt.Fprintf(out, "  (this pass wrote %d; the rest were already there)\n", stats.Edges)
			case edgeTotal > 0:
				fmt.Fprintf(out, "  (this pass found nothing new to bind)\n")
			default:
				fmt.Fprintln(out)
			}
			for _, c := range []model.Confidence{
				model.ConfidenceExact, model.ConfidenceHigh, model.ConfidenceMedium, model.ConfidenceLow,
			} {
				if n := edgeTiers[c]; n > 0 {
					fmt.Fprintf(out, "    %-8s %d\n", string(c)+":", n)
				}
			}
			if access.Total > 0 {
				fmt.Fprintf(out, "  Data access: %d statement(s), %d bound to a declared table, %d edges\n",
					access.Total, access.Bound, access.Edges)
				if access.Unbound > 0 {
					// Not a failure. A query against a table this repository
					// does not declare is a service reading somebody else's
					// database, which is worth naming rather than hiding.
					fmt.Fprintf(out, "    %d access(es) name tables no migration here declares", access.Unbound)
					if len(access.UnboundTables) > 0 {
						shown := access.UnboundTables
						if len(shown) > 6 {
							shown = shown[:6]
						}
						fmt.Fprintf(out, " (e.g. %s)", strings.Join(shown, ", "))
					}
					fmt.Fprintln(out)
				}
			}
			if cfgUse.Total > 0 {
				fmt.Fprintf(out, "  Config:      %d read(s), %d bound to a declared key, %d edges\n",
					cfgUse.Total, cfgUse.Bound, cfgUse.Edges)
				if cfgUse.Unbound > 0 {
					// The finding that catches a deployment failing at start-up:
					// code reads a setting no committed file provides.
					fmt.Fprintf(out, "    %d read(s) name keys no configuration file here declares "+
						"— see 'graphlume config'\n", cfgUse.Unbound)
				}
			}
			if infra.Units > 0 {
				fmt.Fprintf(out, "  Deployment:  %d unit(s), %d naming a build context, %d edges\n",
					infra.Units, infra.WithContext, infra.Edges)
				// Two different silences. A unit that names no build context
				// says what runs without saying which directory it came from —
				// a Kubernetes container or a Terraform resource, and a
				// coverage fact rather than a failure. An endpoint that matched
				// several units is a real ambiguity the join REFUSED to guess
				// at. Reporting them as one number would hide the second.
				if unplaced := infra.WithContext - infra.Bound; unplaced > 0 {
					fmt.Fprintf(out, "    %d unit(s) name a build context that matched no endpoint\n", unplaced)
				}
				if infra.Ambiguous > 0 {
					fmt.Fprintf(out, "    %d endpoint(s) matched more than one unit and were left unbound "+
						"— see 'graphlume infra'\n", infra.Ambiguous)
				}
			}
			scipOut.render(out)
			renderLSPBridge(out, lspOut)
			renderGoTypes(out, mode, goOut)
			renderCompilerIndexers(out, mode, ciOut)
			renderSuggestedIndexers(out, g.Quiet, sugg)
			fmt.Fprintf(out, "  Duration:    %dms\n", time.Since(start).Milliseconds())
			writeReclaimed(out, "  ", reclaimed)
			return nil
		},
	}
	cmd.Flags().StringVar(&minConfidence, "min-confidence", "", "drop bindings weaker than: high|medium|low")
	cmd.Flags().StringVar(&accuracy, "accuracy", "",
		"override the accuracy mode for this run: fast|auto|exact (fast skips the Go type check)")
	cmd.Flags().BoolVar(&allowBuild, "allow-repo-build", false,
		"let the compiler pass run indexers that execute code the repository chooses: Java without Docker (Maven, Gradle), C/C++ (the compilers named in compile_commands.json) and Rust (cargo build scripts and macros)")
	cmd.Flags().BoolVar(&noSCIP, "no-scip", false, "do not import an index.scip found in the project root or .graphlume/")
	cmd.Flags().BoolVar(&withLSP, "lsp", false, "run configured language servers (LSP) to resolve ambiguous references")
	cmd.Flags().BoolVar(&reResolve, "re-resolve", false, "reset non-compiler bindings and re-resolve all references")
	cmd.Flags().DurationVar(&resolveTimeout, "timeout", 0, "bound on indexer subprocess runtime (default 10m; e.g. 15m)")
	return cmd
}

// scipAutoOutcome records what SCIP auto-discovery did, so resolve can say so
// in both its text and JSON output instead of only in the log.
type scipAutoOutcome struct {
	Path     string
	Tool     string
	Upgraded int
	Promoted int
	Err      error
}

func (o scipAutoOutcome) found() bool { return o.Path != "" }

func (o scipAutoOutcome) json() map[string]any {
	if !o.found() {
		return map[string]any{"found": false}
	}
	m := map[string]any{"found": true, "path": o.Path}
	if o.Err != nil {
		m["error"] = o.Err.Error()
		return m
	}
	m["tool"], m["edges_upgraded"], m["calls_promoted"] = o.Tool, o.Upgraded, o.Promoted
	return m
}

func (o scipAutoOutcome) render(out io.Writer) {
	switch {
	case !o.found():
		return
	case o.Err != nil:
		fmt.Fprintf(out, "  SCIP:        %s found but NOT imported: %v\n", o.Path, o.Err)
		fmt.Fprintf(out, "    edges were not upgraded to exact; fix the index or run 'graphlume scip import %s'\n", o.Path)
	default:
		fmt.Fprintf(out, "  SCIP:        imported %s (%s): %d edge(s) upgraded to exact, %d call(s) promoted\n",
			o.Path, o.Tool, o.Upgraded, o.Promoted)
	}
}

// autoImportSCIP imports the first non-empty index.scip it finds under
// repoRoot. A failed import is returned in the outcome, never swallowed, and
// does not fail resolve: the heuristic graph it just built is still valid.
func autoImportSCIP(ctx context.Context, g *Global, reg *registry.Registry, runID int64, repoRoot string) scipAutoOutcome {
	for _, candidate := range []string{
		filepath.Join(repoRoot, "index.scip"),
		filepath.Join(repoRoot, ".graphlume", "index.scip"),
	} {
		fi, err := os.Stat(candidate)
		if err != nil || fi.IsDir() || fi.Size() == 0 {
			continue
		}
		g.Log().Info("auto-discovered SCIP index; importing compiler ground truth", "path", candidate)
		imp := scipimport.NewImporter(reg, runID, scip.Options{
			RepoRoot: repoRoot,
			MaxBytes: scip.DefaultMaxBytes,
		})
		res, err := imp.Import(ctx, candidate)
		if err != nil {
			g.Log().Warn("scip auto-import failed", "path", candidate, "error", err)
			return scipAutoOutcome{Path: candidate, Err: fmt.Errorf("import %s: %w", candidate, err)}
		}
		g.Log().Info("scip auto-import complete",
			"tool", res.Ingest.Tool.String(),
			"edges_upgraded", res.Edges.Upgraded,
			"calls_promoted", res.Promote.Promoted)
		return scipAutoOutcome{
			Path: candidate, Tool: res.Ingest.Tool.String(),
			Upgraded: res.Edges.Upgraded, Promoted: res.Promote.Promoted,
		}
	}
	return scipAutoOutcome{}
}

func renderLSPBridge(out io.Writer, outcome lsp.BridgeOutcome) {
	if len(outcome.Steps) == 0 {
		return
	}
	var ranSteps []lsp.BridgeStep
	for _, s := range outcome.Steps {
		if s.Ran {
			ranSteps = append(ranSteps, s)
		}
	}
	if len(ranSteps) == 0 {
		return
	}
	fmt.Fprintf(out, "  LSP Bridge:  %d server(s) active\n", len(ranSteps))
	for _, s := range ranSteps {
		if s.Err != nil {
			fmt.Fprintf(out, "    %-12s %s: failed: %v\n", s.Language+":", s.Server, s.Err)
		} else {
			fmt.Fprintf(out, "    %-12s %s: %d queried, %d resolved (%d upgraded to exact, %d new)\n",
				s.Language+":", s.Server, s.Queried, s.Resolved, s.EdgesUpgraded, s.EdgesInserted)
		}
	}
}

func lspBridgeJSON(outcome lsp.BridgeOutcome) any {
	steps := make([]map[string]any, len(outcome.Steps))
	for i, s := range outcome.Steps {
		steps[i] = map[string]any{
			"language":       s.Language,
			"server":         s.Server,
			"ran":            s.Ran,
			"queried":        s.Queried,
			"resolved":       s.Resolved,
			"edges_upgraded": s.EdgesUpgraded,
			"edges_inserted": s.EdgesInserted,
			"duration_ms":    s.Duration.Milliseconds(),
			"skipped_reason": s.SkippedReason,
		}
	}
	return map[string]any{
		"steps":          steps,
		"total_upgraded": outcome.TotalUpgraded(),
		"total_inserted": outcome.TotalInserted(),
		"note":           outcome.Note,
	}
}
