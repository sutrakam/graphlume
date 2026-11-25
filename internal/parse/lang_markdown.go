package parse

import (
	"regexp"
	"strings"
)

// Markdown. What the index holds, see fixtures/langs/markdown/expected.tsv:
// every heading, ATX (# Title) and setext (Title over === or ---), as a
// package named by the heading's text. Nothing else: not links, not code
// blocks, not list items, not link reference definitions.
//
// The rule is narrow on purpose. A heading is the one thing in a document that
// a person looks up by name ("where is the Installing section"), and headings
// are rarely repeated within a file. The name is the heading text with its
// closing # run and inline markup (emphasis, code spans, links) removed.
// Headings inside a fenced or indented code block are not headings to the
// grammar and are not captured.
func init() {
	registerDefinitions("markdown", markdownDefinitions, false)
	registerNameFix("markdown", markdownHeadingName)
}

const markdownDefinitions = `
(atx_heading (inline) @name) @definition.module
(setext_heading (paragraph (inline) @name)) @definition.module
`

var (
	markdownClosingHashes = regexp.MustCompile(`\s+#+\s*$`)
	markdownLink          = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	markdownEmphasis      = regexp.MustCompile("[*`]+|(^|\\s)_+|_+(\\s|$)")
	markdownSpaces        = regexp.MustCompile(`\s+`)
)

// markdownHeadingName turns the inline text of a heading into its name: the
// closing sequence of # characters, links reduced to their text, emphasis
// markers and code-span backticks removed, runs of white space (a setext
// heading may span lines) collapsed to one space.
func markdownHeadingName(text string) string {
	text = strings.TrimSpace(text)
	text = markdownClosingHashes.ReplaceAllString(text, "")
	text = markdownLink.ReplaceAllString(text, "$1")
	text = markdownEmphasis.ReplaceAllString(text, "$1$2")
	return strings.TrimSpace(markdownSpaces.ReplaceAllString(text, " "))
}
