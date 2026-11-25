package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sutrakam/graphlume/internal/capability/communities"
	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/registry"
	"github.com/sutrakam/graphlume/pkg/model"
)

// seedGroups builds `groups` directories of `size` files each. Every file
// defines one function, and the files of a directory call each other in a
// ring, so each directory is a community. The first file of each directory
// also calls the first file of the next at `bridge` confidence, which is what
// a wrong binding between two unrelated groups looks like.
func seedGroups(groups, size int, bridge model.Confidence) func(context.Context, *registry.Registry, int64) {
	return seedGroupsUnder("", groups, size, bridge)
}

// seedGroupsUnder is seedGroups with every path below root.
func seedGroupsUnder(root string, groups, size int, bridge model.Confidence) func(context.Context, *registry.Registry, int64) {
	return func(ctx context.Context, reg *registry.Registry, runID int64) {
		w, err := reg.NewWriter(ctx, runID)
		if err != nil {
			panic(err)
		}
		ids := make([][]int64, groups)
		paths := make([][]string, groups)
		for g := 0; g < groups; g++ {
			for f := 0; f < size; f++ {
				p := fmt.Sprintf("%sg%02d/f%d.go", root, g, f)
				file := model.File{Path: p, Language: "go", ContentHash: p, ParseStatus: "ok", SizeBytes: 100}
				fid, _ := w.AddFile(ctx, &file)
				s := model.Symbol{
					FileID: fid, Kind: model.KindFunction, Name: fmt.Sprintf("fn%d_%d", g, f), FQN: p + "#fn",
					Range:      model.Range{Start: model.Position{Line: 1}, End: model.Position{Line: 5}},
					Confidence: model.ConfidenceHigh, Source: model.SourceSourceCode,
				}
				id, _ := w.AddSymbol(ctx, &s)
				ids[g] = append(ids[g], id)
				paths[g] = append(paths[g], p)
			}
		}
		_ = w.Flush(ctx)
		_ = w.Close()

		edge := func(src, dst int64, file string, line int, conf model.Confidence) model.Edge {
			return model.Edge{SrcID: src, DstID: dst, Kind: model.EdgeCalls, Confidence: conf,
				Evidence: model.Evidence{FilePath: file, Range: model.Range{Start: model.Position{Line: line}},
					Source: model.SourceSourceCode}}
		}
		var edges []model.Edge
		for g := 0; g < groups; g++ {
			for f := 0; f < size; f++ {
				next := (f + 1) % size
				// Two calls per neighbour, so a ring outweighs a single bridge.
				edges = append(edges,
					edge(ids[g][f], ids[g][next], paths[g][f], 2, model.ConfidenceHigh),
					edge(ids[g][f], ids[g][next], paths[g][f], 3, model.ConfidenceHigh))
			}
			if g+1 < groups {
				edges = append(edges, edge(ids[g][0], ids[g+1][0], paths[g][0], 4, bridge))
			}
		}
		if _, err := reg.AddResolvedEdges(ctx, runID, edges); err != nil {
			panic(err)
		}
	}
}

func withLimits(l Limits) fullOption { return func(d *Deps) { d.Limits = l } }

func TestFindCommunitiesGroupsFilesAndListsTheirMembers(t *testing.T) {
	fs := newFullServer(t, seedGroups(2, 3, model.ConfidenceHigh))
	res := callTool(t, fs.sess, "communities", map[string]any{})
	text := resultText(res)
	if res.IsError {
		t.Fatalf("communities failed:\n%s", text)
	}
	for _, want := range []string{
		"2 communities of files found, all shown",
		// The community row: label, then its id, which is its first member.
		"- community g00  g00/f0.go  (3 files, all in g00; named for the directory all share; 6 relationship(s) inside, 1 crossing)",
		"- community g01  g01/f0.go",
		"- member f1.go  g00/f1.go  (2 relationship(s) from the other members end here; member of g00/f0.go)",
		"The path on a community row is its id",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "confidence=") {
		t.Errorf("a community is not a relationship and must not carry a confidence tier:\n%s", text)
	}
}

// The obligation every tool here carries: the answer says what the index could
// not see, in the text a model reads, not only in a structured field.
func TestFindCommunitiesStatesWhatTheIndexCouldNotSee(t *testing.T) {
	fs := newFullServer(t, seedGroups(2, 3, model.ConfidenceHigh))
	text := resultText(callTool(t, fs.sess, "communities", map[string]any{}))
	for _, want := range []string{
		"recorded no references at all", // this fixture has edges and no reference rows
		"not a declared architecture",
		"A reference it could not bind is a missing link",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
}

// The two calls must agree byte for byte: same ids, same labels, same order.
func TestFindCommunitiesIsTheSameOnEveryCall(t *testing.T) {
	fs := newFullServer(t, seedGroups(4, 3, model.ConfidenceHigh))
	first := resultText(callTool(t, fs.sess, "communities", map[string]any{}))
	for i := 0; i < 5; i++ {
		if again := resultText(callTool(t, fs.sess, "communities", map[string]any{})); again != first {
			t.Fatalf("call %d answered differently:\n%s\n---\n%s", i+2, again, first)
		}
	}
}

// A low-confidence bridge is the wrong binding that would chain every group
// into one. It is left out by default, counted, and admitted on request.
func TestFindCommunitiesLeavesLowConfidenceLinksOutUnlessAsked(t *testing.T) {
	fs := newFullServer(t, seedGroups(2, 3, model.ConfidenceLow))

	text := resultText(callTool(t, fs.sess, "communities", map[string]any{}))
	if !strings.Contains(text, "1 relationship(s) between these files are below medium confidence and were left out") {
		t.Errorf("the relationship left out is not counted:\n%s", text)
	}
	if !strings.Contains(text, "6 relationship(s) inside, 0 crossing") {
		t.Errorf("the low-confidence bridge was counted as a crossing relationship:\n%s", text)
	}

	withLow := resultText(callTool(t, fs.sess, "communities", map[string]any{"min_confidence": "low"}))
	if !strings.Contains(withLow, "6 relationship(s) inside, 1 crossing") || strings.Contains(withLow, "were left out") {
		t.Errorf("min_confidence=low should count the bridge and leave nothing out:\n%s", withLow)
	}
}

func TestFindCommunitiesHonoursPathAndCountsWhatCrossesIt(t *testing.T) {
	fs := newFullServer(t, seedGroups(2, 3, model.ConfidenceHigh))
	text := resultText(callTool(t, fs.sess, "communities", map[string]any{"path": "g01/"}))
	if strings.Contains(text, "- community g00") || !strings.Contains(text, "- community g01") {
		t.Errorf("only the community under g01/ should be listed:\n%s", text)
	}
	if !strings.Contains(text, `connect a file under "g01/" to one outside it`) {
		t.Errorf("the relationship crossing the prefix is not stated:\n%s", text)
	}
}

// Each cut is stated: communities beyond the limit, members beyond the member
// bound, and members given up so the communities asked for fit one answer.
func TestFindCommunitiesCountsEverythingItCuts(t *testing.T) {
	fs := newFullServer(t, seedGroups(6, 4, model.ConfidenceHigh))

	text := resultText(callTool(t, fs.sess, "communities", map[string]any{"limit": 2, "members": 1}))
	for _, want := range []string{
		"6 communities of files found, showing the 2 largest",
		"Showing the 2 largest of 6 communities. The 4 not shown hold 16 file(s)",
		"3 member(s) not listed",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	if n := strings.Count(text, "\n- community "); n != 2 {
		t.Errorf("listed %d communities, want 2:\n%s", n, text)
	}
	if n := strings.Count(text, "\n- member "); n != 2 {
		t.Errorf("listed %d members, want 1 for each of 2 communities:\n%s", n, text)
	}

	// Twelve items of budget, six communities: one member each, not the four
	// asked for, and the answer says so.
	small := DefaultLimits()
	small.MaxItems = 12
	tight := newFullServer(t, seedGroups(6, 4, model.ConfidenceHigh), withLimits(small))
	text = resultText(callTool(t, tight.sess, "communities", map[string]any{"limit": 6, "members": 4}))
	if n := strings.Count(text, "\n- community "); n != 6 {
		t.Errorf("listed %d communities, want all 6:\n%s", n, text)
	}
	if n := strings.Count(text, "\n- member "); n != 6 {
		t.Errorf("listed %d members, want 1 for each of 6 communities:\n%s", n, text)
	}
	if !strings.Contains(text, "At most 1 member(s) are listed for each community, not the 4 asked for") {
		t.Errorf("the member cut is not stated:\n%s", text)
	}
	if !strings.Contains(text, "3 member(s) not listed") {
		t.Errorf("a community's own count of unlisted members must include the ones cut to fit:\n%s", text)
	}
}

// Deep trees are where an answer made of paths outgrows its budget. The answer
// must stay inside the inline limit, every member row must keep its own path
// and its count, and anything dropped to fit must be said to be dropped.
func TestFindCommunitiesStaysBoundedOnLongPaths(t *testing.T) {
	root := strings.Repeat("deeply/nested/", 13) // 182 bytes before the group directory
	fs := newFullServer(t, seedGroupsUnder(root, 8, 6, model.ConfidenceHigh))
	text := resultText(callTool(t, fs.sess, "communities", map[string]any{}))

	if limit := DefaultLimits().MaxTotalBytes; len(text) > limit {
		t.Errorf("the answer is %d bytes, over the inline limit of %d", len(text), limit)
	}
	if !strings.Contains(text, "- member f1.go  "+root+"g00/f1.go  (2 relationship(s) from the other members end here") {
		t.Errorf("a member row lost its path or its count to the long community id:\n%s", text)
	}
	listed := strings.Count(text, "\n- community ") + strings.Count(text, "\n- member ")
	if listed < 8*(1+defaultCommunityMembers) && !strings.Contains(text, "truncated:") {
		t.Errorf("%d of %d rows are present and nothing says the rest were cut:\n%s",
			listed, 8*(1+defaultCommunityMembers), text)
	}
}

func TestFindCommunitiesWithNothingToGroupIsNotAFailure(t *testing.T) {
	fs := newFullServer(t, nil)
	res := callTool(t, fs.sess, "communities", map[string]any{})
	text := resultText(res)
	if res.IsError {
		t.Fatalf("an index with nothing to group is a state of the world, not a malfunction:\n%s", text)
	}
	if !strings.Contains(text, "No communities found. This is not proof the files are unrelated") {
		t.Errorf("an empty answer must not read as proof:\n%s", text)
	}
	if !strings.Contains(text, "not a declared architecture") {
		t.Errorf("an empty answer must still carry the limits statement:\n%s", text)
	}
}

func TestFindCommunitiesRejectsBadArguments(t *testing.T) {
	fs := newFullServer(t, seedGroups(2, 3, model.ConfidenceHigh))
	for name, args := range map[string]map[string]any{
		"negative limit":     {"limit": -1},
		"negative members":   {"members": -2},
		"unknown confidence": {"min_confidence": "probable"},
	} {
		if res := callTool(t, fs.sess, "communities", args); !res.IsError {
			t.Errorf("%s: a malformed call must be an error, got:\n%s", name, resultText(res))
		}
	}
}

// The tool costs every request of a session its schema, so it is opt-in.
func TestFindCommunitiesIsOnTheFullProfileOnly(t *testing.T) {
	has := func(p config.MCPProfile) bool {
		fs := newFullServer(t, nil, withProfile(p))
		for _, name := range fs.srv.Tools() {
			if name == "communities" {
				return true
			}
		}
		return false
	}
	if !has(config.MCPFull) {
		t.Error("the full profile must expose communities")
	}
	if has(config.MCPStandard) || has(config.MCPMinimal) {
		t.Error("communities must not be on the standard or minimal profile")
	}
}

// The tool reports what the capability reports: one set of defaults per
// surface, one statement of limits for both.
func TestFindCommunitiesCarriesTheCapabilitysLimitsVerbatim(t *testing.T) {
	fs := newFullServer(t, seedGroups(2, 3, model.ConfidenceHigh))
	text := resultText(callTool(t, fs.sess, "communities", map[string]any{}))
	if !strings.Contains(text, "note: "+communities.Limits) {
		t.Errorf("the capability's Limits statement must reach the model unedited:\n%s", text)
	}
}
