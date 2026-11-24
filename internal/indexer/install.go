package indexer

import (
	"context"
	"fmt"
	"strings"
)

// Install is one download a plan would make: which indexer, which exact
// artifact, and where it comes from. Every field derives from the compile-time
// pin table, so what a user is told cannot be written by the repository.
type Install struct {
	Indexer  Indexer
	Language Language
	// Ref is the exact pinned artifact, such as "@scope/pkg@1.2.3".
	Ref string
	// Source says where the bytes come from and how they are checked.
	Source string
	// Pin is the pin to install.
	Pin Pin
}

// String renders the line shown before the install.
func (i Install) String() string {
	return fmt.Sprintf("%s (%s) for %s, from %s", i.Indexer, i.Ref, i.Language, i.Source)
}

// Source describes where a pin is obtained and what verifies it.
func (p Pin) Source() string {
	switch p.Channel {
	case ChannelNPM:
		return "the npm registry (registry.npmjs.org), checked against a pinned SHA-256"
	case ChannelGoInstall:
		return "the Go module proxy (proxy.golang.org), checked by the Go checksum database"
	case ChannelDocker:
		return "a container registry, pulled by digest"
	case ChannelStandaloneRelease:
		return p.ReleaseURL + ", checked against a pinned SHA-256"
	case ChannelNuGetTool:
		return "NuGet (api.nuget.org), checked against a pinned SHA-256, then installed by `dotnet tool install` from that one file"
	}
	return "the user (GraphLume does not install it)"
}

// Where says where the install lands and how it is checked, for the line shown
// beside the command.
func (i Install) Where() string {
	switch i.Pin.Channel {
	case ChannelNPM:
		return "GraphLume's own npm prefix, digest checked first"
	case ChannelNuGetTool:
		return "GraphLume's own .NET tool directory (~/.graphlume/dotnet), digest checked first"
	case ChannelStandaloneRelease:
		return "GraphLume's own bin directory, digest checked first"
	case ChannelGoInstall:
		return "Go's install directory, checked by the Go checksum database"
	}
	return "this machine"
}

// Installs lists the downloads the plan needs, one per indexer, in plan order:
// the steps that can run but have no usable indexer yet. Go is left out when
// skipGo is set, because the type checker built into GraphLume proves Go and
// scip-go is never run by `resolve`.
func (p Plan) Installs(skipGo bool) []Install {
	var out []Install
	seen := map[Indexer]bool{}
	for _, s := range p.Steps {
		if s.Blocked || !s.NeedsProvision || seen[s.Indexer] {
			continue
		}
		if skipGo && s.Project.Language == LangGo {
			continue
		}
		seen[s.Indexer] = true
		out = append(out, Install{
			Indexer: s.Indexer, Language: s.Project.Language,
			Ref: s.Pin.Ref(), Source: s.Pin.Source(), Pin: s.Pin,
		})
	}
	return out
}

// ProvisionFailure is an install that did not happen, and why.
type ProvisionFailure struct {
	Install Install
	Err     error
}

// Provision installs the given indexers and nothing else: it runs no indexer.
// A failed install is reported and the rest still proceed. The project whose
// indexer is missing is then indexed with the heuristic pass only, which is
// what the plan already says for a language with no compiler available.
func Provision(ctx context.Context, plan Plan, installs []Install, opts Options) ([]Installed, []ProvisionFailure) {
	opts = opts.withDefaults()
	prov := &Provisioner{
		Runner:     opts.Runner,
		Policy:     plan.ProvisionPolicy,
		Prompt:     opts.Prompt,
		Audit:      opts.Audit,
		Timeout:    plan.Timeout,
		AssumeYes:  opts.AssumeYes,
		RunnerKind: plan.Runner,
	}
	var done []Installed
	var failed []ProvisionFailure
	for _, in := range installs {
		if err := ctx.Err(); err != nil {
			failed = append(failed, ProvisionFailure{Install: in, Err: err})
			continue
		}
		inst, err := prov.Ensure(ctx, in.Pin)
		if err != nil {
			failed = append(failed, ProvisionFailure{Install: in, Err: err})
			continue
		}
		done = append(done, inst)
	}
	return done, failed
}

// DescribeInstalls renders the "about to install" block, one line per install.
func DescribeInstalls(installs []Install) string {
	var b strings.Builder
	for _, in := range installs {
		b.WriteString("  - " + in.String() + "\n")
	}
	return b.String()
}
