package indexer

import (
	"strings"
)

// projectBlock returns why a C/C++ project cannot be indexed however the
// machine is set up, or "" when nothing is known against it. Only a file's
// name is looked at.
func projectBlock(repoRoot string, p Project) string {
	switch p.Language {
	case LangCPP:
		if p.hasMarker("compile_commands.json") {
			return ""
		}
		return "scip-clang reads a compilation database and this project has no compile_commands.json " +
			"beside its " + strings.Join(p.Markers, ", ") + ", so it cannot run. Generate one and copy it " +
			"into " + dirLabel(p.Dir) + " (a copy, because a symbolic link is not trusted as a marker): " +
			"CMake: cmake -S . -B build -DCMAKE_EXPORT_COMPILE_COMMANDS=ON && cp build/compile_commands.json " +
			"<project dir>; Meson writes it into its build directory; for make, run the build under bear"
	}
	return ""
}

func dirLabel(dir string) string {
	if dir == "." || dir == "" {
		return "the repository root"
	}
	return dir
}
