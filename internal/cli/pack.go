package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sutrakam/graphlume/internal/export"
)

func newPackCmd(g *Global) *cobra.Command {
	var formatStr string
	var includeTree bool
	var outPath string
	var prefix string
	var noRedact bool
	var maxFileKB int64

	cmd := &cobra.Command{
		Use:   "pack",
		Short: "Bundle codebase files into an AI-ready prompt pack with directory tree and secret protection",
		Long: `Assembles repository source files into an AI-ready XML, Markdown, or JSON bundle.
Includes an ASCII directory tree, token counts, and automated secret redaction.

  graphlume pack
  graphlume pack --format markdown --out prompt.md
  graphlume pack --prefix internal/api --format xml
  graphlume pack --format json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			s, err := openQuery(ctx, g)
			if err != nil {
				return err
			}
			defer s.close()

			var fmtChoice export.PackFormat
			switch strings.ToLower(strings.TrimSpace(formatStr)) {
			case "md", "markdown":
				fmtChoice = export.PackFormatMarkdown
			case "json":
				fmtChoice = export.PackFormatJSON
			default:
				fmtChoice = export.PackFormatXML
			}

			if maxFileKB <= 0 {
				maxFileKB = 500
			}

			opts := export.PackOptions{
				Format:       fmtChoice,
				IncludeTree:  includeTree,
				MaxFileBytes: maxFileKB * 1024,
				PathPrefix:   prefix,
				Redact:       !noRedact,
			}

			if g.JSON && fmtChoice != export.PackFormatJSON {
				res, err := export.GeneratePack(ctx, s.reg, s.runID, opts)
				if err != nil {
					return fmt.Errorf("pack: %w", err)
				}
				return emitJSON(cmd.OutOrStdout(), res)
			}

			if outPath != "" {
				f, err := os.Create(outPath) //nolint:gosec // G304: the output path is the user-supplied --out flag
				if err != nil {
					return fmt.Errorf("pack: create %s: %w", outPath, err)
				}
				defer func() { _ = f.Close() }()

				res, err := export.StreamPack(ctx, s.reg, s.runID, opts, f)
				if err != nil {
					return fmt.Errorf("pack: stream to %s: %w", outPath, err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Packed %d files (~%d tokens) to %s\n",
					res.FileCount, res.EstimatedTokens, outPath)
				if res.RedactedCount > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "Note: redacted %d credential(s) from pack\n", res.RedactedCount)
				}
				return nil
			}

			_, err = export.StreamPack(ctx, s.reg, s.runID, opts, cmd.OutOrStdout())
			if err != nil {
				return fmt.Errorf("pack: stream: %w", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&formatStr, "format", "xml", "pack format: xml, markdown, or json")
	cmd.Flags().BoolVar(&includeTree, "tree", true, "include directory structure tree")
	cmd.Flags().StringVar(&outPath, "out", "", "write bundle to file instead of stdout")
	cmd.Flags().StringVar(&prefix, "prefix", "", "filter to files under a specific path prefix")
	cmd.Flags().BoolVar(&noRedact, "no-redact", false, "disable automated secret/credential redaction")
	cmd.Flags().Int64Var(&maxFileKB, "max-file-kb", 500, "maximum size per file in KB")

	return cmd
}
