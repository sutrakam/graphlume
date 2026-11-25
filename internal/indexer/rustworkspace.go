package indexer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// A Cargo workspace is indexed by ONE `rust-analyzer scip` run from the
// workspace root: rust-analyzer loads every member crate from the root manifest
// and writes one index whose document paths are relative to that root, which
// is what GraphLume's importer expects for a project at the root. The member
// crates therefore need no runs of their own, and this file only answers which
// directories are members, so the planner can say how many one run covers and
// leave them out of the plan.
//
// Cargo.toml is read here for two facts and nothing else, and what it says can
// only select directories to report or to leave out of the plan: the
// `members` and `exclude` arrays of the [workspace] table. It never reaches an
// argument, an environment variable or a command.

// cargoWorkspace is what a root Cargo.toml declares.
type cargoWorkspace struct {
	// Members are the member crates' directories, relative to the workspace
	// root and slash separated, in order. The root itself is listed as "." when
	// its manifest also has a [package] table.
	Members []string
	// Excluded are the directories the manifest excludes from the workspace.
	Excluded []string
}

// maxCargoManifest bounds a Cargo.toml read.
const maxCargoManifest = 1 << 20

// readCargoWorkspace reads dir/Cargo.toml. ok is false when it has no
// [workspace] table.
func readCargoWorkspace(dir string) (cargoWorkspace, bool) {
	raw, found := readLimited(filepath.Join(dir, "Cargo.toml"), maxCargoManifest)
	if !found {
		return cargoWorkspace{}, false
	}
	var ws cargoWorkspace
	var memberPatterns, excludePatterns []string
	hasWorkspace, hasPackage := false, false
	section := ""
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	sc.Buffer(make([]byte, 0, 64<<10), maxCargoManifest)
	var pending *[]string // the array being collected across lines
	depth := 0
	var buf strings.Builder
	flush := func() {
		if pending != nil {
			*pending = append(*pending, tomlStrings(buf.String())...)
			pending = nil
			buf.Reset()
			depth = 0
		}
	}
	for sc.Scan() {
		line := stripTomlComment(sc.Text())
		trimmed := strings.TrimSpace(line)
		if pending != nil {
			buf.WriteString(" " + trimmed)
			depth += bracketDelta(trimmed)
			if depth <= 0 {
				flush()
			}
			continue
		}
		if strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "[[") {
			section = strings.TrimSpace(strings.Trim(trimmed, "[]"))
			hasWorkspace = hasWorkspace || section == "workspace"
			hasPackage = hasPackage || section == "package"
			continue
		}
		if strings.HasPrefix(trimmed, "[[") || section != "workspace" {
			continue
		}
		key, val, isKV := strings.Cut(trimmed, "=")
		if !isKV {
			continue
		}
		switch strings.TrimSpace(key) {
		case "members":
			pending = &memberPatterns
		case "exclude":
			pending = &excludePatterns
		default:
			continue
		}
		buf.WriteString(val)
		depth = bracketDelta(val)
		if depth <= 0 {
			flush()
		}
	}
	flush()
	if !hasWorkspace {
		return cargoWorkspace{}, false
	}

	// Members: directories the patterns select that hold a Cargo.toml, less the
	// excluded ones.
	patterns := append([]string(nil), memberPatterns...)
	for _, e := range excludePatterns {
		patterns = append(patterns, "!"+e)
	}
	for _, rel := range expandWorkspaceDirs(dir, patterns) {
		if isRegularFile(filepath.Join(dir, filepath.FromSlash(rel), "Cargo.toml")) {
			ws.Members = append(ws.Members, rel)
		}
	}
	if hasPackage {
		ws.Members = append([]string{"."}, ws.Members...)
	}
	for _, e := range excludePatterns {
		ws.Excluded = append(ws.Excluded, expandWorkspaceDirs(dir, []string{e})...)
	}
	return ws, true
}

// stripTomlComment removes a trailing # comment that is not inside a string.
func stripTomlComment(s string) string {
	inStr := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inStr != 0:
			if c == '\\' && inStr == '"' {
				i++
			} else if c == inStr {
				inStr = 0
			}
		case c == '"' || c == '\'':
			inStr = c
		case c == '#':
			return s[:i]
		}
	}
	return s
}

// bracketDelta is the number of "[" minus "]" outside strings.
func bracketDelta(s string) int {
	d := 0
	inStr := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inStr != 0:
			if c == '\\' && inStr == '"' {
				i++
			} else if c == inStr {
				inStr = 0
			}
		case c == '"' || c == '\'':
			inStr = c
		case c == '[':
			d++
		case c == ']':
			d--
		}
	}
	return d
}

// tomlStrings returns the quoted strings in s.
func tomlStrings(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		q := s[i]
		if q != '"' && q != '\'' {
			continue
		}
		j := i + 1
		var b strings.Builder
		for ; j < len(s) && s[j] != q; j++ {
			if s[j] == '\\' && q == '"' && j+1 < len(s) {
				j++
			}
			b.WriteByte(s[j])
		}
		out = append(out, b.String())
		i = j
	}
	return out
}

// cargoWorkspaceCovers reports whether the run of a workspace above p already
// reads p: p lies beneath a detected Rust project whose Cargo.toml declares a
// workspace, and is not one of its excluded directories. An excluded crate is
// not part of that workspace, so it stays a project of its own.
func cargoWorkspaceCovers(repoRoot string, projects []Project, p Project) bool {
	for _, o := range projects {
		if o.Language != LangRust || o.Dir == p.Dir {
			continue
		}
		if o.Dir != "." && !strings.HasPrefix(p.Dir, o.Dir+"/") {
			continue
		}
		root := filepath.Join(repoRoot, filepath.FromSlash(path.Clean(o.Dir)))
		ws, ok := readCargoWorkspace(root)
		if !ok {
			continue
		}
		rel := p.Dir
		if o.Dir != "." {
			rel = strings.TrimPrefix(p.Dir, o.Dir+"/")
		}
		for _, m := range ws.Members {
			if rel == m {
				return true
			}
		}
	}
	return false
}

// rustSafeConfig is the rust-analyzer configuration a run uses unless the
// repository's build is allowed: no build scripts (build.rs), no procedural
// macros, and nothing that expands them. It is a fixed literal; nothing from
// the repository is part of it.
const rustSafeConfig = `{
  "cargo": {"buildScripts": {"enable": false}},
  "procMacro": {"enable": false, "attributes": {"enable": false}},
  "checkOnSave": false
}
`

// writeRustSafeConfig writes rustSafeConfig to file.
func writeRustSafeConfig(file string) error {
	if !json.Valid([]byte(rustSafeConfig)) {
		return fmt.Errorf("indexer: the rust-analyzer safe configuration is not valid JSON")
	}
	return os.WriteFile(file, []byte(rustSafeConfig), 0o600)
}

// rustNotes says what one rust-analyzer run covers and what the chosen mode
// leaves out, in sentences meant for a user, so that a reduced proof is stated
// where the run is reported rather than discovered afterwards.
func rustNotes(repoRoot string, p Project, allowRepoBuild bool) []string {
	var notes []string
	if ws, ok := readCargoWorkspace(filepath.Join(repoRoot, filepath.FromSlash(p.Dir))); ok {
		n := 0
		for _, m := range ws.Members {
			if m != "." {
				n++
			}
		}
		if n > 0 {
			word := "crates"
			if n == 1 {
				word = "crate"
			}
			notes = append(notes, fmt.Sprintf("Cargo workspace: one run reads the root and its %d member %s", n, word))
		}
	}
	if !allowRepoBuild {
		notes = append(notes,
			"build scripts and procedural macros are not run, and cargo is kept offline, so code they generate, "+
				"calls made through macro expansion and calls into crates that are not already in cargo's cache "+
				"stay graded, not proven; re-run with --allow-repo-build to run them (it also lets cargo fetch crates)")
	}
	return notes
}
