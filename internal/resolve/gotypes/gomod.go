package gotypes

import (
	"strconv"
	"strings"
)

// modFile is the part of a go.mod this package needs: the module's own path,
// its language version, what it requires and what it replaces. It is a reader
// and nothing else: no semantic versions are compared and no graph is walked,
// because the version of a dependency is whatever the main module's go.mod
// says, and that is a lookup, not a computation.
type modFile struct {
	Path      string
	GoVersion string
	Require   map[string]string
	Replace   []modReplace
}

type modReplace struct {
	Old, OldVersion string
	New, NewVersion string
}

// parseGoMod reads the directives that matter from go.mod text. Unknown
// directives and malformed lines are ignored: a go.mod this cannot read
// degrades to "no dependencies found", which turns every dependency import
// into a stub, never into a wrong answer.
func parseGoMod(data []byte) modFile {
	mf := modFile{Require: map[string]string{}}
	block := ""
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
			mf.directive(block, fields(line))
			continue
		}
		f := fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) >= 2 && f[1] == "(" {
			block = f[0]
			continue
		}
		mf.directive(f[0], f[1:])
	}
	return mf
}

func (mf *modFile) directive(verb string, args []string) {
	switch verb {
	case "module":
		if len(args) > 0 {
			mf.Path = args[0]
		}
	case "go":
		if len(args) > 0 {
			mf.GoVersion = args[0]
		}
	case "require":
		if len(args) >= 2 {
			mf.Require[args[0]] = args[1]
		}
	case "replace":
		arrow := -1
		for i, a := range args {
			if a == "=>" {
				arrow = i
			}
		}
		if arrow < 1 || arrow >= len(args)-1 {
			return
		}
		r := modReplace{Old: args[0], New: args[arrow+1]}
		if arrow >= 2 {
			r.OldVersion = args[1]
		}
		if len(args) > arrow+2 {
			r.NewVersion = args[arrow+2]
		}
		mf.Replace = append(mf.Replace, r)
	}
}

// fields splits a go.mod line on spaces, unquoting "..." and `...` words.
func fields(line string) []string {
	var out []string
	for _, w := range strings.Fields(line) {
		if len(w) >= 2 && (w[0] == '"' || w[0] == '`') {
			if u, err := strconv.Unquote(w); err == nil {
				w = u
			}
		}
		out = append(out, w)
	}
	return out
}

// escapePath applies the module cache's case-escaping: an upper-case letter is
// written as '!' and its lower-case form, so that case-insensitive file
// systems do not conflate two modules.
func escapePath(p string) string {
	var b strings.Builder
	for _, r := range p {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
