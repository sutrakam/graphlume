package gotypes

import (
	"go/build"
	"go/version"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// findGoroot locates a Go installation's root WITHOUT executing anything, for
// a repository that needs at least Go version `need` (a go.mod `go` line such
// as "1.26.10", or "" when it asks for nothing).
//
// The candidates, in order: the GOROOT environment variable; the first `go` on
// PATH, with symlinks resolved, whose directory's parent is the root
// (bin/go -> root). A candidate is accepted only when src/fmt exists under it,
// so a stale variable or a wrapper script in a bin directory is skipped rather
// than believed.
//
// The first candidate that is new enough wins. When none is, the go command
// would download a toolchain and switch to it (GOTOOLCHAIN=auto), and the
// module cache then holds it under golang.org/toolchain@v0.0.1-goX.Y.Z.os-arch;
// that is the root used, as the repository is built with it. Failing that, the
// first candidate is used even though it is older: a standard library that is
// too old to have a package leaves that import a stub, which withholds and
// never guesses.
//
// Returns "" when no candidate exists, which the caller reports as one line
// naming what to set.
func findGoroot(env func(string) string, modcache, need string) string {
	valid := func(root string) bool {
		if root == "" {
			return false
		}
		fi, err := os.Stat(filepath.Join(root, "src", "fmt"))
		return err == nil && fi.IsDir()
	}
	var candidates []string
	if r := env("GOROOT"); valid(r) {
		candidates = append(candidates, filepath.Clean(r))
	}
	for _, dir := range filepath.SplitList(env("PATH")) {
		if dir == "" {
			continue
		}
		cand := filepath.Join(dir, "go")
		fi, err := os.Stat(cand)
		if err != nil || fi.IsDir() {
			continue
		}
		real, err := filepath.EvalSymlinks(cand)
		if err != nil {
			continue
		}
		if root := filepath.Dir(filepath.Dir(real)); valid(root) {
			candidates = append(candidates, root)
			break
		}
	}
	if len(candidates) == 0 {
		return "" // nothing named a Go installation; a cached toolchain alone is not "found"
	}
	for _, c := range candidates {
		if rootSatisfies(c, need) {
			return c
		}
	}
	if t := cachedToolchain(modcache, need); t != "" {
		return t
	}
	return candidates[0]
}

// rootSatisfies reports whether a GOROOT's VERSION file names a Go release at
// least `need`. A root whose version cannot be read satisfies any need.
func rootSatisfies(root, need string) bool {
	if need == "" {
		return true
	}
	data, err := os.ReadFile(filepath.Join(root, "VERSION")) //nolint:gosec // the Go installation's own VERSION file
	if err != nil {
		return true
	}
	have := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
	if !version.IsValid(have) {
		return true
	}
	return version.Compare(have, "go"+need) >= 0
}

// cachedToolchain finds the newest downloaded toolchain in the module cache
// for this platform that satisfies need ("" when there is none).
func cachedToolchain(modcache, need string) string {
	if modcache == "" {
		return ""
	}
	dirs, err := filepath.Glob(filepath.Join(modcache, "golang.org", "toolchain@v0.0.1-go*."+build.Default.GOOS+"-"+build.Default.GOARCH))
	if err != nil || len(dirs) == 0 {
		return ""
	}
	type tc struct{ dir, ver string }
	var found []tc
	for _, d := range dirs {
		base := filepath.Base(d)
		i := strings.Index(base, "-go")
		j := strings.LastIndex(base, "."+build.Default.GOOS+"-")
		if i < 0 || j <= i {
			continue
		}
		v := "go" + base[i+3:j]
		if !version.IsValid(v) {
			continue
		}
		if need != "" && version.Compare(v, "go"+need) < 0 {
			continue
		}
		if fi, err := os.Stat(filepath.Join(d, "src", "fmt")); err != nil || !fi.IsDir() {
			continue
		}
		found = append(found, tc{d, v})
	}
	if len(found) == 0 {
		return ""
	}
	// The lowest that satisfies: it is what the go command would pick for a
	// `go` line, and it keeps two runs on one machine on one standard library.
	sort.Slice(found, func(a, b int) bool {
		if c := version.Compare(found[a].ver, found[b].ver); c != 0 {
			return c < 0
		}
		return found[a].dir < found[b].dir
	})
	return found[0].dir
}

// findModCache locates the module cache: GOMODCACHE, else $GOPATH/pkg/mod
// (the first GOPATH entry that has one), else ~/go/pkg/mod. Returns "" when
// none exists as a directory.
func findModCache(env func(string) string) string {
	isDir := func(p string) bool {
		fi, err := os.Stat(p)
		return err == nil && fi.IsDir()
	}
	if m := env("GOMODCACHE"); m != "" {
		if isDir(m) {
			return filepath.Clean(m)
		}
		return ""
	}
	for _, gp := range filepath.SplitList(env("GOPATH")) {
		if gp == "" {
			continue
		}
		if p := filepath.Join(gp, "pkg", "mod"); isDir(p) {
			return p
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if p := filepath.Join(home, "go", "pkg", "mod"); isDir(p) {
			return p
		}
	}
	return ""
}
