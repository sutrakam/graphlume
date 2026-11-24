package licenses

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// anyLicense is a package.json licence: a string, or the old {"type": ...}.
type anyLicense string

func (a *anyLicense) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*a = anyLicense(s)
		return nil
	}
	var o struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(b, &o) == nil {
		*a = anyLicense(o.Type)
	}
	return nil
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// ---------------------------------------------------------------- Maven

type pom struct {
	Licenses []struct {
		Name string `xml:"name"`
		URL  string `xml:"url"`
	} `xml:"licenses>license"`
	Parent struct {
		GroupID    string `xml:"groupId"`
		ArtifactID string `xml:"artifactId"`
		Version    string `xml:"version"`
	} `xml:"parent"`
}

func parsePOM(b []byte) (pom, error) {
	var p pom
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	dec.AutoClose = xml.HTMLAutoClose
	err := dec.Decode(&p)
	return p, err
}

// maxParentDepth bounds how far up a POM's parents a licence is looked for.
const maxParentDepth = 4

func (r *resolver) maven(d Dependency) Result {
	group, artifact, ok := strings.Cut(d.Name, ":")
	if !ok || group == "" || artifact == "" || strings.Contains(d.Name, "..") || strings.ContainsAny(d.Name, `/\`) {
		return unknown("not a groupId:artifactId coordinate")
	}
	version, note := strings.TrimSpace(d.Version), ""
	if version == "" || strings.ContainsAny(version, "$[(,") {
		vs := r.cachedVersions(group, artifact)
		if len(vs) != 1 {
			if len(vs) == 0 {
				return unknown("the version is not declared here (a BOM or parent supplies it) and no copy is in the local Maven or Gradle cache")
			}
			return unknown(fmt.Sprintf("the version is not declared here and the local caches hold %d versions, so which one applies cannot be told", len(vs)))
		}
		note = fmt.Sprintf("the version is not declared here; read %s, the only cached version", vs[0])
		version = vs[0]
	}
	path := r.pomPath(group, artifact, version)
	if path == "" {
		return unknown(fmt.Sprintf("%s:%s is not in the local Maven repository (%s) or Gradle cache", d.Name, version, r.display(r.env.MavenRepo)))
	}
	res := r.pomLicense(path, 0)
	if note != "" && res.Source != SourceUnknown {
		res.Confidence = downgrade(res.Confidence)
		res.Reason = note
	}
	return res
}

func (r *resolver) pomLicense(path string, depth int) Result {
	b, err := readBounded(path, maxMetaBytes)
	if err != nil {
		return unknown("the cached POM could not be read")
	}
	p, err := parsePOM(b)
	if err != nil {
		return unknown("the cached POM is not well-formed XML")
	}
	if len(p.Licenses) > 0 {
		var ids, names []string
		conf := Exact
		for _, l := range p.Licenses {
			name := strings.TrimSpace(l.Name)
			names = append(names, name)
			if e := NormalizeExpression(name); e != "" {
				ids = append(ids, e)
				continue
			}
			id := FromName(name)
			if id == "" {
				id = FromURL(l.URL)
			}
			if id == "" {
				ids = nil
				break
			}
			ids = append(ids, id)
			conf = High
		}
		declared := strings.Join(names, ", ")
		if len(ids) == 0 {
			return Result{License: "unknown", Source: SourceUnknown, Path: r.display(path), Declared: declared,
				Reason: fmt.Sprintf("the POM declares %q, which is not a licence name or URL this report recognises", declared)}
		}
		res := Result{License: joinIDs(ids, "OR"), Source: SourceDeclared, Path: r.display(path), Confidence: conf}
		if conf != Exact {
			res.Declared = declared
		}
		return res
	}
	pg, pa, pv := strings.TrimSpace(p.Parent.GroupID), strings.TrimSpace(p.Parent.ArtifactID), strings.TrimSpace(p.Parent.Version)
	if depth < maxParentDepth && pg != "" && pa != "" && pv != "" && !strings.Contains(pv, "$") &&
		!strings.Contains(pg+pa+pv, "..") && !strings.ContainsAny(pg+pa+pv, `/\`) {
		if pp := r.pomPath(pg, pa, pv); pp != "" {
			return r.pomLicense(pp, depth+1)
		}
		return unknown("the POM declares no licence and its parent POM is not in the local cache")
	}
	return unknown("the POM declares no licence")
}

// pomPath finds a POM in the Maven local repository or the Gradle cache.
func (r *resolver) pomPath(group, artifact, version string) string {
	file := artifact + "-" + version + ".pom"
	if r.env.MavenRepo != "" {
		p := filepath.Join(r.env.MavenRepo, filepath.FromSlash(strings.ReplaceAll(group, ".", "/")), artifact, version, file)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if r.env.GradleHome != "" {
		dir := filepath.Join(r.env.GradleHome, "caches", "modules-2", "files-2.1", group, artifact, version)
		ents, err := os.ReadDir(dir)
		if err == nil {
			for _, e := range ents {
				p := filepath.Join(dir, e.Name(), file)
				if _, err := os.Stat(p); err == nil {
					return p
				}
			}
		}
	}
	return ""
}

func (r *resolver) cachedVersions(group, artifact string) []string {
	seen := map[string]bool{}
	var out []string
	var dirs []string
	if r.env.MavenRepo != "" {
		dirs = append(dirs, filepath.Join(r.env.MavenRepo, filepath.FromSlash(strings.ReplaceAll(group, ".", "/")), artifact))
	}
	if r.env.GradleHome != "" {
		dirs = append(dirs, filepath.Join(r.env.GradleHome, "caches", "modules-2", "files-2.1", group, artifact))
	}
	for _, dir := range dirs {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() && !seen[e.Name()] {
				seen[e.Name()] = true
				out = append(out, e.Name())
			}
		}
	}
	return out
}
