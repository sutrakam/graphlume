package langdetect

import (
	"bytes"
	"path"
	"regexp"
	"strings"
	"sync"

	"github.com/odvcencio/gotreesitter/grammars"
)

// maxShebangLine bounds how much of the first line is examined. A real `#!`
// line is a path and a flag or two; the kernel itself stops reading at 127 or
// 255 bytes depending on the system.
const maxShebangLine = 256

// shebangLanguage maps a `#!` line to a grammar, or returns "".
//
// A script with no extension was never detected: `#!/usr/bin/env bash` in a
// file named `sbt` was skipped as "no grammar for this file type". The
// interpreter table is the library's own; what is added here is the
// normalisation it lacks, so that `python3.11`, `python2.7` and
// `/usr/local/bin/ruby2.7` are found under the names the table holds.
func shebangLanguage(head []byte) string {
	interp := shebangInterpreter(head)
	if interp == "" {
		return ""
	}
	if interp == "sh" || interp == "bash" {
		// The polyglot header, after Linguist's shebang strategy: a script
		// that starts under sh only to re-run itself under its real
		// interpreter (`exec scala "$0" "$@"`) is written in that language.
		window := head
		if len(window) > 2*maxShebangLine {
			window = window[:2*maxShebangLine]
		}
		if m := execSelf.FindSubmatch(window); m != nil {
			interp = strings.ToLower(path.Base(string(m[1])))
		}
	}
	// Exact name, then without a minor version (python3.11 -> python3), then
	// without any version (python3 -> python, lua5.4 -> lua).
	major := trimMinorVersion(interp)
	for _, name := range [3]string{interp, major, strings.TrimRight(major, "0123456789")} {
		if name == "" {
			continue
		}
		if g, ok := interpreterGrammar(name); ok {
			return g
		}
	}
	return ""
}

// extraInterpreters holds interpreters the library's table has no entry for.
// An empty value is a known interpreter with no grammar: it stops the search,
// so that perl6 is not found under perl once its version is trimmed.
var extraInterpreters = map[string]string{
	"swift":   "swift",
	"kotlin":  "kotlin",
	"kotlinc": "kotlin",
	"nu":      "nushell",
	"perl6":   "",
	"raku":    "",
	"rakudo":  "",
}

// execSelf matches `exec <interpreter> ... "$0"` on a line of its own.
var execSelf = regexp.MustCompile(`(?m)^[ \t]*exec[ \t]+([\w./+-]+)[ \t][^\n]*\$0`)

const envShebang = "#!/usr/bin/env "

var (
	declaredOnce     sync.Once
	declaredInterps  map[string]string // interpreter -> grammar, from the `#!` lines grammars declare
	declaredShebangs []string          // those lines verbatim
)

// interpreterGrammar looks one interpreter name up, exactly.
//
// Exactly is the point. The library matches the `#!` lines its grammars
// declare by prefix, so `#!/usr/bin/env shx` would be read as `sh` and
// `lualatex` as `lua`. Its second table, keyed by interpreter name, is exact;
// it is consulted here only when no declared line is a prefix of the one being
// asked about, which is the only case in which the library reaches it.
func interpreterGrammar(name string) (string, bool) {
	if g, ok := extraInterpreters[name]; ok {
		return g, true
	}
	declaredOnce.Do(func() {
		declaredInterps = make(map[string]string)
		for _, e := range grammars.AllLanguages() {
			for _, s := range e.Shebangs {
				declaredShebangs = append(declaredShebangs, s)
				if interp := shebangInterpreter([]byte(s)); interp != "" {
					if _, dup := declaredInterps[interp]; !dup {
						declaredInterps[interp] = e.Name
					}
				}
			}
		}
	})
	if g, ok := declaredInterps[name]; ok {
		return g, true
	}
	line := envShebang + name
	for _, s := range declaredShebangs {
		if strings.HasPrefix(line, s) {
			return "", false
		}
	}
	if e := grammars.DetectLanguageByShebang(line); e != nil {
		return e.Name, true
	}
	return "", false
}

// shebangInterpreter returns the lower-cased base name of the program a `#!`
// line runs, looking through `env`, its flags and any VAR=value assignments:
// `#!/usr/bin/env -S deno run` is deno, `#! /bin/sh -e` is sh.
func shebangInterpreter(head []byte) string {
	if len(head) < 3 || head[0] != '#' || head[1] != '!' {
		return ""
	}
	line := head[2:]
	if len(line) > maxShebangLine {
		line = line[:maxShebangLine]
	}
	if i := bytes.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	fields := strings.Fields(string(line))
	if len(fields) == 0 {
		return ""
	}
	prog := path.Base(strings.ReplaceAll(fields[0], `\`, "/"))
	if prog == "env" {
		prog = ""
		for _, arg := range fields[1:] {
			if strings.HasPrefix(arg, "-") || strings.Contains(arg, "=") {
				continue
			}
			prog = path.Base(arg)
			break
		}
	}
	return strings.ToLower(prog)
}

// trimMinorVersion turns python3.11 into python3 and perl5.36.0 into perl5.
func trimMinorVersion(name string) string {
	i := strings.IndexByte(name, '.')
	if i <= 0 {
		return name
	}
	for _, c := range name[i+1:] {
		if (c < '0' || c > '9') && c != '.' {
			return name
		}
	}
	return name[:i]
}
