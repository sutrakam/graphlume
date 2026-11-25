package cli

import (
	"fmt"
	"path/filepath"

	"github.com/sutrakam/graphlume/internal/config"
	"github.com/sutrakam/graphlume/internal/multirepo"
	policycheck "github.com/sutrakam/graphlume/internal/policy/check"
)

// The estate the MCP server's estate tool reads, and where it may come from.
//
// An estate file names other repositories' indexes, and the tool opens every
// one of them, so who may name the file matters. Three sources, in order:
//
//   - serve --estate FILE: the operator's own flag.
//   - estate: in user config: written by the machine's owner, so trusted to
//     name a file anywhere.
//   - estate: in the repository's .graphlume.yml: repository content, so held
//     to the rule policy.contracts.estate is held to. config.Validate refuses
//     an absolute path or one that climbs out lexically; here the path is
//     resolved through symlinks and refused if it lands outside the root.
//
// A trusted source wins, so a repository cannot replace the estate its user
// chose.

// estateRepoKey names the .graphlume.yml key in messages.
const estateRepoKey = "estate"

// resolveServeEstate returns the estate file the server should read, or ""
// when none is configured.
func resolveServeEstate(cfg config.Config, flagPath string) (string, error) {
	switch {
	case flagPath != "":
		abs, err := filepath.Abs(flagPath)
		if err != nil {
			return "", fmt.Errorf("--estate %q: %w", flagPath, err)
		}
		return abs, nil
	case cfg.Estate != "":
		return cfg.Estate, nil
	case cfg.RepoEstate != "":
		real, err := policycheck.ResolveEstateKey(cfg.RepoRoot, cfg.RepoEstate, estateRepoKey)
		if err != nil {
			return "", fmt.Errorf("%s in %s: %w", estateRepoKey, config.RepoConfigName, err)
		}
		return real, nil
	}
	return "", nil
}

// openServeEstate builds the estate service from the configured estate file,
// or returns nil when none is configured. Members are opened lazily, read-only,
// through the pool and memberStoreOpener, so a member that cannot be opened is
// reported by the tool and does not stop the server.
func openServeEstate(cfg config.Config, flagPath string) (*multirepo.EstateService, error) {
	path, err := resolveServeEstate(cfg, flagPath)
	if err != nil || path == "" {
		return nil, err
	}
	declared, err := multirepo.LoadEstate(path)
	if err != nil {
		return nil, err
	}
	members, err := normalizeMembers(declared)
	if err != nil {
		return nil, err
	}
	return multirepo.NewEstateService(members, memberStoreOpener, Version), nil
}
