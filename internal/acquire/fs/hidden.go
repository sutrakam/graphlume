package fs

import (
	"path"
	"strings"
)

// Which hidden files and directories are part of a project.
//
// A name that begins with a dot is hidden by convention, and the convention
// covers two opposite things. `.github/workflows/ci.yml`, `.eslintrc.js` and
// `.env.example` are a project's own configuration, committed and reviewed
// like source. `.venv/`, `.idea/`, `.pytest_cache/`, `.DS_Store` and a
// `.syms.json` some tool left behind are state that an editor or a tool keeps
// beside the code. A walk that makes no distinction would index a scratch file
// dropped in the project directory on the next run, and a hidden
// directory holding a second copy of the repository (an agent's worktrees, a
// virtual environment) in full.
//
// THE RULE.
//
//	Hidden directory   Not indexed, unless its name is one that holds project
//	                   automation or configuration (sourceDotDirs): .github,
//	                   .gitlab, .circleci, .devcontainer, .husky and the like.
//
//	Hidden file        Indexed, unless it is editor or tool scratch: a data or
//	                   temporary file by extension (.json, .log, .tmp, .swp,
//	                   .bak, .lock, .db, .cache ...), an editor's swap, lock
//	                   or backup file, or a known state file (.DS_Store,
//	                   .coverage). A hidden JSON file that is configuration
//	                   stays: one named like a run-control file (.eslintrc.json,
//	                   .babelrc.json) or on the short list (.mcp.json,
//	                   .cspell.json, .markdownlint.json ...).
//
//	.git, .graphlume  Never indexed, at any depth, whatever the configuration
//	                   says. One is the object store; the other is this tool's
//	                   own index.
//
// The two halves lean opposite ways on purpose. Almost every hidden directory
// is tool state, and descending one by mistake can double an index, so
// directories are opt-in. Hidden files are mostly configuration, and missing
// `.gitlab-ci.yml` or `.env.example` silently empties `graphlume infra` and
// `graphlume config`, so files are opt-out and only what is recognisably
// scratch is dropped.
//
// CONFIGURATION, in `.graphlume.yml`:
//
//	index.exclude   removes more, as it always has: `.github/**`, `.*.yml`.
//	index.include   brings a hidden name back, when a pattern component that
//	                itself begins with a dot matches it: `.claude/**`,
//	                `.syms.json`, `**/.meta/*.json`, or `.*` for every hidden
//	                name. As in a shell, `*` and `**` do not match a hidden
//	                name, so the default `**` does not undo the rule and
//	                `.claude/**` brings back .claude and what is visible
//	                inside it, not the hidden entries further down. Naming
//	                a hidden directory opens it: the visible files inside are
//	                then matched against the whole include list like any
//	                others. Setting index.include replaces the default, so
//	                keep `**` in the list.
//
// What is left out by this rule is not recorded in the index, exactly as
// node_modules is not: it is a decision about what the project is, not a file
// the indexer failed to read.

// sourceDotDirs are the hidden directories that hold project automation and
// configuration. Everything here is something a repository commits and a
// reviewer reads.
var sourceDotDirs = map[string]bool{
	".github":          true, // workflows, actions, issue templates, CODEOWNERS
	".gitlab":          true,
	".gitea":           true,
	".forgejo":         true,
	".circleci":        true,
	".buildkite":       true,
	".azuredevops":     true,
	".azure-pipelines": true,
	".woodpecker":      true,
	".tekton":          true,
	".devcontainer":    true,
	".husky":           true, // git hooks kept as source
	".cargo":           true, // config.toml
	".mvn":             true, // maven wrapper and jvm.config
	".config":          true, // dotnet tools, nextest, and other per-project config
	".storybook":       true,
	".changeset":       true,
	".well-known":      true, // served as part of a web application
}

// neverIndexed are names no configuration can bring into the index.
var neverIndexed = map[string]bool{".git": true, IndexDirName: true}

// IndexDirName is the directory GraphLume keeps its own index in. Declared
// here because the walk must refuse it by name; the CLI owns the path.
const IndexDirName = ".graphlume"

// scratchDotExts are the extensions that mark a hidden file as data or
// temporary state. A visible file with one of these is unaffected.
var scratchDotExts = map[string]bool{
	".json": true, ".jsonl": true, ".ndjson": true,
	".log": true, ".tmp": true, ".temp": true, ".bak": true, ".old": true,
	".orig": true, ".rej": true, ".swp": true, ".swo": true, ".swn": true,
	".pid": true, ".lock": true, ".cache": true, ".db": true, ".sqlite": true,
	".sqlite3": true, ".out": true, ".prof": true, ".dat": true, ".bin": true,
	".dump": true, ".stackdump": true, ".part": true, ".crdownload": true,
}

// scratchDotFiles are hidden state files with no telling extension.
var scratchDotFiles = map[string]bool{
	".DS_Store": true, ".localized": true, ".directory": true,
	".coverage": true, ".eslintcache": true, ".stylelintcache": true,
	".prettiercache": true, ".node_repl_history": true, ".python_history": true,
	".bash_history": true, ".zsh_history": true, ".viminfo": true,
	".lesshst": true, ".netrwhist": true,
}

// configDotJSON are hidden JSON files that are configuration, by exact name.
// Run-control names (.eslintrc.json) are recognised by shape instead.
var configDotJSON = map[string]bool{
	".mcp.json": true, ".cspell.json": true, ".markdownlint.json": true,
	".devcontainer.json": true, ".jscpd.json": true,
	".luarc.json": true, ".release-please-manifest.json": true,
	".prettierrc.json": true, ".watchmanconfig.json": true, ".vsconfig.json": true,
	".lintstagedrc.json": true, ".commitlintrc.json": true, ".releaserc.json": true,
}

// isHidden reports whether a single path component is a hidden name.
func isHidden(name string) bool {
	return len(name) > 1 && name[0] == '.' && name != ".."
}

// hiddenScratch reports whether a hidden name is tool or editor state under
// the rule above. name must be hidden.
func hiddenScratch(name string, isDir bool) bool {
	if isDir {
		return !sourceDotDirs[name]
	}
	if scratchDotFiles[name] {
		return true
	}
	// Editor leftovers: Emacs locks and autosaves (.#name, .name~), Vim swap
	// files (.name.swp is caught by extension; .swp alone here), macOS
	// AppleDouble forks (._name).
	if strings.HasPrefix(name, ".#") || strings.HasPrefix(name, "._") || strings.HasSuffix(name, "~") {
		return true
	}
	ext := strings.ToLower(path.Ext(name))
	if ext == name {
		// ".json", ".log": the whole name is the "extension". Too little to
		// call scratch; `.env` and `.npmrc` have the same shape.
		ext = ""
	}
	if !scratchDotExts[ext] {
		return false
	}
	if ext == ".json" {
		return !configJSONName(name)
	}
	return true
}

// configJSONName reports whether a hidden JSON file is configuration: on the
// list, or shaped like a run-control file (".eslintrc.json", ".c8rc.json").
func configJSONName(name string) bool {
	if configDotJSON[name] {
		return true
	}
	stem := strings.TrimSuffix(name, path.Ext(name))
	return strings.HasSuffix(stem, "rc") || strings.HasSuffix(stem, "config")
}

// hiddenExcluded applies the rule to one directory entry. rel is its
// repo-relative slash path and name its last component.
func (w *Walker) hiddenExcluded(rel, name string, isDir bool) bool {
	if !isHidden(name) {
		return false
	}
	if neverIndexed[name] {
		return true
	}
	if !hiddenScratch(name, isDir) {
		return false
	}
	return !w.namedByInclude(rel, isDir)
}

// hiddenPathExcluded applies the rule to a whole path, for callers that are
// handed one without having walked to it: every hidden component must pass.
func (w *Walker) hiddenPathExcluded(rel string, isDir bool) bool {
	if !strings.Contains(rel, "/.") && !strings.HasPrefix(rel, ".") {
		return false // no hidden component; the common case
	}
	parts := strings.Split(rel, "/")
	for i, name := range parts {
		last := i == len(parts)-1
		if w.hiddenExcluded(strings.Join(parts[:i+1], "/"), name, !last || isDir) {
			return true
		}
	}
	return false
}

// namedByInclude reports whether an index.include pattern names this hidden
// entry: a component of the pattern that itself begins with a dot matches the
// entry's name, and the pattern up to that component matches the path up to
// the entry.
//
// For a file the component must be the pattern's last, so the whole pattern
// matches. For a directory any component will do, because a pattern that
// names a hidden directory part-way along (`.config/app/*.yml`,
// `**/.meta/*.json`) can only match beneath it, and the walk has to descend to
// find out.
func (w *Walker) namedByInclude(rel string, isDir bool) bool {
	if len(w.includeHidden) == 0 {
		return false
	}
	parts := strings.Split(rel, "/")
	name := parts[len(parts)-1]
	for i := range w.includeHidden {
		comps := w.includeHidden[i].comps
		last := len(comps) - 1
		if w.includeHidden[i].contentsOnly {
			// "dir/**" compiles with a synthetic trailing "*"; the pattern's
			// own last component is the one before the "**".
			last = len(comps) - 3
		}
		for k := range comps {
			if !strings.HasPrefix(comps[k].s, ".") || !matchSeg(comps[k], name) {
				continue
			}
			if !isDir && k != last {
				continue
			}
			if matchComps(comps[:k+1], parts) {
				return true
			}
		}
	}
	return false
}

// namesHidden reports whether a glob has a component that begins with a dot,
// which is what lets it reach hidden names.
func namesHidden(glob string) bool {
	for _, part := range strings.Split(strings.TrimPrefix(glob, "/"), "/") {
		if isHidden(part) {
			return true
		}
	}
	return false
}
