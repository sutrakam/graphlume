package parse

// Bash: `for name do`.
//
// POSIX shell lets a for loop leave out `in`, and then it loops over the
// script's own arguments: `for arg do ... done`. The Gradle wrapper script,
// `gradlew`, which every Android and Gradle project carries, has exactly that
// line. The grammar has no production for it, so the loop is one error node.
//
// The mask rewrites `for name do` as `while :;do` followed by blanks, the same
// length with the same newlines, which closes with `done` just as the for loop
// does. What the loop iterates is not a definition. A name of one or two letters
// leaves too few characters to fit `while :;do`, so it is left alone.

import (
	"bytes"
	"regexp"
	"sort"
)

var bashForWithoutIn = regexp.MustCompile(`(?m)^[ \t]*(for[ \t]+[A-Za-z_][A-Za-z0-9_]*[ \t]+do)\b`)

// findBashForWithoutIn returns one span for each such loop head.
func findBashForWithoutIn(src []byte) []maskSpan {
	const head = "while :;do"
	var out []maskSpan
	for _, m := range bashForWithoutIn.FindAllSubmatchIndex(src, -1) {
		start, end := m[2], m[3]
		if end-start < len(head) {
			continue
		}
		repl := []byte(head)
		for len(repl) < end-start {
			repl = append(repl, ' ')
		}
		out = append(out, maskSpan{start: start, repl: repl})
	}
	return out
}

// Bash: punctuation in the message of `${name:?message}`.
//
// `${1:?mint_jwt: ROLE is required (ADMIN|CENTRAL|FE)}` stops the script with
// that message when the argument is missing. The grammar reads the message as
// shell words, so the `(`, `|` and `)` in it are a subshell and a pipe, and the
// function around the line (pulse's jwt.sh: 6 error nodes) is lost.
//
// The mask overwrites those characters, and `;`, `&`, `<` and `>`, with `_`
// inside such a message only (a space would leave one before the closing brace,
// which the grammar also rejects). A message is text printed to a person; nothing in
// it is a definition. Same length, newlines kept.

// findBashParamMessages returns one one-byte span for each such character.
func findBashParamMessages(src []byte) []maskSpan {
	if !bytes.Contains(src, []byte(":?")) {
		return nil
	}
	var out []maskSpan
	for i := 0; i+1 < len(src); i++ {
		if src[i] != '$' || src[i+1] != '{' || bashCommentBefore(src, i) {
			continue
		}
		msg, end, ok := bashMessageSpan(src, i+2)
		if !ok {
			continue
		}
		for k := msg; k < end; k++ {
			switch src[k] {
			case '(', ')', '|', ';', '&', '<', '>':
				out = append(out, maskSpan{start: k, repl: []byte("_")})
			}
		}
		i = end
	}
	return out
}

// bashMessageSpan returns the message of the expansion opening at src[from]
// (after `${`), and where the expansion closes: the text between `:?` and the
// matching `}`. ok is false for an expansion with no `:?` message.
func bashMessageSpan(src []byte, from int) (start, end int, ok bool) {
	i := from
	for i < len(src) && (isTSIdentByte(src[i]) || src[i] == '#' || src[i] == '!' || src[i] == '@' || src[i] == '*') {
		i++
	}
	if i+1 >= len(src) || src[i] != ':' || src[i+1] != '?' {
		return 0, 0, false
	}
	start = i + 2
	depth := 1
	for k := start; k < len(src) && src[k] != '\n'; k++ {
		switch {
		case src[k] == '$' && k+1 < len(src) && src[k+1] == '{':
			depth++
			k++
		case src[k] == '}':
			depth--
			if depth == 0 {
				return start, k, true
			}
		}
	}
	return 0, 0, false
}

// bashCommentBefore reports whether a `#` comment starts earlier on the line.
func bashCommentBefore(src []byte, at int) bool {
	lineStart := bytes.LastIndexByte(src[:at], '\n') + 1
	return bytes.IndexByte(src[lineStart:at], '#') >= 0
}

// findBashMasks is every shell mask, in source order.
func findBashMasks(src []byte) []maskSpan {
	out := append(findBashForWithoutIn(src), findBashParamMessages(src)...)
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}
