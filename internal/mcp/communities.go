package mcp

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/sutrakam/graphlume/internal/capability/communities"
	"github.com/sutrakam/graphlume/pkg/model"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// This file holds communities, a `full`-profile tool: the repository's
// files grouped by how they refer to each other.
//
// Like the other whole-repository reports it adds no way of knowing anything.
// It is a partition of the edges every other tool reads, so it inherits their
// obligation in a sharper form: a missing edge does not shorten this answer,
// it changes which group a file is in. The capability's notes say how many
// references were left unbound, and they are passed through whole.

type communitiesArgs struct {
	Path          string `json:"path,omitempty" jsonschema:"Only group files under this path prefix, e.g. 'internal/billing/'. Relationships that leave the prefix are counted and not followed."`
	Limit         int    `json:"limit,omitempty" jsonschema:"Most communities to return, largest first. Default 8."`
	Members       int    `json:"members,omitempty" jsonschema:"Most files to list for each community, most referenced first. Default 5."`
	MinConfidence string `json:"min_confidence,omitempty" jsonschema:"Weakest relationship that links two files: exact, high, medium or low. Default medium, because one wrong low-confidence binding can merge unrelated groups."`
}

const (
	// defaultCommunityItems and defaultCommunityMembers are what the tool
	// returns unless asked. Each community is one row and each listed member
	// another, so eight of five is forty-eight items, inside the response's
	// item bound.
	defaultCommunityItems   = 8
	defaultCommunityMembers = 5
)

// registerCommunities adds communities. It reads only the registry, so
// like hotspots it registers whether or not a graph is present.
func (s *Server) registerCommunities(allowed map[string]bool) {
	addTool(s, allowed, &sdk.Tool{
		Name:        "communities",
		Description: toolDescriptions()["communities"],
	}, s.findCommunities)
}

func (s *Server) findCommunities(ctx context.Context, req *sdk.CallToolRequest, args communitiesArgs) (*sdk.CallToolResult, *Response, error) {
	if args.Limit < 0 || args.Members < 0 {
		return failWith("invalid_argument", "Pass limit and members as positive numbers, or omit them.",
			"limit and members must not be negative")
	}
	var floor model.Confidence
	if strings.TrimSpace(args.MinConfidence) != "" {
		var err error
		if floor, err = parseMinConfidence(args.MinConfidence); err != nil {
			return failFrom(err, "%v", err)
		}
	}
	runID, status, err := s.currentRun(ctx)
	if err != nil {
		return s.noRun(ctx, req, err)
	}

	limit := args.Limit
	if limit == 0 {
		limit = defaultCommunityItems
	}
	limit = min(limit, s.limits.MaxItems)
	asked := args.Members
	if asked == 0 {
		asked = defaultCommunityMembers
	}
	asked = min(asked, s.limits.MaxItems)

	rep, err := communities.Detect(ctx, s.deps.Registry, runID, communities.Options{
		Limit: limit, Members: asked, PathPrefix: strings.TrimSpace(args.Path), MinConfidence: floor,
	})
	if err != nil {
		return failFrom(err, "community detection: %v", err)
	}

	// Two bounds share one budget: a community is one item and each listed
	// member another. The communities are kept and the members cut to what is
	// left, because a caller who asked for thirty communities wants thirty
	// rows more than it wants any one of them in detail. The cut is made
	// against the communities actually returned, so asking for thirty of a
	// repository that has six costs those six nothing.
	members := asked
	if n := len(rep.Communities); n > 0 {
		members = min(asked, s.limits.MaxItems/n-1)
	}

	resp := &Response{
		IndexRevision: runID,
		IndexStatus:   status,
		Summary:       communitiesSummary(rep),
		Notes:         rep.Notes,
	}
	cut := false
	for _, c := range rep.Communities {
		if len(c.Members) > members {
			c.MembersOmitted += len(c.Members) - members
			c.Members = c.Members[:members]
			cut = true
		}
		resp.Items = append(resp.Items, Item{Kind: "community", Name: c.Label, Path: c.ID, Why: communityWhy(c)})
		for _, m := range c.Members {
			// The count leads and the id trails: the reason is bounded in
			// bytes, and a long id cut short costs nothing, since the row
			// sits directly under its community.
			resp.Items = append(resp.Items, Item{
				Kind: "member", Name: path.Base(m.Path), Path: m.Path,
				Why: fmt.Sprintf("%d relationship(s) from the other members end here; member of %s",
					m.ReferencedWithin, c.ID),
			})
		}
	}
	// The most central files lead the next rows: PageRank over the file graph
	// the partition used, so a file every other module goes through ranks
	// above a helper with the same number of direct dependents.
	for i, c := range rep.Central {
		if i >= 5 {
			break
		}
		resp.Items = append(resp.Items, Item{
			Kind: "central_file", Name: path.Base(c.Path), Path: c.Path,
			Why: fmt.Sprintf("PageRank %.4f of the file graph (rank %d)", c.Score, i+1),
		})
	}
	if len(rep.Communities) > 0 {
		resp.Notes = append(resp.Notes,
			"The path on a community row is its id: its first member in path order, a real file, and "+
				"the same on every call over the same index. The member rows under it are the files "+
				"the rest of the community refers to most.")
	}
	if cut {
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"At most %d member(s) are listed for each community, not the %d asked for, so that %d "+
				"communities fit in one answer. Lower limit, or pass a path, to see more of each.",
			members, asked, len(rep.Communities)))
	}
	if rep.Truncated {
		resp.markTruncated(rep.TruncatedReason)
	}
	return s.reply(ctx, req, resp)
}

// communitiesSummary is the one-line answer.
func communitiesSummary(rep *communities.Report) string {
	switch {
	case rep.Truncated:
		return "No communities are reported: the file graph is too large to partition in one request."
	case rep.Total == 0:
		return "No communities found. This is not proof the files are unrelated; see the notes."
	}
	found := fmt.Sprintf("%d communities of files found, all shown", rep.Total)
	switch {
	case rep.Total == 1:
		found = "1 community of files found"
	case rep.Omitted > 0:
		found = fmt.Sprintf("%d communities of files found, showing the %d largest", rep.Total, len(rep.Communities))
	}
	return fmt.Sprintf("%s. %d of %d indexed file(s) are in one; the partition is computed from %d bound "+
		"relationship(s) between files.", found, rep.Grouped, rep.Files, rep.Relationships)
}

// communityWhy is the one-line account of a community: its size, where it is,
// where its name came from and how well it holds together.
func communityWhy(c communities.Community) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d files", c.Files)
	if len(c.Directories) > 0 {
		d := c.Directories[0]
		dir := d.Path
		if dir == "." {
			dir = "the repository root"
		}
		switch rest := len(c.Directories) - 1 + c.DirectoriesOmitted; rest {
		case 0:
			fmt.Fprintf(&b, ", all in %s", dir)
		case 1:
			fmt.Fprintf(&b, ", %d in %s and the rest in 1 other directory", d.Files, dir)
		default:
			fmt.Fprintf(&b, ", %d in %s and the rest in %d other directories", d.Files, dir, rest)
		}
	}
	switch c.LabelMethod {
	case communities.MethodSharedPath:
		b.WriteString("; named for the directory all share")
	case communities.MethodTopMember:
		b.WriteString("; named for its most-referenced file")
	}
	fmt.Fprintf(&b, "; %d relationship(s) inside, %d crossing", c.Internal, c.External)
	if c.MembersOmitted > 0 {
		fmt.Fprintf(&b, "; %d member(s) not listed", c.MembersOmitted)
	}
	return b.String()
}
