package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/sutrakam/graphlume/internal/capability"
	"github.com/sutrakam/graphlume/internal/capability/impact"
	"github.com/sutrakam/graphlume/internal/capability/planchange"
	"github.com/sutrakam/graphlume/internal/graph"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

func newPlanChangeCmd(g *Global) *cobra.Command {
	var minConfStr string

	cmd := &cobra.Command{
		Use:   "plan-change <symbol>",
		Short: "Plan refactoring steps, affected callers, and test verification gates",
		Long: `Generates a deterministic change blueprint for refactoring a symbol:
  - Resolves target definition and direct callers
  - Assesses risk score and blast radius
  - Discovers covering tests that must pass before and after changes
  - Produces an ordered, step-by-step execution plan with pre/post-conditions

Examples:
  graphlume plan-change updateStatus
  graphlume plan-change service.TransactionService --min-confidence high
  graphlume plan-change auth.Login --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			targetSymbol := args[0]

			db, err := openReadOnly(ctx, g.DBPath())
			if err != nil {
				return err
			}
			defer func() { _ = db.Close() }()

			reg := registry.New(db)
			currentRun, err := reg.CurrentRun(ctx)
			if err != nil {
				return fmt.Errorf("read current index run: %w", err)
			}
			if currentRun == nil {
				return fmt.Errorf("index is empty: run 'graphlume index' first")
			}

			gr := graph.New(db)
			caps := capability.NewDeps(reg, gr)
			planner := planchange.NewPlanner(caps)

			minConf := model.ConfidenceMedium
			if minConfStr != "" {
				minConf = model.Confidence(minConfStr)
				if !minConf.Valid() {
					return fmt.Errorf("unknown confidence %q: use exact, high, medium, or low", minConfStr)
				}
			}

			plan, err := planner.Plan(ctx, currentRun.ID, targetSymbol, planchange.Options{
				MinConfidence: minConf,
			})
			if err != nil {
				return fmt.Errorf("plan change: %w", err)
			}

			if g.JSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				// The two fields a Plan keeps out of its own JSON, so the command
				// that prints the plan does not drop what the agent tool says.
				return enc.Encode(struct {
					*planchange.Plan
					RiskPartial string              `json:"risk_partial,omitempty"`
					ValueUse    capability.ValueUse `json:"value_use"`
				}{plan, plan.RiskPartial, plan.ValueUse})
			}

			// Human readable output
			fmt.Printf("GraphLume Change Plan for %s (%s)\n", plan.Target.Name, plan.Target.Kind)
			fmt.Printf("Location:      %s:%d\n", plan.Target.FilePath, plan.Target.Line)
			if plan.ValueUse.Any() {
				// The same doubt the agent tool leads with: a function passed as a
				// value is reached without a call edge.
				fmt.Printf("INCOMPLETE:    %s.\n", plan.ValueUse.Lead())
			}
			switch {
			case plan.RiskLevel == impact.LevelUnknown:
				fmt.Printf("Risk Level:    UNKNOWN: %s.\n", strings.TrimSuffix(plan.RiskUndetermined, "."))
				fmt.Println("               The callers below are the ones the index could bind, not all of them.")
			case plan.RiskUndetermined != "":
				why := "counts only the callers that were bound"
				if plan.RiskPartial != "" {
					why += "; " + plan.RiskPartial
				}
				fmt.Printf("Risk Level:    %s or higher (Score: %d/100 %s)\n",
					strings.ToUpper(plan.RiskLevel), plan.RiskScore, why)
			case plan.RiskPartial != "":
				fmt.Printf("Risk Level:    %s or higher (Score: %d/100; %s)\n",
					strings.ToUpper(plan.RiskLevel), plan.RiskScore, plan.RiskPartial)
			default:
				fmt.Printf("Risk Level:    %s (Score: %d/100)\n", strings.ToUpper(plan.RiskLevel), plan.RiskScore)
			}
			fmt.Printf("Blast Radius:  %d direct caller(s), %d total reached\n\n", plan.DirectCallers, plan.TotalCallers)

			fmt.Println("Preconditions:")
			for _, pre := range plan.Preconditions {
				fmt.Printf("  [ ] %s\n", pre)
			}
			fmt.Println()

			fmt.Println("Execution Sequence:")
			for _, step := range plan.Steps {
				loc := ""
				if step.Path != "" {
					loc = fmt.Sprintf(" [%s]", step.Path)
				}
				fmt.Printf("  Step %d: %s%s\n", step.Order, step.Description, loc)
				if step.Command != "" {
					fmt.Printf("          Run: `%s`\n", step.Command)
				}
			}
			fmt.Println()

			fmt.Println("Postconditions:")
			for _, post := range plan.Postconditions {
				fmt.Printf("  [ ] %s\n", post)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&minConfStr, "min-confidence", "medium", "Lowest confidence edge to walk (exact, high, medium, low)")
	return cmd
}
