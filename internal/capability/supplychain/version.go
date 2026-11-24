package supplychain

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/sutrakam/graphlume/internal/analyze/deps"
)

// Version ordering, one scheme per ecosystem.
//
// Whether a dependency falls inside an advisory's range is decided by ordering
// two version strings, and every ecosystem orders them differently: semver puts
// 1.0.0-rc.1 before 1.0.0, PEP 440 puts 1.0.post1 after it and 1.0.dev1 before
// 1.0a1, Maven treats "1.0-sp" as newer than "1.0" and "1.0-foo" as newer than
// both. Comparing with the wrong scheme does not fail, it answers — and the
// answer that comes out wrong is "not affected".
//
// So every comparison returns an error when it cannot be made, and the caller
// turns that error into "could not compare", never into a verdict. There is no
// fallback to string comparison anywhere in this file.

// ErrNoScheme is returned for an ecosystem with no version ordering here.
var ErrNoScheme = errors.New("no version ordering is implemented for this ecosystem")

// CompareVersions orders two versions under the ecosystem's own rules: -1 when
// a sorts before b, 0 when they are the same version, 1 when a sorts after.
//
// A non-nil error means the two could not be ordered. It is never safe to
// treat that as "equal" or "older".
func CompareVersions(eco deps.Ecosystem, a, b string) (int, error) {
	switch eco {
	case deps.EcosystemGo, deps.EcosystemNPM, deps.EcosystemCargo:
		va, err := parseSemver(a)
		if err != nil {
			return 0, err
		}
		vb, err := parseSemver(b)
		if err != nil {
			return 0, err
		}
		return va.compare(vb), nil
	case deps.EcosystemPyPI:
		va, err := parsePEP440(a)
		if err != nil {
			return 0, err
		}
		vb, err := parsePEP440(b)
		if err != nil {
			return 0, err
		}
		return va.compare(vb), nil
	case deps.EcosystemMaven, deps.EcosystemGradle:
		return compareMaven(a, b)
	}
	return 0, fmt.Errorf("%w: %q", ErrNoScheme, eco)
}

// SchemeName names the ordering used for an ecosystem, for the report.
func SchemeName(eco deps.Ecosystem) string {
	switch eco {
	case deps.EcosystemGo, deps.EcosystemNPM, deps.EcosystemCargo:
		return "semver 2.0.0"
	case deps.EcosystemPyPI:
		return "PEP 440"
	case deps.EcosystemMaven, deps.EcosystemGradle:
		return "Maven ComparableVersion"
	}
	return ""
}

// ---------------------------------------------------------------- numbers

// cmpDigits orders two non-negative decimal strings numerically without
// converting them. A Go pseudo-version carries a 14-digit timestamp and Maven
// permits arbitrarily long numbers; an integer type would overflow on input a
// registry considers valid, and an overflow here is a wrong ordering.
func cmpDigits(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------- semver

type semver struct {
	nums [3]string
	pre  []string
}

// parseSemver reads a semantic version.
//
// Lenient in exactly three ways, each because a registry really produces it: a
// leading "v" (every Go module version), build metadata (Go's "+incompatible",
// ignored for precedence as the specification requires), and a missing minor or
// patch ("1.2", read as 1.2.0 — OSV events are occasionally written short).
// Anything else is refused rather than guessed at.
func parseSemver(s string) (semver, error) {
	orig := s
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	core, pre, hasPre := strings.Cut(s, "-")
	var v semver
	parts := strings.Split(core, ".")
	if len(parts) > 3 {
		return v, fmt.Errorf("%q is not a semantic version", orig)
	}
	for i := range v.nums {
		v.nums[i] = "0"
	}
	for i, p := range parts {
		if !allDigits(p) {
			return v, fmt.Errorf("%q is not a semantic version", orig)
		}
		v.nums[i] = p
	}
	if hasPre {
		if pre == "" {
			return v, fmt.Errorf("%q is not a semantic version: empty pre-release", orig)
		}
		for _, id := range strings.Split(pre, ".") {
			if id == "" || !semverIdent(id) {
				return v, fmt.Errorf("%q is not a semantic version: bad pre-release identifier", orig)
			}
			v.pre = append(v.pre, id)
		}
	}
	return v, nil
}

func semverIdent(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-'
		if !ok {
			return false
		}
	}
	return true
}

// compare implements semver 2.0.0 §11.
func (a semver) compare(b semver) int {
	for i := range a.nums {
		if c := cmpDigits(a.nums[i], b.nums[i]); c != 0 {
			return c
		}
	}
	// A version with a pre-release sorts BEFORE the same version without one.
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, y := a.pre[i], b.pre[i]
		xn, yn := allDigits(x), allDigits(y)
		var c int
		switch {
		case xn && yn:
			c = cmpDigits(x, y)
		case xn:
			c = -1 // numeric identifiers sort below alphanumeric ones
		case yn:
			c = 1
		default:
			c = strings.Compare(x, y)
		}
		if c != 0 {
			return c
		}
	}
	switch {
	case len(a.pre) < len(b.pre):
		return -1
	case len(a.pre) > len(b.pre):
		return 1
	}
	return 0
}

// ---------------------------------------------------------------- PEP 440

// pep440Pattern is the version grammar from PEP 440 appendix B, as the
// reference implementation (pypa/packaging) writes it.
var pep440Pattern = regexp.MustCompile(`(?i)^\s*v?` +
	`(?:(?P<epoch>[0-9]+)!)?` +
	`(?P<release>[0-9]+(?:\.[0-9]+)*)` +
	`(?P<pre>[-_.]?(?P<pre_l>alpha|a|beta|b|preview|pre|c|rc)[-_.]?(?P<pre_n>[0-9]+)?)?` +
	`(?P<post>(?:-(?P<post_n1>[0-9]+))|(?:[-_.]?(?P<post_l>post|rev|r)[-_.]?(?P<post_n2>[0-9]+)?))?` +
	`(?P<dev>[-_.]?(?P<dev_l>dev)[-_.]?(?P<dev_n>[0-9]+)?)?` +
	`(?:\+(?P<local>[a-z0-9]+(?:[-_.][a-z0-9]+)*))?` +
	`\s*$`)

type pep440 struct {
	epoch   string
	release []string

	hasPre  bool
	preRank int // 0 alpha, 1 beta, 2 release candidate
	preN    string

	hasPost bool
	postN   string

	hasDev bool
	devN   string

	local []string
}

func parsePEP440(s string) (pep440, error) {
	var v pep440
	m := pep440Pattern.FindStringSubmatch(s)
	if m == nil {
		return v, fmt.Errorf("%q is not a PEP 440 version", s)
	}
	group := func(name string) string { return m[pep440Pattern.SubexpIndex(name)] }
	orZero := func(n string) string {
		if n == "" {
			return "0"
		}
		return n
	}

	v.epoch = orZero(group("epoch"))
	v.release = strings.Split(group("release"), ".")
	// Trailing zeros are not significant: 1.0 and 1.0.0 are the same release.
	for len(v.release) > 1 && cmpDigits(v.release[len(v.release)-1], "0") == 0 {
		v.release = v.release[:len(v.release)-1]
	}

	if group("pre") != "" {
		v.hasPre = true
		switch strings.ToLower(group("pre_l")) {
		case "a", "alpha":
			v.preRank = 0
		case "b", "beta":
			v.preRank = 1
		default: // c, rc, pre, preview
			v.preRank = 2
		}
		v.preN = orZero(group("pre_n"))
	}
	if group("post") != "" {
		v.hasPost = true
		v.postN = orZero(group("post_n1") + group("post_n2"))
	}
	if group("dev") != "" {
		v.hasDev = true
		v.devN = orZero(group("dev_n"))
	}
	if l := group("local"); l != "" {
		v.local = strings.FieldsFunc(strings.ToLower(l), func(r rune) bool {
			return r == '-' || r == '_' || r == '.'
		})
	}
	return v, nil
}

// compare follows the sort key in pypa/packaging (_cmpkey): epoch, release,
// pre, post, dev, local — with the infinities that make 1.0.dev1 sort before
// 1.0a1, and 1.0 sort before 1.0.post1.
func (a pep440) compare(b pep440) int {
	if c := cmpDigits(a.epoch, b.epoch); c != 0 {
		return c
	}
	for i := 0; i < len(a.release) || i < len(b.release); i++ {
		x, y := "0", "0"
		if i < len(a.release) {
			x = a.release[i]
		}
		if i < len(b.release) {
			y = b.release[i]
		}
		if c := cmpDigits(x, y); c != 0 {
			return c
		}
	}

	// Pre-release segment. A version that is ONLY a dev release (no pre, no
	// post) sorts before any pre-release of the same release; a version with
	// no pre-release at all sorts after every pre-release.
	preKey := func(v pep440) (class, rank int, n string) {
		switch {
		case !v.hasPre && !v.hasPost && v.hasDev:
			return -1, 0, "0"
		case !v.hasPre:
			return 1, 0, "0"
		}
		return 0, v.preRank, v.preN
	}
	ac, ar, an := preKey(a)
	bc, br, bn := preKey(b)
	if ac != bc {
		return sign(ac - bc)
	}
	if ar != br {
		return sign(ar - br)
	}
	if c := cmpDigits(an, bn); c != 0 {
		return c
	}

	// Post segment: absent sorts before present.
	if a.hasPost != b.hasPost {
		if b.hasPost {
			return -1
		}
		return 1
	}
	if a.hasPost {
		if c := cmpDigits(a.postN, b.postN); c != 0 {
			return c
		}
	}

	// Dev segment: absent sorts AFTER present.
	if a.hasDev != b.hasDev {
		if a.hasDev {
			return -1
		}
		return 1
	}
	if a.hasDev {
		if c := cmpDigits(a.devN, b.devN); c != 0 {
			return c
		}
	}

	// Local segment: absent sorts before present; numeric parts above
	// alphanumeric ones; a longer local version above its own prefix.
	for i := 0; i < len(a.local) && i < len(b.local); i++ {
		x, y := a.local[i], b.local[i]
		xn, yn := allDigits(x), allDigits(y)
		var c int
		switch {
		case xn && yn:
			c = cmpDigits(x, y)
		case xn:
			c = 1
		case yn:
			c = -1
		default:
			c = strings.Compare(x, y)
		}
		if c != 0 {
			return c
		}
	}
	return sign(len(a.local) - len(b.local))
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// ---------------------------------------------------------------- Maven

// Maven's ordering is an algorithm, not a grammar: every string is a version,
// and ComparableVersion defines how any two compare. This follows the "Version
// Order Specification" in the POM reference — dots and hyphens separate tokens,
// a hyphen (or a digit/letter transition) opens a nested list, trailing "null"
// tokens (0, "", final, ga, release) are trimmed, and qualifiers order as
//
//	alpha < beta < milestone < rc = cr < snapshot < "" = final = ga = release < sp
//
// with any other qualifier after those, alphabetically.
//
// ONE known disagreement between Maven releases is refused rather than
// answered. Maven 3.9 changed how a string qualifier after a DOT compares with
// the same release's qualifier after a HYPHEN ("1.0.0.RC1" against
// "1.0.0-RC2", MNG-7644): older Maven orders them one way, newer the other.
// Which Maven a project builds with is not something this tool knows, so that
// one shape returns an error. See mavenSeparatorConflict.

type (
	mvnInt  string // decimal digits
	mvnStr  string // canonical qualifier
	mvnList struct{ items []any }
)

var mvnQualifiers = []string{"alpha", "beta", "milestone", "rc", "snapshot", "", "sp"}

const mvnReleaseKey = "5" // index of "" in mvnQualifiers

func mvnQualifierKey(q string) string {
	for i, known := range mvnQualifiers {
		if q == known {
			return strconv.Itoa(i)
		}
	}
	return strconv.Itoa(len(mvnQualifiers)) + "-" + q
}

func newMvnStr(value string, followedByDigit bool) mvnStr {
	if followedByDigit && len(value) == 1 {
		switch value[0] {
		case 'a':
			value = "alpha"
		case 'b':
			value = "beta"
		case 'm':
			value = "milestone"
		}
	}
	switch value {
	case "ga", "final", "release":
		value = ""
	case "cr":
		value = "rc"
	}
	return mvnStr(value)
}

func mvnParseItem(isDigit bool, buf string) any {
	if isDigit {
		buf = strings.TrimLeft(buf, "0")
		if buf == "" {
			buf = "0"
		}
		return mvnInt(buf)
	}
	return newMvnStr(buf, false)
}

func mvnIsNull(item any) bool {
	switch it := item.(type) {
	case mvnInt:
		return it == "0"
	case mvnStr:
		return mvnQualifierKey(string(it)) == mvnReleaseKey
	case *mvnList:
		return len(it.items) == 0
	}
	return true
}

func (l *mvnList) normalize() {
	for i := len(l.items) - 1; i >= 0; i-- {
		last := l.items[i]
		if mvnIsNull(last) {
			l.items = append(l.items[:i], l.items[i+1:]...)
		} else if _, isList := last.(*mvnList); !isList {
			break
		}
	}
}

func parseMaven(version string) *mvnList {
	v := strings.ToLower(version)
	root := &mvnList{}
	cur := root
	stack := []*mvnList{root}
	push := func() {
		n := &mvnList{}
		cur.items = append(cur.items, n)
		cur = n
		stack = append(stack, n)
	}

	isDigit := false
	start := 0
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c == '.' || c == '-':
			if i == start {
				cur.items = append(cur.items, mvnInt("0"))
			} else {
				cur.items = append(cur.items, mvnParseItem(isDigit, v[start:i]))
			}
			start = i + 1
			if c == '-' {
				push()
			}
		case c >= '0' && c <= '9':
			if !isDigit && i > start {
				cur.items = append(cur.items, newMvnStr(v[start:i], true))
				start = i
				push()
			}
			isDigit = true
		default:
			if isDigit && i > start {
				cur.items = append(cur.items, mvnParseItem(true, v[start:i]))
				start = i
				push()
			}
			isDigit = false
		}
	}
	if len(v) > start {
		cur.items = append(cur.items, mvnParseItem(isDigit, v[start:]))
	}
	for i := len(stack) - 1; i >= 0; i-- {
		stack[i].normalize()
	}
	return root
}

// mvnCompare orders two items; b may be nil, meaning "nothing at this position".
func mvnCompare(a, b any) int {
	switch x := a.(type) {
	case mvnInt:
		switch y := b.(type) {
		case nil:
			if x == "0" {
				return 0
			}
			return 1
		case mvnInt:
			return cmpDigits(string(x), string(y))
		default: // a number is newer than a qualifier or a sub-list
			return 1
		}
	case mvnStr:
		switch y := b.(type) {
		case nil:
			return strings.Compare(mvnQualifierKey(string(x)), mvnReleaseKey)
		case mvnInt:
			return -1
		case mvnStr:
			return strings.Compare(mvnQualifierKey(string(x)), mvnQualifierKey(string(y)))
		default:
			return -1
		}
	case *mvnList:
		switch y := b.(type) {
		case nil:
			for _, it := range x.items {
				if c := mvnCompare(it, nil); c != 0 {
					return c
				}
			}
			return 0
		case mvnInt:
			return -1
		case mvnStr:
			return 1
		case *mvnList:
			for i := 0; i < len(x.items) || i < len(y.items); i++ {
				var l, r any
				if i < len(x.items) {
					l = x.items[i]
				}
				if i < len(y.items) {
					r = y.items[i]
				}
				var c int
				if l == nil {
					if r != nil {
						c = -mvnCompare(r, nil)
					}
				} else {
					c = mvnCompare(l, r)
				}
				if c != 0 {
					return c
				}
			}
			return 0
		}
	}
	return 0
}

var mvnNumericCore = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)*`)

// mavenSeparatorConflict reports the one comparison whose answer depends on the
// Maven release: the same numeric release, with a non-release qualifier
// attached by a dot on one side and by a hyphen (or directly) on the other.
func mavenSeparatorConflict(a, b string) bool {
	split := func(v string) (core []string, dotQualifier, otherQualifier bool) {
		v = strings.ToLower(strings.TrimSpace(v))
		c := mvnNumericCore.FindString(v)
		rest := v[len(c):]
		core = strings.Split(c, ".")
		for len(core) > 1 && cmpDigits(core[len(core)-1], "0") == 0 {
			core = core[:len(core)-1]
		}
		if rest == "" {
			return core, false, false
		}
		if rest[0] == '.' {
			q := strings.TrimLeft(rest[1:], "abcdefghijklmnopqrstuvwxyz")
			word := rest[1 : len(rest)-len(q)]
			// .Final, .GA and .RELEASE are "null" qualifiers under every Maven
			// release: they are trimmed, and there is nothing to disagree on.
			if q == "" && (word == "final" || word == "ga" || word == "release") {
				return core, false, false
			}
			return core, true, false
		}
		// A hyphen followed by a NUMBER ("1-1") is a sub-version, not a
		// qualifier, and every Maven release orders it the same way.
		q := strings.TrimPrefix(rest, "-")
		return core, false, q != "" && q[0] >= 'a' && q[0] <= 'z'
	}
	ca, da, oa := split(a)
	cb, db, ob := split(b)
	if (!da || !ob) && (!db || !oa) {
		return false
	}
	if len(ca) != len(cb) {
		return false
	}
	for i := range ca {
		if cmpDigits(ca[i], cb[i]) != 0 {
			return false
		}
	}
	return true
}

func compareMaven(a, b string) (int, error) {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	if a == "" || b == "" {
		return 0, errors.New("empty Maven version")
	}
	if mavenSeparatorConflict(a, b) {
		return 0, fmt.Errorf("%q and %q attach a qualifier with different separators; "+
			"Maven releases before and after 3.9 order that differently", a, b)
	}
	return sign(mvnCompare(parseMaven(a), parseMaven(b))), nil
}
