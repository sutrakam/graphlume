// Package licenses finds the licence of a dependency from what is already on
// this machine, and says where it found it.
//
// # The rules this package keeps
//
// Nothing is downloaded, no package manager is run, and no code from the
// repository or a dependency is executed. Every source is a file read with a
// size bound: a dependency's own metadata (an installed package.json, a
// lockfile's licence field, a wheel's METADATA, a crate's Cargo.toml, a POM)
// or a licence file in a local module cache. When none of those exists the
// licence is "unknown" and the reason says what was looked for.
//
// A licence is a legal fact about a package, so a guess from its name would be
// wrong in exactly the cases that matter. Nothing here guesses from a name.
// Licence TEXT is identified by a small classifier (classify.go) that compares
// normalised text against bundled canonical templates and fingerprints, and
// grades what it found exact, high, medium or low, the same words the rest of
// GraphLume grades evidence with.
package licenses

import (
	"regexp"
	"sort"
	"strings"
)

// Category groups licences by the obligation they put on a distributor.
type Category string

const (
	Permissive     Category = "permissive"
	WeakCopyleft   Category = "weak_copyleft"
	StrongCopyleft Category = "strong_copyleft"
	// Other is a valid identifier this package does not place, such as a
	// LicenseRef- or a Creative Commons content licence.
	Other   Category = "other"
	Unknown Category = "unknown"
)

// Categories lists every category in report order.
var Categories = []Category{Permissive, WeakCopyleft, StrongCopyleft, Other, Unknown}

// known maps the lower-cased SPDX identifiers this package recognises to their
// canonical spelling and category. Deprecated identifiers (GPL-2.0, LGPL-2.1)
// are kept because metadata still uses them.
var known = map[string]struct {
	id  string
	cat Category
}{}

func init() {
	add := func(cat Category, ids ...string) {
		for _, id := range ids {
			known[strings.ToLower(id)] = struct {
				id  string
				cat Category
			}{id, cat}
		}
	}
	add(Permissive,
		"MIT", "MIT-0", "X11", "ISC", "0BSD", "BSD-1-Clause", "BSD-2-Clause", "BSD-2-Clause-Patent",
		"BSD-3-Clause", "BSD-3-Clause-Clear", "BSD-4-Clause", "Apache-1.1", "Apache-2.0", "Zlib",
		"Unlicense", "CC0-1.0", "BSL-1.0", "PSF-2.0", "Python-2.0", "WTFPL", "BlueOak-1.0.0",
		"PostgreSQL", "NCSA", "UPL-1.0", "Artistic-2.0", "OpenSSL", "curl", "libpng-2.0",
		"Unicode-DFS-2016", "Unicode-3.0", "ICU", "HPND", "AFL-3.0", "MS-PL", "W3C", "JSON", "Beerware",
		"CC-BY-3.0", "CC-BY-4.0", "BSD-Source-Code", "OFL-1.1", "Zlib-acknowledgement", "bzip2-1.0.6")
	add(WeakCopyleft,
		"LGPL-2.0", "LGPL-2.0-only", "LGPL-2.0-or-later", "LGPL-2.1", "LGPL-2.1-only", "LGPL-2.1-or-later",
		"LGPL-3.0", "LGPL-3.0-only", "LGPL-3.0-or-later", "MPL-1.1", "MPL-2.0", "MPL-2.0-no-copyleft-exception",
		"EPL-1.0", "EPL-2.0", "CDDL-1.0", "CDDL-1.1", "CPL-1.0", "MS-RL", "Ms-RL", "CC-BY-SA-4.0")
	add(StrongCopyleft,
		"GPL-2.0", "GPL-2.0-only", "GPL-2.0-or-later", "GPL-2.0+", "GPL-3.0", "GPL-3.0-only",
		"GPL-3.0-or-later", "GPL-3.0+", "AGPL-3.0", "AGPL-3.0-only", "AGPL-3.0-or-later", "SSPL-1.0",
		"OSL-3.0", "EUPL-1.1", "EUPL-1.2", "RPL-1.5", "CC-BY-NC-4.0", "CC-BY-NC-SA-4.0")
}

// copyleftWithException are exceptions that turn a strong copyleft licence
// into a linking-permissive one.
var linkingExceptions = map[string]bool{
	"classpath-exception-2.0": true, "llvm-exception": true, "gcc-exception-3.1": true,
	"gcc-exception-2.0": true, "linking-exception": true, "universal-foss-exception-1.0": true,
	"openjdk-assembly-exception-1.0": true, "bison-exception-2.2": true, "autoconf-exception-3.0": true,
}

var idToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+-]*$`)

// NormalizeExpression returns the canonical spelling of an SPDX licence
// expression, or "" when s is not one this package recognises. Identifiers
// must be known or LicenseRef-/DocumentRef- references; operators are AND, OR
// and WITH, case-insensitively, with parentheses.
func NormalizeExpression(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 512 {
		return ""
	}
	// "MIT/Apache-2.0" is how old crates wrote a choice.
	if strings.Contains(s, "/") && !strings.ContainsAny(s, " ()") {
		s = strings.ReplaceAll(s, "/", " OR ")
	}
	s = strings.ReplaceAll(strings.ReplaceAll(s, "(", " ( "), ")", " ) ")
	var out []string
	depth, expectID, afterWith := 0, true, false
	for _, tok := range strings.Fields(s) {
		up := strings.ToUpper(tok)
		switch {
		case tok == "(":
			if !expectID {
				return ""
			}
			depth++
			out = append(out, "(")
		case tok == ")":
			if expectID || depth == 0 {
				return ""
			}
			depth--
			out = append(out, ")")
		case up == "AND" || up == "OR" || up == "WITH":
			if expectID {
				return ""
			}
			out = append(out, up)
			expectID, afterWith = true, up == "WITH"
		default:
			if !expectID || !idToken.MatchString(tok) {
				return ""
			}
			id := canonicalID(tok, afterWith)
			if id == "" {
				return ""
			}
			out = append(out, id)
			expectID, afterWith = false, false
		}
	}
	if expectID || depth != 0 || len(out) == 0 {
		return ""
	}
	return strings.ReplaceAll(strings.ReplaceAll(strings.Join(out, " "), "( ", "("), " )", ")")
}

func canonicalID(tok string, exception bool) string {
	low := strings.ToLower(tok)
	if exception {
		if linkingExceptions[low] || strings.HasSuffix(low, "-exception") || strings.Contains(low, "exception-") {
			return tok
		}
		return ""
	}
	if strings.HasPrefix(low, "licenseref-") || strings.HasPrefix(low, "documentref-") {
		return tok
	}
	if k, ok := known[low]; ok {
		return k.id
	}
	return ""
}

// CategoryOf places an expression: OR takes the least restrictive choice, AND
// the most restrictive, and a strong copyleft licence WITH a linking exception
// counts as weak copyleft.
func CategoryOf(expr string) Category {
	if expr == "" || expr == "unknown" {
		return Unknown
	}
	toks := strings.Fields(strings.ReplaceAll(strings.ReplaceAll(expr, "(", " ( "), ")", " ) "))
	pos := 0
	var parseOr func() Category
	parseAtom := func() Category {
		if pos >= len(toks) {
			return Unknown
		}
		t := toks[pos]
		pos++
		var c Category
		if t == "(" {
			c = parseOr()
			if pos < len(toks) && toks[pos] == ")" {
				pos++
			}
		} else if k, ok := known[strings.ToLower(t)]; ok {
			c = k.cat
		} else {
			c = Other
		}
		if pos+1 < len(toks) && toks[pos] == "WITH" {
			exc := strings.ToLower(toks[pos+1])
			pos += 2
			if c == StrongCopyleft && linkingExceptions[exc] {
				c = WeakCopyleft
			}
		}
		return c
	}
	parseAnd := func() Category {
		c := parseAtom()
		for pos < len(toks) && toks[pos] == "AND" {
			pos++
			c = stricter(c, parseAtom())
		}
		return c
	}
	parseOr = func() Category {
		c := parseAnd()
		for pos < len(toks) && toks[pos] == "OR" {
			pos++
			c = looser(c, parseAnd())
		}
		return c
	}
	return parseOr()
}

// rank orders categories by obligation. Other sits between permissive and
// copyleft: it is a licence, but not one this package can place.
func rank(c Category) int {
	switch c {
	case Permissive:
		return 0
	case Other:
		return 1
	case WeakCopyleft:
		return 2
	case StrongCopyleft:
		return 3
	}
	return 4
}

func stricter(a, b Category) Category {
	if rank(a) >= rank(b) {
		return a
	}
	return b
}

func looser(a, b Category) Category {
	if rank(a) <= rank(b) {
		return a
	}
	return b
}

// joinIDs combines distinct expressions with op, sorted so the result is stable.
func joinIDs(ids []string, op string) string {
	seen := map[string]bool{}
	var uniq []string
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	sort.Strings(uniq)
	if len(uniq) == 1 {
		return uniq[0]
	}
	for i, id := range uniq {
		if strings.ContainsAny(id, " ") {
			uniq[i] = "(" + id + ")"
		}
	}
	return strings.Join(uniq, " "+op+" ")
}

// licenseNames maps the free-text names metadata uses (POM <name>, a wheel's
// License: field, a Python trove classifier) to SPDX identifiers. Keys are
// lower-case words with punctuation removed. A name that does not say which
// version or variant it means ("BSD License", "GPL") is deliberately absent.
var licenseNames = map[string]string{
	"mit": "MIT", "mit license": "MIT", "the mit license": "MIT", "mit licence": "MIT", "expat": "MIT",
	"mit no attribution license": "MIT-0",
	"apache 2":                   "Apache-2.0", "apache 2 0": "Apache-2.0", "apache2": "Apache-2.0", "apache license 2 0": "Apache-2.0",
	"apache license version 2 0": "Apache-2.0", "the apache license version 2 0": "Apache-2.0",
	"the apache software license version 2 0": "Apache-2.0", "apache software license version 2 0": "Apache-2.0",
	"apache software license 2 0": "Apache-2.0", "asl 2 0": "Apache-2.0", "al 2 0": "Apache-2.0",
	"bsd 2 clause": "BSD-2-Clause", "simplified bsd": "BSD-2-Clause", "freebsd": "BSD-2-Clause",
	"bsd 2 clause license": "BSD-2-Clause", "the bsd 2 clause license": "BSD-2-Clause",
	"bsd 3 clause": "BSD-3-Clause", "new bsd": "BSD-3-Clause", "new bsd license": "BSD-3-Clause",
	"bsd 3 clause license": "BSD-3-Clause", "the bsd 3 clause license": "BSD-3-Clause",
	"revised bsd license": "BSD-3-Clause", "modified bsd license": "BSD-3-Clause",
	"eclipse distribution license v 1 0": "BSD-3-Clause", "edl 1 0": "BSD-3-Clause",
	"isc": "ISC", "isc license": "ISC", "isc license iscl": "ISC", "iscl": "ISC",
	"mozilla public license 2 0": "MPL-2.0", "mozilla public license version 2 0": "MPL-2.0",
	"mozilla public license 2 0 mpl 2 0": "MPL-2.0", "mpl 2 0": "MPL-2.0",
	"eclipse public license 2 0": "EPL-2.0", "eclipse public license v2 0": "EPL-2.0",
	"eclipse public license v 2 0": "EPL-2.0", "eclipse public license 2 0 epl 2 0": "EPL-2.0", "epl 2 0": "EPL-2.0",
	"eclipse public license 1 0": "EPL-1.0", "eclipse public license v1 0": "EPL-1.0",
	"eclipse public license v 1 0": "EPL-1.0", "epl 1 0": "EPL-1.0",
	"gnu general public license v2 gplv2": "GPL-2.0-only", "gnu general public license v2 or later gplv2": "GPL-2.0-or-later",
	"gnu general public license v3 gplv3": "GPL-3.0-only", "gnu general public license v3 or later gplv3": "GPL-3.0-or-later",
	"gplv2": "GPL-2.0-only", "gplv3": "GPL-3.0-only", "gpl 2 0": "GPL-2.0-only", "gpl 3 0": "GPL-3.0-only",
	"gnu general public license version 2": "GPL-2.0-only", "gnu general public license version 3": "GPL-3.0-only",
	"gnu lesser general public license v2 lgplv2": "LGPL-2.0-only", "gnu lesser general public license v2 or later lgplv2": "LGPL-2.0-or-later",
	"gnu lesser general public license v3 lgplv3": "LGPL-3.0-only", "gnu lesser general public license v3 or later lgplv3": "LGPL-3.0-or-later",
	"lgplv3": "LGPL-3.0-only", "lgpl 2 1": "LGPL-2.1-only", "lgpl 3 0": "LGPL-3.0-only",
	"gnu lesser general public license version 2 1": "LGPL-2.1-only", "gnu lesser general public license version 3": "LGPL-3.0-only",
	"gnu affero general public license v3": "AGPL-3.0-only", "gnu affero general public license v3 or later agplv3": "AGPL-3.0-or-later",
	"agplv3": "AGPL-3.0-only", "agpl 3 0": "AGPL-3.0-only",
	"python software foundation license": "PSF-2.0", "psf": "PSF-2.0", "psf license": "PSF-2.0",
	"the unlicense unlicense": "Unlicense", "the unlicense": "Unlicense", "unlicense": "Unlicense",
	"zlib libpng license": "Zlib", "zlib license": "Zlib", "zlib": "Zlib",
	"boost software license 1 0 bsl 1 0": "BSL-1.0", "boost software license 1 0": "BSL-1.0",
	"cc0 1 0 universal cc0 1 0 public domain dedication": "CC0-1.0", "cc0": "CC0-1.0", "cc0 1 0": "CC0-1.0",
	"universal permissive license upl": "UPL-1.0", "the universal permissive license upl version 1 0": "UPL-1.0",
	"common development and distribution license 1 0 cddl 1 0": "CDDL-1.0", "cddl 1 0": "CDDL-1.0", "cddl 1 1": "CDDL-1.1",
	"bouncy castle licence": "MIT", "bouncy castle license": "MIT",
	"european union public licence 1 2 eupl 1 2": "EUPL-1.2",
	"artistic license 2 0":                       "Artistic-2.0",
	"mit x11":                                    "MIT",
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// FromName maps a free-text licence name to an SPDX expression, or "".
func FromName(name string) string {
	if e := NormalizeExpression(name); e != "" {
		return e
	}
	key := strings.TrimSpace(nonWord.ReplaceAllString(strings.ToLower(name), " "))
	if id, ok := licenseNames[key]; ok {
		return id
	}
	return ""
}

// licenseURL maps a licence URL to an SPDX identifier.
var licenseURL = regexp.MustCompile(`(?i)(?:opensource\.org/licenses?|spdx\.org/licenses)/([A-Za-z0-9.+-]+?)(?:\.html|\.php|/)?$`)

// FromURL maps the URL a POM gives for a licence to an SPDX identifier.
func FromURL(u string) string {
	u = strings.TrimSpace(u)
	low := strings.ToLower(u)
	switch {
	case strings.Contains(low, "apache.org/licenses/license-2.0"):
		return "Apache-2.0"
	case strings.Contains(low, "eclipse.org/legal/epl-2.0") || strings.Contains(low, "eclipse.org/legal/epl-v20"):
		return "EPL-2.0"
	case strings.Contains(low, "eclipse.org/legal/epl-v10"):
		return "EPL-1.0"
	case strings.Contains(low, "mozilla.org/mpl/2.0"):
		return "MPL-2.0"
	case strings.Contains(low, "gnu.org/licenses/lgpl-2.1"):
		return "LGPL-2.1-only"
	case strings.Contains(low, "gnu.org/licenses/lgpl"):
		return "LGPL-3.0-only"
	case strings.Contains(low, "gnu.org/licenses/agpl"):
		return "AGPL-3.0-only"
	case strings.Contains(low, "gnu.org/licenses/gpl-2.0") || strings.Contains(low, "gnu.org/licenses/old-licenses/gpl-2.0"):
		return "GPL-2.0-only"
	case strings.Contains(low, "gnu.org/licenses/gpl"):
		return "GPL-3.0-only"
	}
	if m := licenseURL.FindStringSubmatch(u); m != nil {
		id := m[1]
		switch strings.ToLower(id) {
		case "bsd-license":
			return "BSD-2-Clause"
		case "mit-license":
			return "MIT"
		case "apache2.0":
			return "Apache-2.0"
		}
		if k, ok := known[strings.ToLower(id)]; ok {
			return k.id
		}
	}
	return ""
}
