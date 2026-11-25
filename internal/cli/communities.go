package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/sutrakam/graphlume/internal/capability/communities"
	"github.com/sutrakam/graphlume/pkg/model"

	"github.com/spf13/cobra"
)

// The terminal form of communities. The capability is one function for
// both; this file only decides how a person reads it.

func newCommunitiesCmd(g *Global) *cobra.Command {
	var (
		pathPrefix    string
		limit         int
		members       int
		minConfidence string
		algorithm     string
	)
	cmd := &cobra.Command{
		Use:   "communities",
		Short: "Group files by how they refer to each other",
		Long: `Groups the repository's files into communities: sets of files whose
relationships run between their members more than they run outwards.

  graphlume communities
  graphlume communities --path internal/ --limit 10
  graphlume communities --members 20
  graphlume communities --json

Use it to see how an unfamiliar repository divides before reading it, or to
find the files that belong with the one you are about to change. Each community
is listed with its most-referenced files first.

A community is named after the directory all its members share, when they share
one, and otherwise after its most-referenced file; the NAMED FOR line says
which. Its ID is its first member path in sorted order: a real file, and the
same on every run over the same index.

INSIDE and CROSSING count the relationships between two members and those with
one end outside. A community with far more crossing than inside is a weak one.

A community describes how the indexed code refers to itself today. It is not a
declared architecture. Only relationships the index bound are counted, so files
that work together can be listed apart, or in no community at all, when what
connects them is a reference the index could not bind. The count of those is
printed with every answer.

Low-confidence relationships are left out unless --min-confidence low is given.
Most are matches on a name alone, and one wrong one can merge unrelated groups;
how many were left out is printed. Files in no community are counted, not
listed. A graph of more than 250,000 file-to-file links is not partitioned at
all: narrow it with --path.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if limit < 1 {
				return errors.New("--limit must be at least 1")
			}
			if members < 0 {
				return errors.New("--members must not be negative")
			}
			s, err := openQuery(ctx, g)
			if err != nil {
				return err
			}
			defer s.close()

			opts := communities.Options{Limit: limit, Members: members, PathPrefix: pathPrefix, Algorithm: algorithm}
			if minConfidence != "" {
				opts.MinConfidence = model.Confidence(minConfidence)
				if !opts.MinConfidence.Valid() {
					return fmt.Errorf("--min-confidence %q: want exact, high, medium or low", minConfidence)
				}
			}
			if members == 0 {
				opts.Members = -1 // zero asks for the counts alone
			}
			rep, err := communities.Detect(ctx, s.reg, s.runID, opts)
			if err != nil {
				return err
			}
			if g.JSON {
				return emitJSON(cmd.OutOrStdout(), rep)
			}
			renderCommunities(cmd.OutOrStdout(), rep)
			return nil
		},
	}
	cmd.Flags().StringVar(&pathPrefix, "path", "", "only group files under this path prefix")
	cmd.Flags().IntVarP(&limit, "limit", "n", communities.DefaultLimit, "maximum communities to list, largest first")
	cmd.Flags().IntVar(&members, "members", communities.DefaultMembers, "maximum files to list for each community (0 lists none)")
	cmd.Flags().StringVar(&minConfidence, "min-confidence", "",
		"weakest relationship that links two files (exact, high, medium, low); default medium")
	cmd.Flags().StringVar(&algorithm, "algorithm", "",
		"partition method: leiden (default) or label_propagation")
	return cmd
}

func renderCommunities(out io.Writer, rep *communities.Report) {
	switch {
	case rep.Truncated:
		fmt.Fprintln(out, "No communities reported: the file graph is too large to partition in one request.")
	case rep.Total == 0:
		fmt.Fprintln(out, "No communities found.")
	case rep.Total == 1:
		fmt.Fprintf(out, "1 community of %d of %d file(s), from %d relationship(s) between files.\n",
			rep.Grouped, rep.Files, rep.Relationships)
	default:
		fmt.Fprintf(out, "%d communities over %d of %d file(s), from %d relationship(s) between files.\n",
			rep.Total, rep.Grouped, rep.Files, rep.Relationships)
	}

	for i, c := range rep.Communities {
		fmt.Fprintf(out, "\n%d. %s  (%d files)\n", i+1, c.Label, c.Files)
		tw := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
		fmt.Fprintf(tw, "   NAMED FOR\t%s\n", labelMethodText(c.LabelMethod))
		fmt.Fprintf(tw, "   ID\t%s\n", c.ID)
		fmt.Fprintf(tw, "   MOSTLY IN\t%s\n", directoriesText(c))
		fmt.Fprintf(tw, "   INSIDE\t%d relationship(s) between members\n", c.Internal)
		fmt.Fprintf(tw, "   CROSSING\t%d relationship(s) with a file outside\n", c.External)
		_ = tw.Flush()
		if len(c.Members) == 0 {
			continue
		}
		fmt.Fprintln(out, "   MEMBERS, most referenced by the others first")
		tw = tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
		for _, m := range c.Members {
			fmt.Fprintf(tw, "     %s\t%d\n", m.Path, m.ReferencedWithin)
		}
		_ = tw.Flush()
		if c.MembersOmitted > 0 {
			fmt.Fprintf(out, "     ... and %d more; raise --members to list them\n", c.MembersOmitted)
		}
	}

	if len(rep.Central) > 0 {
		fmt.Fprintln(out, "\nMOST CENTRAL FILES (PageRank over the same file graph; share of total rank)")
		tw := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
		for _, c := range rep.Central {
			fmt.Fprintf(tw, "   %s\t%.4f\n", c.Path, c.Score)
		}
		_ = tw.Flush()
	}

	for _, n := range rep.Notes {
		fmt.Fprintf(out, "\n%s\n", wrapText(n, 100))
	}
}

// directoriesText lists where a community's files are, fullest directory first.
func directoriesText(c communities.Community) string {
	parts := make([]string, 0, len(c.Directories)+1)
	for _, d := range c.Directories {
		name := d.Path
		if name == "." {
			name = "the repository root"
		}
		parts = append(parts, fmt.Sprintf("%s (%d)", name, d.Files))
	}
	if c.DirectoriesOmitted > 0 {
		parts = append(parts, fmt.Sprintf("and %d more directories", c.DirectoriesOmitted))
	}
	return strings.Join(parts, ", ")
}

// labelMethodText says in words where a community's name came from.
func labelMethodText(method string) string {
	switch method {
	case communities.MethodSharedPath:
		return "the directory every member is under"
	case communities.MethodTopMember:
		return "its most-referenced file (the members share no directory)"
	default:
		return method
	}
}
