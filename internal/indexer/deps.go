package indexer

import (
	"os"
	"path/filepath"
	"strings"
)

// dependencyAdvisory reports that a project's third-party dependencies are
// declared but not installed.
//
// WHY THIS IS WORTH A CHECK. Both npm-channel indexers resolve imports through
// the dependency tree on disk. Without it they do not fail -- they succeed
// smaller. Measured on a two-file TypeScript project: 675 bytes of index
// without node_modules, 732 with, both exiting 0 with "done". The missing 57
// bytes are the resolved external symbol, and nothing in the output
// distinguishes the two runs.
//
// That is the failure mode this codebase treats as the serious one: not an
// error, but a quiet reduction in what the graph knows, indistinguishable from
// a repository that genuinely has fewer edges.
//
// Deliberately NOT the Java-style version ceiling. That check exists because a
// container digest freezes a JDK; the npm indexers install the current compiler
// at provision time (scip-typescript depends on typescript ^5.6.2, resolved
// fresh), and neither package declares an `engines` floor. Inventing a version
// ceiling for them would block projects that work.
func dependencyAdvisory(p Project, dir string) string {
	switch p.Language {
	case LangTypeScript:
		if !exists(filepath.Join(dir, "package.json")) {
			return ""
		}
		// In monorepos (pnpm/yarn/npm workspaces), node_modules may live at the workspace root.
		curr := dir
		for i := 0; i < 4; i++ {
			if exists(filepath.Join(curr, "node_modules")) {
				return ""
			}
			parent := filepath.Dir(curr)
			if parent == curr {
				break
			}
			curr = parent
		}
		return "dependencies are declared in package.json but node_modules is absent; " +
			"imports into third-party packages will not resolve and the index will be " +
			"quietly smaller. Run: npm install (or pnpm install / yarn install)"

	case LangPython:
		// A virtualenv inside the project is the common layout; an interpreter
		// managed elsewhere is equally valid, so this only fires when the
		// project declares dependencies AND carries no local environment.
		if !declaresPythonDeps(dir) || hasLocalEnv(dir) {
			return ""
		}
		fixCmd := "python -m venv .venv && source .venv/bin/activate && pip install -r requirements.txt"
		if exists(filepath.Join(dir, "poetry.lock")) {
			fixCmd = "poetry install (or poetry shell)"
		} else if exists(filepath.Join(dir, "uv.lock")) {
			fixCmd = "uv sync"
		} else if exists(filepath.Join(dir, "Pipfile")) {
			fixCmd = "pipenv install"
		}
		return "dependencies are declared but no virtualenv is present in the project; " +
			"imports into third-party packages may not resolve and the index will be " +
			"quietly smaller. To resolve: " + fixCmd + " (or activate the existing environment)"

	case LangCSharp:
		hasProj := false
		if entries, err := os.ReadDir(dir); err == nil {
			for _, e := range entries {
				n := e.Name()
				if strings.HasSuffix(n, ".csproj") || strings.HasSuffix(n, ".sln") {
					hasProj = true
					break
				}
			}
		}
		if hasProj && !exists(filepath.Join(dir, "obj", "project.assets.json")) && !exists(filepath.Join(dir, "packages.lock.json")) {
			return "dependencies are declared in .NET project but not restored; " +
				"imports into NuGet packages will not resolve and the index will be quietly smaller. Run: dotnet restore"
		}

	case LangRuby:
		if exists(filepath.Join(dir, "Gemfile")) && !exists(filepath.Join(dir, "Gemfile.lock")) && !exists(filepath.Join(dir, "vendor", "bundle")) {
			return "dependencies are declared in Gemfile but lockfile/vendor bundle is absent; " +
				"imports into third-party gems will not resolve. Run: bundle install"
		}

	case LangRust:
		if exists(filepath.Join(dir, "Cargo.toml")) && !exists(filepath.Join(dir, "Cargo.lock")) {
			return "dependencies are declared in Cargo.toml but Cargo.lock is absent; " +
				"Run: cargo check (or cargo fetch)"
		}

	case LangJava:
		for _, w := range []string{"gradlew", "mvnw"} {
			p := filepath.Join(dir, w)
			if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
				if fi.Mode().Perm()&0o111 == 0 {
					return "wrapper " + w + " is present but not executable (+x); " +
						"To resolve: chmod +x " + p
				}
			}
		}
	}
	return ""
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func declaresPythonDeps(dir string) bool {
	for _, f := range []string{"requirements.txt", "pyproject.toml", "Pipfile", "setup.py", "poetry.lock", "uv.lock"} {
		if exists(filepath.Join(dir, f)) {
			return true
		}
	}
	return false
}

func hasLocalEnv(dir string) bool {
	if os.Getenv("VIRTUAL_ENV") != "" || os.Getenv("CONDA_PREFIX") != "" {
		return true
	}
	if findPythonVenv(dir, "") != "" {
		return true
	}
	curr := dir
	for i := 0; i < 4; i++ {
		for _, d := range []string{".venv", "venv", "env", ".tox", ".poetry", ".pixi"} {
			if exists(filepath.Join(curr, d)) {
				return true
			}
		}
		parent := filepath.Dir(curr)
		if parent == curr {
			break
		}
		curr = parent
	}
	return false
}
