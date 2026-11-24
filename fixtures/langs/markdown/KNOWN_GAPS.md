# Markdown: forms not extracted

Only headings are definitions (see the rule at the top of `expected.tsv`).

- **Duplicate headings.** `## Usage` appears in many documents and often several
  times in one (a changelog repeats `### Added`, `### Fixed` under every
  release). Each is recorded, since the grammar gives no way to tell a repeat
  from a first use; a lookup of such a name returns many rows.
- **Heading text is the name, not an anchor.** The GitHub slug
  (`#install-on-linux`) is not recorded.
- **Heading level and nesting** are not recorded as a hierarchy; every heading
  is a flat `package`.
- **Headings inside a block quote** are extracted like any other (the grammar
  nests the heading in a section of the quote, indistinguishable by query from
  a top-level one).
- **Front matter** (`---` YAML or `+++` TOML) is not read; its keys are not
  definitions. The closing `---` is not taken as a setext underline.
- **Links, images, reference definitions, fenced code, tables** are not
  definitions by design.
- The `markdown_inline` grammar is not used; emphasis and link syntax in a
  heading is removed from the name by text rules, so an unusual construct
  (nested brackets, raw HTML in a heading) may leave residue in the name.
- **Fixture file extension.** The conformance walk skips every `.md` file (to skip
  this document), so the fixture sources are named `.markdown`.
