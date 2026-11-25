package supplychain

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Lockfiles are read for ONE thing: the version of a dependency the manifest
// already declares. They are never a source of dependencies — see the package
// comment of internal/analyze/deps for why a lockfile's transitive closure is
// not what a project "depends on". The consequence is stated in every report:
// a vulnerable package that appears only in a lockfile is not examined here.
//
// Each reader returns "" when it cannot name exactly one version. Two versions
// of one crate in Cargo.lock, a Git commit where a version should be, an entry
// that is missing: all of those leave the dependency a range, which is true.

// ---------------------------------------------------------------- npm

type npmLock struct {
	Packages map[string]struct {
		Version string `json:"version"`
	} `json:"packages"`
	Dependencies map[string]struct {
		Version string `json:"version"`
	} `json:"dependencies"`
}

var concreteSemver = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

// npmLocked finds the resolved version of a package.json dependency. It
// returns the version and the lockfile it came from.
func (r *Resolver) npmLocked(manifest, name, spec string) (version, lockfile string) {
	dirs := ancestors(manifest)
	manifestDir := dirs[0]
	for _, dir := range dirs {
		for _, file := range []string{"package-lock.json", "npm-shrinkwrap.json"} {
			lockPath := joinRel(dir, file)
			lock := r.npmLock(lockPath)
			if lock == nil {
				continue
			}
			// The lockfile's own directory is the install root. A workspace
			// member's dependency is recorded under the member's path when it
			// is not hoisted, and at the top level when it is.
			member := strings.TrimPrefix(strings.TrimPrefix(manifestDir, dir), "/")
			var keys []string
			if member != "" {
				keys = append(keys, member+"/node_modules/"+name)
			}
			keys = append(keys, "node_modules/"+name)
			for _, k := range keys {
				if p, ok := lock.Packages[k]; ok && concreteSemver.MatchString(p.Version) {
					return p.Version, lockPath
				}
			}
			// lockfileVersion 1 has no "packages" map.
			if member == "" {
				if p, ok := lock.Dependencies[name]; ok && concreteSemver.MatchString(p.Version) {
					return p.Version, lockPath
				}
			}
		}
		yarnPath := joinRel(dir, "yarn.lock")
		if entries := r.yarnLock(yarnPath); entries != nil {
			if v, ok := entries[name+"@"+spec]; ok && concreteSemver.MatchString(v) {
				return v, yarnPath
			}
		}
	}
	return "", ""
}

func (r *Resolver) npmLock(lockPath string) *npmLock {
	if lock, ok := r.npmLocks[lockPath]; ok {
		return lock
	}
	var lock *npmLock
	if src, ok := r.file(lockPath); ok {
		var parsed npmLock
		if json.Unmarshal([]byte(src), &parsed) == nil {
			lock = &parsed
		}
	}
	r.npmLocks[lockPath] = lock
	return lock
}

func (r *Resolver) yarnLock(lockPath string) map[string]string {
	if entries, ok := r.yarnLocks[lockPath]; ok {
		return entries
	}
	var entries map[string]string
	if src, ok := r.file(lockPath); ok {
		entries = parseYarnLock(src)
	}
	r.yarnLocks[lockPath] = entries
	return entries
}

// parseYarnLock maps "name@range" to the resolved version, for both the
// classic format and the YAML one Yarn 2+ writes:
//
//	"@scope/pkg@^1.0.0", "@scope/pkg@^1.2.0":      "@scope/pkg@npm:^1.0.0, @scope/pkg@npm:^1.2.0":
//	  version "1.2.3"                                version: 1.2.3
//
// A line parser rather than a YAML one because the classic format is not YAML,
// and the two differ only in punctuation at the two lines that matter.
func parseYarnLock(src string) map[string]string {
	out := map[string]string{}
	var current []string
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			current = current[:0]
			header := strings.TrimSuffix(strings.TrimSpace(line), ":")
			header = strings.ReplaceAll(header, `"`, "")
			for _, spec := range strings.Split(header, ",") {
				spec = strings.TrimSpace(spec)
				// The name may itself start with "@"; the separator is the
				// first "@" after position 0.
				at := strings.Index(spec[min(1, len(spec)):], "@")
				if at < 0 {
					continue
				}
				name, rng := spec[:at+1], spec[at+2:]
				current = append(current, name+"@"+strings.TrimPrefix(rng, "npm:"))
			}
			continue
		}
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) == 2 && (fields[0] == "version" || fields[0] == "version:") {
			v := strings.Trim(fields[1], `"`)
			for _, key := range current {
				out[key] = v
			}
		}
	}
	return out
}

// ---------------------------------------------------------------- TOML locks

// parsePackageLock reads the `[[package]]` tables shared by Cargo.lock,
// poetry.lock and uv.lock, returning every version recorded for each name.
func parsePackageLock(src string, normalize func(string) string) map[string][]string {
	out := map[string][]string{}
	inPackage := false
	name, version := "", ""
	flush := func() {
		if inPackage && name != "" && version != "" {
			key := normalize(name)
			if !containsString(out[key], version) {
				out[key] = append(out[key], version)
			}
		}
		name, version = "", ""
	}
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			flush()
			inPackage = line == "[[package]]"
			continue
		}
		if !inPackage {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "name":
			name = value
		case "version":
			version = value
		}
	}
	flush()
	return out
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func (r *Resolver) tomlLocked(manifest, name string, files []string, normalize func(string) string) (version, lockfile string) {
	for _, dir := range ancestors(manifest) {
		for _, file := range files {
			lockPath := joinRel(dir, file)
			entries, ok := r.tomlLocks[lockPath]
			if !ok {
				if src, found := r.file(lockPath); found {
					entries = parsePackageLock(src, normalize)
				}
				r.tomlLocks[lockPath] = entries
			}
			// Exactly one version, or no answer: two versions of one package
			// in a lockfile means two requirements resolved differently, and
			// which one this manifest's line produced is not recorded here.
			if versions := entries[normalize(name)]; len(versions) == 1 {
				return versions[0], lockPath
			}
		}
	}
	return "", ""
}

// ---------------------------------------------------------------- Gradle

// parseGradleLock reads gradle.lockfile: one `group:artifact:version=configurations`
// per line.
func parseGradleLock(src string) map[string][]string {
	out := map[string][]string{}
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		coord, _, _ := strings.Cut(line, "=")
		parts := strings.Split(coord, ":")
		if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
			continue
		}
		key := parts[0] + ":" + parts[1]
		if !containsString(out[key], parts[2]) {
			out[key] = append(out[key], parts[2])
		}
	}
	return out
}

func (r *Resolver) gradleLocked(manifest, name string) (version, lockfile string) {
	for _, dir := range ancestors(manifest) {
		lockPath := joinRel(dir, "gradle.lockfile")
		entries, ok := r.gradle[lockPath]
		if !ok {
			if src, found := r.file(lockPath); found {
				entries = parseGradleLock(src)
			}
			r.gradle[lockPath] = entries
		}
		if versions := entries[name]; len(versions) == 1 {
			return versions[0], lockPath
		}
	}
	return "", ""
}
