package parse

import "regexp"

// TOML. What the index holds, see fixtures/langs/toml/expected.tsv: every
// table header [a.b] as a package named by its dotted path without the
// brackets. Keys inside a table, keys before the first table, inline tables,
// values and arrays of tables ([[a.b]]) are not definitions.
//
// The rule is narrow because a TOML key is a setting, not a name in a code
// graph: the configuration analyser (internal/analyze/config) already reports
// every key as a configuration symbol, and a table header is the one TOML
// construct that names a section other files refer to ([tool.ruff],
// [rule.exported]). Three things are left out on purpose:
//
//   - [[a]] elements. The same header repeats once per element ([[package]]
//     once per dependency in a lock file, [[bin]], [[endpoints]]), so it is not
//     declared once and a query cannot collapse the repeats.
//   - The sections every manifest has (package, lib, workspace, features,
//     build-system, project, and every *dependencies table). They are one
//     name per Cargo.toml or pyproject.toml, so a monorepo would hold dozens
//     of "package" symbols, and the dependencies are already dependency
//     symbols from the manifest analyser.
//
// Quoted segments keep their quotes, so the dot inside "my.plugin" is not
// mistaken for a separator.
func init() {
	registerDefinitions("toml", tomlDefinitions, false)
	registerNameFix("toml", tomlTableName)
}

const tomlDefinitions = `
(table
  . [(bare_key) (dotted_key) (quoted_key)] @name
  (#not-match? @name "^(package|lib|workspace|features|build-system|project)([.]|$)")
  (#not-match? @name "(^|[.])(dev-|build-)?dependencies([.]|$)")) @definition.module
`

var tomlDotSpaces = regexp.MustCompile(`\s*\.\s*`)

// tomlTableName removes the white space TOML allows around the dots of a
// dotted key: `[ logging . file ]` is the table logging.file.
func tomlTableName(name string) string {
	return tomlDotSpaces.ReplaceAllString(name, ".")
}
