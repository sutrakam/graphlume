package cli

import (
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

func newExploreCmd(g *Global) *cobra.Command {
	var path string
	var line int
	var limit int
	var minConfidence string
	var diagram string

	cmd := &cobra.Command{
		Use:   "explore <name>",
		Short: "Deep-dive into a symbol: definition, callers, callees, and routes in one turn",
		Long: `Single-turn deep exploration of a symbol.

Returns the verbatim source definition with line numbers, upstream callers
(tagged with exact vs heuristic confidence), downstream callees, and any
associated HTTP routes.

  graphlume explore UserService
  graphlume explore authenticate --path backend/services/user.go
  graphlume explore Login --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := strings.TrimSpace(args[0])
			if name == "" {
				return errors.New("name must not be empty")
			}

			var minConf model.Confidence
			if minConfidence != "" {
				minConf = model.Confidence(minConfidence)
				if !minConf.Valid() {
					return fmt.Errorf("--min-confidence %q: want exact, high, medium or low", minConfidence)
				}
			}

			if limit <= 0 {
				limit = 15
			}
			if err := checkDiagramFormat(diagram); err != nil {
				return err
			}

			s, err := openQuery(ctx, g)
			if err != nil {
				return err
			}
			defer s.close()

			target, all, err := resolveTarget(ctx, s, name, path)
			if err != nil {
				return err
			}

			// Read source excerpt
			lines := target.EndLine - target.StartLine + 1
			if lines < 1 {
				lines = 1
			}
			if lines > 40 {
				lines = 40
			}
			excerpt, _ := s.reg.FileExcerpt(ctx, s.runID, target.FilePath, target.StartLine, lines)

			// Upstream callers
			callerFilter := registry.EdgeFilter{
				MinConfidence: minConf,
				Kinds:         []string{string(model.EdgeCalls)},
			}
			callers, _ := s.reg.IncomingEdges(ctx, s.runID, target.ID, limit, callerFilter)

			// Downstream callees
			callees, _ := s.reg.OutgoingEdges(ctx, s.runID, target.ID, limit, callerFilter)

			if diagram != "" {
				var edges []diagramEdge
				for _, c := range callers {
					edges = append(edges, diagramEdge{From: c.SrcName, To: target.Name, Kind: c.Kind, Confidence: c.Confidence})
				}
				for _, c := range callees {
					edges = append(edges, diagramEdge{From: target.Name, To: c.DstName, Kind: c.Kind, Confidence: c.Confidence})
				}
				return writeDiagram(cmd.OutOrStdout(), diagram, "explore "+target.Name, edges)
			}

			// Check routes
			var matchedRoute string
			if endpoints, rerr := s.reg.EndpointReport(ctx, s.runID); rerr == nil {
				for _, ep := range endpoints {
					if ep.FilePath == target.FilePath && (ep.Handler == target.Name || ep.HandlerID == target.ID || (ep.Line >= target.StartLine && ep.Line <= target.EndLine)) {
						matchedRoute = ep.Signature
						break
					}
				}
			}

			if g.JSON {
				out := map[string]any{
					"symbol": map[string]any{
						"id":         target.ID,
						"name":       target.Name,
						"fqn":        target.FQN,
						"kind":       target.Kind,
						"path":       target.FilePath,
						"start_line": target.StartLine,
						"end_line":   target.EndLine,
						"confidence": target.Confidence,
						"excerpt":    excerpt,
					},
					"callers": callers,
					"callees": callees,
				}
				if matchedRoute != "" {
					out["route"] = matchedRoute
				}
				return emitJSON(cmd.OutOrStdout(), out)
			}

			w := cmd.OutOrStdout()
			announceAmbiguity(w, all, name, target)

			fmt.Fprintf(w, "%s (%s) at %s:%d-%d\n", target.Name, target.Kind, target.FilePath, target.StartLine, target.EndLine)
			fmt.Fprintf(w, "Confidence: %s\n", target.Confidence)
			if matchedRoute != "" {
				fmt.Fprintf(w, "HTTP Route: %s\n", matchedRoute)
			}
			fmt.Fprintln(w)

			if excerpt != "" {
				fmt.Fprintln(w, "--- Source Excerpt ---")
				split := strings.Split(excerpt, "\n")
				for idx, l := range split {
					fmt.Fprintf(w, "%5d | %s\n", target.StartLine+idx, l)
				}
				fmt.Fprintln(w)
			}

			fmt.Fprintf(w, "--- Upstream Callers (%d) ---\n", len(callers))
			if len(callers) == 0 {
				fmt.Fprintln(w, "  (no callers recorded in index; empty result may be ambiguous)")
			} else {
				tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
				for _, c := range callers {
					callerName := c.SrcName
					if c.SrcQualified != "" {
						callerName = c.SrcQualified
					}
					fmt.Fprintf(tw, "  [%s]\t%s\t%s:%d\n", c.Confidence, callerName, c.EvidenceFile, c.EvidenceLine)
				}
				_ = tw.Flush()
			}
			fmt.Fprintln(w)

			fmt.Fprintf(w, "--- Downstream Callees (%d) ---\n", len(callees))
			if len(callees) == 0 {
				fmt.Fprintln(w, "  (no outgoing calls recorded)")
			} else {
				tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
				for _, c := range callees {
					fmt.Fprintf(tw, "  [%s]\t%s\t%s:%d\n", c.Confidence, c.DstName, c.EvidenceFile, c.EvidenceLine)
				}
				_ = tw.Flush()
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&path, "path", "", "choose a definition by file path")
	cmd.Flags().IntVar(&line, "line", 0, "choose a definition by line number")
	cmd.Flags().IntVar(&limit, "limit", 15, "maximum callers and callees to list")
	cmd.Flags().StringVar(&minConfidence, "min-confidence", "", "lowest confidence to report: exact, high, medium, low")
	cmd.Flags().StringVar(&diagram, "diagram", "", "print the callers and callees as a diagram instead: mermaid, dot or html")

	return cmd
}
