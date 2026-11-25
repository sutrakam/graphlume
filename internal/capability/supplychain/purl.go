package supplychain

import (
	"strings"

	"github.com/sutrakam/graphlume/internal/analyze/deps"
)

// PURL builds a package URL (https://github.com/package-url/purl-spec) for a
// package, with the version qualifier only when version is non-empty.
//
// A range is never written into a purl. `pkg:npm/lodash@^4.17.20` is not a
// package URL — the version component names one release — and a consumer that
// parses it would look up a release that does not exist and report no
// findings. A dependency without a concrete version gets a version-less purl,
// which says exactly what is known: which package, not which release.
//
// Returns "" when the name cannot be expressed in the ecosystem's purl type.
func PURL(eco deps.Ecosystem, name, version string) string {
	var b strings.Builder
	switch eco {
	case deps.EcosystemGo:
		// The module path keeps its case. The purl specification's golang type
		// asks for lowercase, but Go module paths are case-sensitive
		// (github.com/BurntSushi/toml is not github.com/burntsushi/toml), and
		// the tools that consume these — cyclonedx-gomod, syft, OSV — all keep
		// the case the module declares.
		b.WriteString("pkg:golang/")
		b.WriteString(purlPath(name))
	case deps.EcosystemNPM:
		b.WriteString("pkg:npm/")
		b.WriteString(purlPath(strings.ToLower(name)))
	case deps.EcosystemPyPI:
		// The pypi type lowercases the name and writes "_" as "-".
		b.WriteString("pkg:pypi/")
		b.WriteString(purlEscape(strings.ReplaceAll(strings.ToLower(name), "_", "-")))
	case deps.EcosystemCargo:
		b.WriteString("pkg:cargo/")
		b.WriteString(purlEscape(name))
	case deps.EcosystemMaven, deps.EcosystemGradle:
		// Gradle resolves from Maven repositories; both are the maven type.
		group, artifact, ok := strings.Cut(name, ":")
		if !ok || group == "" || artifact == "" {
			return ""
		}
		b.WriteString("pkg:maven/")
		b.WriteString(purlEscape(group))
		b.WriteByte('/')
		b.WriteString(purlEscape(artifact))
	default:
		return ""
	}
	if version != "" {
		b.WriteByte('@')
		b.WriteString(purlEscape(version))
	}
	return b.String()
}

// purlPath escapes each segment of a slash-separated name, keeping the slashes.
func purlPath(name string) string {
	segments := strings.Split(name, "/")
	for i, s := range segments {
		segments[i] = purlEscape(s)
	}
	return strings.Join(segments, "/")
}

// purlEscape percent-encodes everything outside the unreserved set. "@" (an
// npm scope), "+" (Go's +incompatible) and ":" must not survive literally: each
// has a meaning in the purl grammar.
func purlEscape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '.' || c == '-' || c == '_' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}
