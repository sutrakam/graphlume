package licenses

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// SourceFirstParty is a dependency that is part of this repository: a Go
// module replaced by a directory inside it, or an npm package linked from one.
// Its licence is the repository's own, read from a licence file in its
// directory or the nearest one above it up to the repository root, or from the
// repository's declared licence. No cache holds such a module, so looking for
// one there could only ever answer "unknown".
const SourceFirstParty = "first-party"

// localGoReplacement returns the directory a go.mod's replace directive maps
// module to, when that directory is inside the repository.
//
// Both forms are read: `replace m => ./dir` and `replace m v1.2.3 => ../dir`,
// on one line or in a `replace ( ... )` block. A replacement by another module
// path (no leading . or ..) is not first-party and is left to the cache.
func (r *resolver) localGoReplacement(manifest, module string) (string, bool) {
	if manifest == "" {
		return "", false
	}
	gomod := filepath.Join(r.env.Root, filepath.FromSlash(manifest))
	b, err := readBounded(gomod, maxMetaBytes)
	if err != nil {
		return "", false
	}
	inBlock := false
	for _, raw := range strings.Split(string(b), "\n") {
		line := raw
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "replace ("), line == "replace(":
			inBlock = true
			continue
		case inBlock && line == ")":
			inBlock = false
			continue
		case strings.HasPrefix(line, "replace "):
			line = strings.TrimSpace(strings.TrimPrefix(line, "replace "))
		case !inBlock:
			continue
		}
		left, right, ok := strings.Cut(line, "=>")
		if !ok {
			continue
		}
		lf, rf := strings.Fields(left), strings.Fields(right)
		if len(lf) == 0 || len(rf) == 0 || strings.Trim(lf[0], `"`) != module {
			continue
		}
		target := strings.Trim(rf[0], `"`)
		if !strings.HasPrefix(target, "./") && !strings.HasPrefix(target, "../") && target != "." && target != ".." {
			continue
		}
		dir := filepath.Join(filepath.Dir(gomod), filepath.FromSlash(target))
		if real, ok := r.inRepo(dir); ok && isDir(real) {
			return real, true
		}
	}
	return "", false
}

// localNPMPath returns the directory of an npm dependency declared by a local
// path (file:, link:) when it is inside the repository.
func (r *resolver) localNPMPath(manifest, version string) (string, bool) {
	spec := strings.TrimSpace(version)
	var rel string
	for _, p := range []string{"file:", "link:"} {
		if v, ok := strings.CutPrefix(spec, p); ok {
			rel = v
		}
	}
	if rel == "" || filepath.IsAbs(rel) {
		return "", false
	}
	dir := filepath.Join(r.env.Root, filepath.Dir(filepath.FromSlash(manifest)), filepath.FromSlash(rel))
	if real, ok := r.inRepo(dir); ok && isDir(real) {
		return real, true
	}
	return "", false
}

// firstParty is the licence of a module that lives in this repository at dir:
// a licence file in dir or the nearest directory above it, up to the
// repository root, then the repository's declared licence (the root
// package.json or Cargo.toml). how says why the module is first-party, for the
// row's reason when no licence is found.
func (r *resolver) firstParty(dir, how string) Result {
	root := r.realRoot
	for d, i := dir, 0; i < 32; i++ {
		if res, ok := r.fromLicenseFiles(d); ok && res.Source != SourceUnknown {
			res.Source = SourceFirstParty
			return res
		}
		rel, err := filepath.Rel(root, d)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			break
		}
		d = filepath.Dir(d)
	}
	if res, ok := r.repositoryDeclared(dir); ok {
		res.Source = SourceFirstParty
		return res
	}
	return Result{
		License: "unknown", Source: SourceFirstParty,
		Reason: how + ": it is part of this repository, and neither its directory nor the repository has a " +
			"LICENSE, LICENCE or COPYING file or a declared licence",
	}
}

// repositoryDeclared reads a licence declared in package.json or Cargo.toml in
// dir or the repository root.
func (r *resolver) repositoryDeclared(dir string) (Result, bool) {
	for _, d := range []string{dir, r.realRoot} {
		pj := filepath.Join(d, "package.json")
		if b, err := readBounded(pj, maxMetaBytes); err == nil {
			var meta struct {
				License any `json:"license"`
			}
			if json.Unmarshal(b, &meta) == nil {
				if s, ok := meta.License.(string); ok {
					if res, ok := r.declared(s, pj); ok {
						return res, true
					}
				}
			}
		}
		ct := filepath.Join(d, "Cargo.toml")
		if b, err := readBounded(ct, maxMetaBytes); err == nil {
			if lic, _ := cargoLicense(string(b)); lic != "" {
				if res, ok := r.declared(lic, ct); ok {
					return res, true
				}
			}
		}
	}
	return Result{}, false
}

// goFirstParty answers for a Go module replaced by a directory in the repository.
func (r *resolver) goFirstParty(d Dependency) (Result, bool) {
	dir, ok := r.localGoReplacement(d.Manifest, d.Name)
	if !ok {
		return Result{}, false
	}
	return r.firstParty(dir, fmt.Sprintf("%s is replaced by %s in %s", d.Name, r.display(dir), d.Manifest)), true
}
