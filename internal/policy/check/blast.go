package check

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/sutrakam/graphlume/internal/capability/impact"
	"github.com/sutrakam/graphlume/internal/policy"
	"github.com/sutrakam/graphlume/pkg/model"
)

// maxEvidenceRows bounds the impacted symbols listed under a blast-radius
// violation. The count is the claim; the rows are a sample so a reviewer can
// see what kind of thing is at risk without a 2,000-line report.
const maxEvidenceRows = 10

// blastRadius fails when the diff's impacted set is larger than the bounds.
//
// "Impacted" is impact.DiffReport.Impacted: the symbols that depend on a
// changed symbol through recorded references, each once. Only those whose
// path confidence is at or above the floor are counted, so a bound of 200 does
// not trip over 500 symbols reached through name-match guesses. The report was
// already computed with that floor, and the filter here repeats it so the
// rule is correct for a report computed at a lower one (the CI command reuses
// the report it already has when the floors agree).
//
// Counts are lower bounds when the traversal was truncated, and the message
// says so: a bound that is exceeded stays exceeded however much more there
// would have been, so a truncated report can still prove a failure; it just
// cannot prove a pass, which is noted separately.
func blastRadius(r policy.MaxBlastRadius, d impact.DiffReport, floor model.Confidence) []Violation {
	var counted []impact.Impacted
	files := map[string]bool{}
	for _, im := range d.Impacted {
		if !im.Confidence.AtLeast(floor) {
			continue
		}
		counted = append(counted, im)
		files[im.FilePath] = true
	}
	sort.SliceStable(counted, func(i, j int) bool {
		if counted[i].Depth != counted[j].Depth {
			return counted[i].Depth < counted[j].Depth
		}
		return counted[i].ID < counted[j].ID
	})

	var evidence []Evidence
	for _, im := range counted {
		if len(evidence) == maxEvidenceRows {
			break
		}
		e := Evidence{Detail: fmt.Sprintf("%s %s, depth %d, %s confidence", im.Kind, im.Name, im.Depth, im.Confidence)}
		e.Path = repoPath(d.IndexRootPrefix, im.FilePath)
		if len(im.Evidence) > 0 {
			e.Path = repoPath(d.IndexRootPrefix, im.Evidence[0].FilePath)
			e.Line = im.Evidence[0].Range.Start.Line
		}
		evidence = append(evidence, e)
	}

	atLeast := ""
	if d.Truncated {
		atLeast = "at least "
	}
	changed := changedRepoSpace(d)
	anchor := ""
	if len(changed) > 0 {
		anchor = changed[0]
	}
	base := id()
	sev := r.Severity.Or()
	var out []Violation
	mk := func(what string, n, bound int) {
		out = append(out, Violation{
			Rule: policy.RuleMaxBlastRadius, RuleID: base, Severity: sev,
			Message: fmt.Sprintf("the change puts %s%d %s at risk, over the bound of %d (counting dependants at %s confidence or better, %d changed file(s))",
				atLeast, n, what, bound, floor, len(changed)),
			Path: anchor, Confidence: string(floor), Evidence: evidence,
		})
	}
	if r.Symbols != nil && len(counted) > *r.Symbols {
		mk("symbols", len(counted), *r.Symbols)
	}
	if r.Files != nil && len(files) > *r.Files {
		mk("files", len(files), *r.Files)
	}
	return out
}

func id() string { return policy.RuleID(policy.RuleMaxBlastRadius) }

// repoPath maps an index-space path to repository space by restoring the
// prefix the index root strips. An empty prefix means the spaces agree.
func repoPath(prefix, p string) string {
	if prefix == "" || p == "" {
		return p
	}
	return path.Join(prefix, p)
}

// changedRepoSpace returns the diff's changed files sorted. They are already
// in repository space, exactly as git reported them (see
// impact.DiffReport.ChangedFiles); only symbol paths are index space.
func changedRepoSpace(d impact.DiffReport) []string {
	out := append([]string(nil), d.ChangedFiles...)
	sort.Strings(out)
	return out
}

// indexSpace is the inverse of repoPath for a changed file: it strips the
// index-root prefix so the path can be compared with the index's own. A file
// outside the indexed subtree is reported as not in the index.
func indexSpace(prefix, p string) (string, bool) {
	if prefix == "" {
		return p, true
	}
	pre := strings.TrimSuffix(path.Clean(prefix), "/") + "/"
	if rest, ok := strings.CutPrefix(p, pre); ok {
		return rest, true
	}
	return "", false
}
