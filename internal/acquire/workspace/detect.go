package workspace

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// WorkspaceManifestFileName is the explicit workspace definition.
const WorkspaceManifestFileName = "workspace.yaml"

// ExplicitWorkspaceManifest defines declarative service topology.
type ExplicitWorkspaceManifest struct {
	Services []ExplicitService `yaml:"services"`
}

// ExplicitService defines one service in workspace.yaml.
type ExplicitService struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
	Role string `yaml:"role"` // "service", "shared_lib", "gateway", "infra"
}

// DetectedService is one service partition discovered within a workspace.
type DetectedService struct {
	ID       string `json:"id"`
	RelPath  string `json:"rel_path"`
	AbsPath  string `json:"abs_path"`
	Role     string `json:"role"` // "service", "shared_lib", "gateway", "infra"
	Manifest string `json:"manifest,omitempty"`
}

// WorkspaceTopology captures the detected layout of a project root.
type WorkspaceTopology struct {
	IsWorkspace bool              `json:"is_workspace"`
	Type        string            `json:"type"` // "single_repo", "explicit_manifest", "polyrepo", "monorepo"
	Root        string            `json:"root"`
	Services    []DetectedService `json:"services"`
}

// DetectWorkspace scans root to determine if it is a multi-project workspace or a single repo.
func DetectWorkspace(root string) (*WorkspaceTopology, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	topo := &WorkspaceTopology{
		IsWorkspace: false,
		Type:        "single_repo",
		Root:        absRoot,
		Services:    []DetectedService{},
	}

	// 1. Check for explicit workspace manifest: .graphlume/workspace.yaml
	manifestPath := filepath.Join(absRoot, ".graphlume", WorkspaceManifestFileName)
	if data, err := os.ReadFile(manifestPath); err == nil { //nolint:gosec // G304: a fixed name under the workspace root
		var manifest ExplicitWorkspaceManifest
		dec := yaml.NewDecoder(strings.NewReader(string(data)))
		dec.KnownFields(true) // Strict structural decoding: zero execution fields allowed
		if err := dec.Decode(&manifest); err == nil && len(manifest.Services) >= 2 {
			topo.IsWorkspace = true
			topo.Type = "explicit_manifest"
			for _, s := range manifest.Services {
				name := strings.TrimSpace(s.Name)
				p := strings.TrimSpace(s.Path)
				if name == "" || p == "" {
					continue
				}
				role := strings.TrimSpace(s.Role)
				if role == "" {
					role = "service"
				}
				svcAbs := filepath.Join(absRoot, p)
				topo.Services = append(topo.Services, DetectedService{
					ID:      name,
					RelPath: filepath.Clean(p),
					AbsPath: filepath.Clean(svcAbs),
					Role:    role,
				})
			}
			sort.Slice(topo.Services, func(i, j int) bool { return topo.Services[i].ID < topo.Services[j].ID })
			return topo, nil
		}
	}

	// 2. Polyrepo check: root contains >= 2 subdirectories with their own .git
	subdirs, _ := os.ReadDir(absRoot)
	var gitChildRepos []DetectedService
	for _, entry := range subdirs {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		childDir := filepath.Join(absRoot, entry.Name())
		gitDir := filepath.Join(childDir, ".git")
		if fi, err := os.Stat(gitDir); err == nil && fi.IsDir() {
			gitChildRepos = append(gitChildRepos, DetectedService{
				ID:      entry.Name(),
				RelPath: entry.Name(),
				AbsPath: childDir,
				Role:    "service",
			})
		}
	}
	if len(gitChildRepos) >= 2 {
		topo.IsWorkspace = true
		topo.Type = "polyrepo"
		topo.Services = gitChildRepos
		sort.Slice(topo.Services, func(i, j int) bool { return topo.Services[i].ID < topo.Services[j].ID })
		return topo, nil
	}

	// 3. Monorepo workspace check: root has .git or workspace manifest (go.work, pnpm-workspace, etc.)
	isMonorepoRoot := false
	for _, marker := range []string{"go.work", "pnpm-workspace.yaml", "nx.json", "turbo.json"} {
		if _, err := os.Stat(filepath.Join(absRoot, marker)); err == nil {
			isMonorepoRoot = true
			break
		}
	}
	// Check Cargo.toml for [workspace]
	if cargoData, err := os.ReadFile(filepath.Join(absRoot, "Cargo.toml")); err == nil { //nolint:gosec // G304: a fixed name under the workspace root
		if strings.Contains(string(cargoData), "[workspace]") {
			isMonorepoRoot = true
		}
	}

	// Scan subdirectories for language manifests: go.mod, package.json, pyproject.toml, pom.xml, Cargo.toml
	var monorepoServices []DetectedService
	searchDirs := []string{"services", "apps", "packages", "modules", "."}

	for _, sdir := range searchDirs {
		scanBase := filepath.Join(absRoot, sdir)
		entries, err := os.ReadDir(scanBase)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || (sdir == "." && (e.Name() == "cmd" || e.Name() == "internal" || e.Name() == "pkg")) {
				continue
			}
			targetDir := filepath.Join(scanBase, e.Name())
			if targetDir == absRoot {
				continue
			}

			manifestFound := ""
			for _, mfile := range []string{"go.mod", "package.json", "pyproject.toml", "pom.xml", "Cargo.toml", "build.gradle"} {
				if _, err := os.Stat(filepath.Join(targetDir, mfile)); err == nil {
					manifestFound = mfile
					break
				}
			}

			if manifestFound != "" {
				rel, _ := filepath.Rel(absRoot, targetDir)
				role := "service"
				if strings.Contains(rel, "lib") || strings.Contains(rel, "common") || strings.Contains(rel, "proto") {
					role = "shared_lib"
				}
				monorepoServices = append(monorepoServices, DetectedService{
					ID:       filepath.Base(targetDir),
					RelPath:  rel,
					AbsPath:  targetDir,
					Role:     role,
					Manifest: manifestFound,
				})
			}
		}
	}

	if (isMonorepoRoot && len(monorepoServices) >= 2) || len(monorepoServices) >= 2 {
		topo.IsWorkspace = true
		topo.Type = "monorepo"
		topo.Services = monorepoServices
		sort.Slice(topo.Services, func(i, j int) bool { return topo.Services[i].ID < topo.Services[j].ID })
		return topo, nil
	}

	return topo, nil
}
