package gotypes

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// A repository with a go.work builds as one workspace: the version of every
// dependency is the highest any workspace module requires, and the replace
// directives of the workspace and of every module apply to all of them.
// Reading only the module's own go.mod would pick a lower version than the one
// the program is built with, which is wrong in exactly the way that matters
// here: a dependency's types are then the wrong types.
//
// This is the part of workspace mode this package honours: `use`, `replace`,
// and the highest-requirement rule, which is what minimal version selection
// reduces to for the complete requirement lists a tidy module carries.

// loadWorkspace reads root/go.work and returns, for the modules it names, the
// merged requirements and replacements. nil means there is no workspace.
func loadWorkspace(root string) *workspace {
	data, err := os.ReadFile(filepath.Join(root, "go.work")) //nolint:gosec // the repository's own go.work
	if err != nil {
		return nil
	}
	w := &workspace{dirs: map[string]bool{}, require: map[string]string{}}
	var work modFile
	block := ""
	var uses []string
	for _, raw := range strings.Split(string(data), "\n") {
		line := raw
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if block != "" {
			if line == ")" {
				block = ""
				continue
			}
			if block == "use" {
				uses = append(uses, unquote(line))
			} else {
				work.directive(block, fields(line))
			}
			continue
		}
		f := fields(line)
		if len(f) >= 2 && f[1] == "(" {
			block = f[0]
			continue
		}
		switch f[0] {
		case "use":
			if len(f) > 1 {
				uses = append(uses, f[1])
			}
		case "replace":
			work.directive("replace", f[1:])
		}
	}
	w.replace = append(w.replace, work.Replace...)
	for _, u := range uses {
		dir := filepath.Clean(filepath.Join(root, filepath.FromSlash(u)))
		w.dirs[dir] = true
		mdata, err := os.ReadFile(filepath.Join(dir, "go.mod")) //nolint:gosec // a module the repository's go.work names
		if err != nil {
			continue
		}
		mf := parseGoMod(mdata)
		for p, v := range mf.Require {
			if cur, ok := w.require[p]; !ok || compareVersions(v, cur) > 0 {
				w.require[p] = v
			}
		}
		w.replace = append(w.replace, mf.Replace...)
	}
	return w
}

type workspace struct {
	dirs    map[string]bool
	require map[string]string
	replace []modReplace
}

func unquote(s string) string {
	if u, err := strconv.Unquote(s); err == nil {
		return u
	}
	return s
}

// compareVersions orders two module versions (vMAJOR.MINOR.PATCH with an
// optional -prerelease), as semantic versioning does. Anything it cannot parse
// sorts below a version it can.
func compareVersions(a, b string) int {
	pa, okA := parseSemver(a)
	pb, okB := parseSemver(b)
	switch {
	case !okA && !okB:
		return strings.Compare(a, b)
	case !okA:
		return -1
	case !okB:
		return 1
	}
	for i := 0; i < 3; i++ {
		if pa.num[i] != pb.num[i] {
			if pa.num[i] < pb.num[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case pa.pre == pb.pre:
		return 0
	case pa.pre == "":
		return 1
	case pb.pre == "":
		return -1
	}
	ia, ib := strings.Split(pa.pre, "."), strings.Split(pb.pre, ".")
	for i := 0; i < len(ia) && i < len(ib); i++ {
		if c := comparePre(ia[i], ib[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(ia) < len(ib):
		return -1
	case len(ia) > len(ib):
		return 1
	}
	return 0
}

func comparePre(a, b string) int {
	na, errA := strconv.Atoi(a)
	nb, errB := strconv.Atoi(b)
	switch {
	case errA == nil && errB == nil:
		switch {
		case na < nb:
			return -1
		case na > nb:
			return 1
		}
		return 0
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	}
	return strings.Compare(a, b)
}

type semver struct {
	num [3]int
	pre string
}

func parseSemver(v string) (semver, bool) {
	var s semver
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		s.pre = v[i+1:]
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return s, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return s, false
		}
		s.num[i] = n
	}
	return s, true
}
