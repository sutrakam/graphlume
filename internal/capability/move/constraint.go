package move

import (
	"go/build/constraint"
	"path"
	"strings"
)

// A file is compiled only when its build constraint holds. Two files with
// different constraints are compiled in different builds, so a declaration
// moved between them would appear in builds it was absent from, or vanish
// from builds that need it — and the one build this machine can run would
// not show it. The move is allowed only between files built under the same
// conditions: the same //go:build expression and the same constraint read
// from the file name.

// constraintOf returns a file's whole build constraint in canonical form, ""
// when it is always built.
func constraintOf(p *parsed) (string, error) {
	expr, err := goBuildLine(p)
	if err != nil {
		return "", err
	}
	c := nameConstraint(path.Base(p.path))
	if expr != "" {
		if c != "" {
			c += ", "
		}
		c += "//go:build " + expr
	}
	return c, nil
}

// goBuildLine returns the expression of the file's //go:build line, "" when
// it has none. A file constrained only by the older "// +build" form is
// refused: those lines count or not depending on the blank lines around
// them, and gofmt has written the unambiguous form beside them since Go 1.17.
func goBuildLine(p *parsed) (string, error) {
	var expr, legacy string
	for _, cg := range p.ast.Comments {
		if cg.Pos() >= p.ast.Package {
			break
		}
		for _, c := range cg.List {
			switch {
			case constraint.IsGoBuild(c.Text):
				x, err := constraint.Parse(c.Text)
				if err != nil {
					return "", refuse(p.path, p.line(c.Pos()), "its //go:build line does not parse (%v)", err)
				}
				if expr != "" {
					return "", refuse(p.path, p.line(c.Pos()), "it has more than one //go:build line")
				}
				expr = x.String()
			case constraint.IsPlusBuild(c.Text):
				legacy = c.Text
			}
		}
	}
	if expr == "" && legacy != "" {
		return "", refuse(p.path, 0, "it is constrained by an old-style %q line with no //go:build line beside it; "+
			"run gofmt on it (Go 1.17 or later) so the constraint can be read exactly", legacy)
	}
	return expr, nil
}

// describeConstraint renders a canonical constraint for a message.
func describeConstraint(c string) string {
	if c == "" {
		return "in every build"
	}
	return "only under " + c
}

// nameConstraint returns the constraint the go command reads from a file
// name, in a canonical spelling: "" for none. It follows go/build exactly: a
// name ending in _test.go is a test file; then, of the name up to its first
// dot, only what comes after the first underscore counts, a final _test is
// set aside, and the last element is an operating system or an architecture,
// or the last two are an operating system followed by an architecture.
func nameConstraint(base string) string {
	var parts []string
	if strings.HasSuffix(base, "_test.go") {
		parts = append(parts, "a test build")
	}
	name, _, _ := strings.Cut(base, ".")
	if i := strings.IndexByte(name, '_'); i >= 0 {
		l := strings.Split(name[i:], "_")
		if n := len(l); n > 0 && l[n-1] == "test" {
			l = l[:n-1]
		}
		switch n := len(l); {
		case n >= 2 && knownOS[l[n-2]] && knownArch[l[n-1]]:
			parts = append(parts, "GOOS="+l[n-2], "GOARCH="+l[n-1])
		case n >= 1 && knownOS[l[n-1]]:
			parts = append(parts, "GOOS="+l[n-1])
		case n >= 1 && knownArch[l[n-1]]:
			parts = append(parts, "GOARCH="+l[n-1])
		}
	}
	return strings.Join(parts, ", ")
}

// knownOS and knownArch are go/build's lists (internal/syslist), which it
// does not export. They only ever grow, and a value missing here makes a file
// name look unconstrained to this package — so TestKnownPlatformsMatchGoBuild
// checks every entry, and the running platform, against go/build itself.
var knownOS = map[string]bool{
	"aix": true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true, "hurd": true,
	"illumos": true, "ios": true, "js": true, "linux": true, "nacl": true, "netbsd": true, "openbsd": true,
	"plan9": true, "solaris": true, "wasip1": true, "windows": true, "zos": true,
}

var knownArch = map[string]bool{
	"386": true, "amd64": true, "amd64p32": true, "arm": true, "armbe": true, "arm64": true, "arm64be": true,
	"loong64": true, "mips": true, "mipsle": true, "mips64": true, "mips64le": true, "mips64p32": true,
	"mips64p32le": true, "ppc": true, "ppc64": true, "ppc64le": true, "riscv": true, "riscv64": true,
	"s390": true, "s390x": true, "sparc": true, "sparc64": true, "wasm": true,
}
