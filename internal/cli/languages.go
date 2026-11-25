package cli

// Prints what GraphLume can actually do per language, in the terms a user
// cares about: can it find definitions, can it bind references, can it tell
// two same-named methods apart. The numbers come from the query maps at run
// time, so this output and the README table cannot disagree.

import (
	"fmt"
	"text/tabwriter"

	"github.com/sutrakam/graphlume/internal/parse"

	"github.com/spf13/cobra"
)

type languageReport struct {
	Typed    []string `json:"typed"`
	Resolved []string `json:"resolved"`
	// Parsed is the definitions-only tier: a definition query exists.
	Parsed []string `json:"parsed"`
	// TextOnly lists grammars the binary carries with no definition query.
	// Files in them are indexed for full-text search and yield no symbols; the
	// index records them as skipped with reason text_only. They were counted
	// under Parsed until it was checked what a file in one of them yields.
	TextOnly []string `json:"text_only"`
	// WithDefinitions is how many languages can produce a definition at all.
	WithDefinitions int `json:"with_definitions"`
	// Total is every grammar in the binary, extractable or not.
	Total int `json:"total"`
}

const textOnlyDescription = "no definition query: full-text search only, no symbols"

func buildLanguageReport() languageReport {
	rep := languageReport{
		Typed:    parse.LanguagesInTier(parse.TierTyped),
		Resolved: parse.LanguagesInTier(parse.TierResolved),
		TextOnly: parse.TextOnlyLanguages(),
	}
	for _, l := range parse.LanguagesInTier(parse.TierParsed) {
		if parse.ExtractsDefinitions(l) {
			rep.Parsed = append(rep.Parsed, l)
		}
	}
	rep.WithDefinitions = len(rep.Typed) + len(rep.Resolved) + len(rep.Parsed)
	rep.Total = rep.WithDefinitions + len(rep.TextOnly)
	return rep
}

func newLanguagesCmd(g *Global) *cobra.Command {
	var verbose bool
	cmd := &cobra.Command{
		Use:   "languages",
		Short: "Report what GraphLume can do for each language it parses",
		Long: `Languages fall into three tiers, and a fourth group that is not a tier.

  typed         definitions, imports, and receiver discrimination by arity
                or by class. References here can reach 'high' confidence.
  resolved      definitions and imports. References bind across files.
  parsed        definitions and full-text search only. No cross-file
                relationship is claimed for these.
  text only     the grammar is bundled but there is no definition query, so
                files in these languages are indexed for full-text search and
                yield no symbols.

A tier says what the parser is built to do, not how well it does it. How much
of a language's definitions and calls are actually found was measured by hand
and varies widely within every tier: see BENCHMARKS.md.

The groups are derived from what the parser can do at run time, not from a
list, so this output always matches what the binary can actually do.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rep := buildLanguageReport()

			if g.JSON {
				return emitJSON(cmd.OutOrStdout(), rep)
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintf(w, "TIER\tCOUNT\tWHAT YOU GET\n")
			fmt.Fprintf(w, "typed\t%d\t%s\n", len(rep.Typed), parse.TierTyped.Description())
			fmt.Fprintf(w, "resolved\t%d\t%s\n", len(rep.Resolved), parse.TierResolved.Description())
			fmt.Fprintf(w, "parsed\t%d\t%s\n", len(rep.Parsed), parse.TierParsed.Description())
			fmt.Fprintf(w, "with definitions\t%d\t\n", rep.WithDefinitions)
			fmt.Fprintf(w, "text only\t%d\t%s\n", len(rep.TextOnly), textOnlyDescription)
			fmt.Fprintf(w, "total\t%d\t\n", rep.Total)
			if err := w.Flush(); err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "\ntyped:    %v\n", rep.Typed)
			fmt.Fprintf(cmd.OutOrStdout(), "resolved: %v\n", rep.Resolved)
			if verbose {
				fmt.Fprintf(cmd.OutOrStdout(), "parsed:   %v\n", rep.Parsed)
				fmt.Fprintf(cmd.OutOrStdout(), "text only: %v\n", rep.TextOnly)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "\n(%d parsed-tier and %d text-only languages not listed; pass --verbose)\n",
					len(rep.Parsed), len(rep.TextOnly))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&verbose, "verbose", false, "list the parsed-tier and text-only languages too")
	return cmd
}
