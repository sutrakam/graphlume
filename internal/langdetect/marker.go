package langdetect

import (
	"bytes"
	"regexp"
)

// A marker is one piece of evidence: a literal, where it has to stand, and
// optionally what has to follow it.
//
// WHY NOT ONE REGULAR EXPRESSION PER LANGUAGE. That is how these rules were
// first written, and a header with no marker in it took 2.5 ms to clear: Go's
// regexp runs an alternation of thirty line-anchored branches as a simulation
// over every byte, at about 3 MB/s. Five thousand C headers would have cost
// twelve seconds of CPU to learn nothing.
//
// A marker is found by its literal first, and a regular expression runs only
// on the remainder of a line that already contains the literal in the right
// place. The same header now clears in 80 µs (see markerSet for the second
// half of that), and a file that does carry a marker in well under one.
type marker struct {
	lit []byte

	where position

	// wordBefore and wordAfter require the literal not to continue an
	// identifier on that side: `std::` but not `mystd::`, `nullptr` but not
	// `nullptr_t`. afterWord is the opposite on the left: `::` only directly
	// after an identifier.
	wordBefore, wordAfter, afterWord bool

	// fold matches the literal and the expressions against a lower-cased copy
	// of the text, for the languages that are case-insensitive. Literals and
	// expressions of a folded marker are written in lower case.
	fold bool

	// rest must match the text from the end of the literal to the end of its
	// line, anchored at the literal. line must match the whole line the
	// literal is on, anchored at its start. Either may be nil.
	rest, line *regexp.Regexp
}

type position uint8

const (
	anywhere  position = iota
	lineStart          // first thing on a line, after spaces and tabs
	columnZero
	fileStart // first thing in the file, after white space
)

// at is a literal at the start of a line, after any indentation. With a rest
// expression the two together read as `^[ \t]*<lit><rest>`.
func at(lit string, rest ...string) marker {
	return marker{lit: []byte(lit), where: lineStart, rest: anchored(rest)}
}

// atWord is at for a keyword: it must not run on into an identifier.
func atWord(lit string, rest ...string) marker {
	m := at(lit, rest...)
	m.wordAfter = true
	return m
}

// col0 is a literal in the first column.
func col0(lit string, rest ...string) marker {
	return marker{lit: []byte(lit), where: columnZero, rest: anchored(rest)}
}

// opens is a literal the file begins with, white space aside.
func opens(lit string) marker { return marker{lit: []byte(lit), where: fileStart} }

// has is a literal anywhere, optionally followed by rest.
func has(lit string, rest ...string) marker {
	return marker{lit: []byte(lit), rest: anchored(rest)}
}

// word is a whole word anywhere.
func word(lit string, rest ...string) marker {
	m := has(lit, rest...)
	m.wordBefore, m.wordAfter = true, true
	return m
}

// onLine is a literal anywhere on a line that matches expr from its start.
// For shapes with no fixed first token, such as `NAME = "value"`.
func onLine(lit string, expr string) marker {
	return marker{lit: []byte(lit), line: regexp.MustCompile(`\A(?:` + expr + `)`)}
}

func anchored(rest []string) *regexp.Regexp {
	if len(rest) == 0 {
		return nil
	}
	return regexp.MustCompile(`\A(?:` + rest[0] + `)`)
}

// folded returns the markers with case-insensitive matching switched on.
func folded(ms ...marker) []marker {
	for i := range ms {
		ms[i].fold = true
	}
	return ms
}

// source is the text under test and, on demand, its lower-cased copy.
type source struct {
	raw   []byte
	lower []byte
}

// folded lower-cases ASCII only and byte for byte, so that an offset into one
// copy is an offset into the other. bytes.ToLower follows Unicode and may
// change the length of the text.
func (s *source) folded() []byte {
	if s.lower == nil {
		s.lower = make([]byte, len(s.raw))
		for i, c := range s.raw {
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			s.lower[i] = c
		}
	}
	return s.lower
}

func isWordByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// found reports whether the marker occurs in the source.
func (m *marker) found(s *source) bool {
	text := s.raw
	if m.fold {
		text = s.folded()
	}
	if m.where == fileStart {
		i := 0
		for i < len(text) && (text[i] == ' ' || text[i] == '\t' || text[i] == '\r' || text[i] == '\n') {
			i++
		}
		return bytes.HasPrefix(text[i:], m.lit) && m.holds(text, i)
	}
	for from := 0; from < len(text); {
		i := bytes.Index(text[from:], m.lit)
		if i < 0 {
			return false
		}
		i += from
		if m.holds(text, i) {
			return true
		}
		from = i + 1
	}
	return false
}

// holds checks everything about one occurrence of the literal except that it
// is there.
func (m *marker) holds(text []byte, i int) bool {
	end := i + len(m.lit)
	switch m.where {
	case columnZero:
		if i > 0 && text[i-1] != '\n' {
			return false
		}
	case lineStart:
		for j := i - 1; j >= 0 && text[j] != '\n'; j-- {
			if text[j] != ' ' && text[j] != '\t' {
				return false
			}
		}
	}
	if m.wordBefore && i > 0 && isWordByte(text[i-1]) {
		return false
	}
	if m.afterWord && (i == 0 || !isWordByte(text[i-1])) {
		return false
	}
	if m.wordAfter && end < len(text) && isWordByte(text[end]) {
		return false
	}
	if m.rest == nil && m.line == nil {
		return true
	}

	lineEnd := len(text)
	if n := bytes.IndexByte(text[end:], '\n'); n >= 0 {
		lineEnd = end + n
	}
	if lineEnd > end && text[lineEnd-1] == '\r' {
		lineEnd--
	}
	if m.rest != nil && !m.rest.Match(text[end:lineEnd]) {
		return false
	}
	if m.line != nil {
		start := bytes.LastIndexByte(text[:i], '\n') + 1
		if !m.line.Match(text[start:lineEnd]) {
			return false
		}
	}
	return true
}

// markerSet is a list of markers arranged so that "is any of these present"
// costs one pass over the lines of the text plus one literal search for each
// marker that may stand anywhere.
//
// Most markers are line-start keywords, and searching for thirty of them one
// by one read the text thirty times. Here the first byte after a line's
// indentation selects the few markers that could start there, so a line with
// no marker on it costs a table lookup. On a full window with nothing to find
// (BenchmarkDetectHeaderWorstCase) this took the `.h` rules from 166 µs to
// 80 µs; what remains is the markers that may stand anywhere.
type markerSet struct {
	elsewhere []marker // anywhere or at the start of the file

	// Line-start and first-column markers, grouped by the first byte of their
	// literal. index[b] is 1 + the position in groups, or 0 for none. The
	// folded half is matched against the lower-cased text.
	raw, folded lineIndex
}

type lineIndex struct {
	index  [256]uint8
	groups [][]marker
}

func (li *lineIndex) add(m marker) {
	b := m.lit[0]
	if li.index[b] == 0 {
		li.groups = append(li.groups, nil)
		li.index[b] = uint8(len(li.groups)) //nolint:gosec // a rule has a few dozen markers, far below 255 groups
	}
	g := li.index[b] - 1
	li.groups[g] = append(li.groups[g], m)
}

func newMarkerSet(ms []marker) *markerSet {
	if len(ms) == 0 {
		return nil
	}
	set := &markerSet{}
	for _, m := range ms {
		switch {
		case m.where != lineStart && m.where != columnZero:
			set.elsewhere = append(set.elsewhere, m)
		case m.fold:
			set.folded.add(m)
		default:
			set.raw.add(m)
		}
	}
	return set
}

// found reports whether any marker of the set occurs in the source.
func (set *markerSet) found(s *source) bool {
	if set == nil {
		return false
	}
	if len(set.raw.groups) > 0 && set.raw.scan(s.raw) {
		return true
	}
	if len(set.folded.groups) > 0 && set.folded.scan(s.folded()) {
		return true
	}
	for i := range set.elsewhere {
		if set.elsewhere[i].found(s) {
			return true
		}
	}
	return false
}

// scan walks the lines of text once, testing at each the markers whose
// literal starts with the line's first non-blank byte.
func (li *lineIndex) scan(text []byte) bool {
	for pos := 0; pos < len(text); {
		q := pos
		for q < len(text) && (text[q] == ' ' || text[q] == '\t') {
			q++
		}
		if q < len(text) {
			if g := li.index[text[q]]; g != 0 {
				for i := range li.groups[g-1] {
					m := &li.groups[g-1][i]
					if m.where == columnZero && q != pos {
						continue
					}
					if bytes.HasPrefix(text[q:], m.lit) && m.holds(text, q) {
						return true
					}
				}
			}
		}
		nl := bytes.IndexByte(text[q:], '\n')
		if nl < 0 {
			return false
		}
		pos = q + nl + 1
	}
	return false
}
