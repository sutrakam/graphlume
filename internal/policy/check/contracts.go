package check

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sutrakam/graphlume/internal/policy"
)

// contracts implements the contracts rule: run the cross-repository contract
// comparison over the estate and fail on the configured finding kinds.
//
// A member whose index cannot be read is a NOTE, not a violation. The
// comparison is a floor when a member is missing, so a pass is weaker than it
// looks, and the note says so; but making CI red because a sibling repository
// has not been indexed on this runner would make the rule unusable in exactly
// the pipelines it is for. A team that wants the strict reading can read the
// note in the SARIF run notification or the JSON.
func contracts(ctx context.Context, env Env, r policy.Contracts) ([]Violation, []string, error) {
	if env.Contracts == nil {
		return nil, nil, errors.New("this build has no contract comparison wired in")
	}
	estate, err := ResolveEstate(env.Root, r.Estate)
	if err != nil {
		return nil, nil, err
	}
	rep, err := env.Contracts(ctx, estate)
	if err != nil {
		return nil, nil, err
	}

	failOn := r.FailOn
	if len(failOn) == 0 {
		failOn = policy.DefaultContractFailOn
	}
	want := map[string]bool{}
	for _, k := range failOn {
		want[k] = true
	}

	localMember := map[string]bool{}
	for _, m := range rep.Members {
		if env.LocalIndex != "" && filepath.Clean(m.IndexPath) == filepath.Clean(env.LocalIndex) {
			localMember[m.Name] = true
		}
	}

	sev := r.Severity.Or()
	id := policy.RuleID(policy.RuleContracts)
	var out []Violation
	for _, f := range rep.Findings {
		if !want[string(f.Kind)] {
			continue
		}
		v := Violation{
			Rule: policy.RuleContracts, RuleID: id, Severity: sev,
			Confidence: string(f.Confidence),
			Message: fmt.Sprintf("%s: %s %s called from %s (%s:%d): %s",
				f.Kind, orDefault(f.Call.Method, "(no verb)"), f.Call.Path, f.Call.Member, f.Call.File, f.Call.Line, f.Detail),
			// A call in this repository gets its own file and line. A call in
			// another repository must not: "src/api.go" there is a different
			// file from "src/api.go" here, and code scanning would annotate
			// the wrong one. Those are anchored to the estate file, which is
			// where this repository declares the boundary, and the real
			// location is in the evidence.
			Path: filepath.ToSlash(r.Estate),
		}
		if localMember[f.Call.Member] {
			v.Path, v.Line = f.Call.File, f.Call.Line
		}
		v.Evidence = append(v.Evidence, Evidence{
			Path: f.Call.File, Line: f.Call.Line,
			Detail: fmt.Sprintf("call in member %s", f.Call.Member),
		})
		for _, rt := range f.Routes {
			v.Evidence = append(v.Evidence, Evidence{
				Path: rt.File, Line: rt.Line,
				Detail: fmt.Sprintf("route %s in member %s", rt.Endpoint, rt.Member),
			})
		}
		out = append(out, v)
	}

	var notes []string
	if len(rep.UnavailableMembers) > 0 {
		notes = append(notes, fmt.Sprintf("contracts: %d estate member(s) could not be read (%s); the comparison is a floor, "+
			"so a pass does not mean the contracts hold", len(rep.UnavailableMembers), strings.Join(rep.UnavailableMembers, ", ")))
	}
	if len(rep.Truncated) > 0 {
		notes = append(notes, "contracts: the comparison cut some sections at its row cap, so findings may be missing from this list")
	}
	return out, notes, nil
}

// ResolveEstate resolves a repository-relative estate path to a real file that
// is inside the repository, or says why not.
//
// policy.Validate already refuses absolute paths and ".." lexically, because
// the config is untrusted. This is the other half: a symlink committed inside
// the repository can still point anywhere, and a string check cannot see that.
// Both the root and the estate are resolved before comparing, since on macOS
// the temporary and home directories are themselves symlinks and comparing an
// unresolved root with a resolved file would refuse every legitimate path.
func ResolveEstate(root, rel string) (string, error) {
	return ResolveEstateKey(root, rel, "policy.contracts.estate")
}

// ResolveEstateKey is [ResolveEstate] for an estate path read from another key
// of .graphlume.yml, which key names in its messages.
func ResolveEstateKey(root, rel, key string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root %s: %w", root, err)
	}
	joined := filepath.Join(realRoot, filepath.FromSlash(rel))
	real, err := filepath.EvalSymlinks(joined)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("estate file %q does not exist; %s is relative to the repository root", rel, key)
		}
		return "", fmt.Errorf("resolve estate file %q: %w", rel, err)
	}
	back, err := filepath.Rel(realRoot, real)
	if err != nil || back == ".." || strings.HasPrefix(back, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("estate file %q resolves outside the repository (through a symlink); "+
			"a repository's config may only name files inside it", rel)
	}
	return real, nil
}
