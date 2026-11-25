package parse

import "strings"

// SQL. What the index holds, see fixtures/langs/sql/expected.tsv: the objects a
// file creates, other than tables. A view and a materialized view are a class,
// so is a CREATE TYPE (an enum or composite); a function, a procedure and a
// trigger are a function; a schema is a package; an index and a sequence are a
// variable (a named object of the database that is neither code nor a type).
//
// Tables and columns are NOT extracted here. The database scanner in
// internal/analyze records every table and column as symbols of kind `table`
// and `column`, which are the ones tables lists and data-access
// relationships attach to. A `class` for the same CREATE TABLE gave every
// table two symbols on one line, and a name lookup two answers. The scanner
// reads statements as text, so it also keeps the tables of a dialect this
// grammar cannot parse.
//
// Statements that only use an object (SELECT, INSERT, UPDATE, DELETE, ALTER,
// DROP, GRANT) are not definitions.
//
// A name is recorded without its schema: `CREATE TABLE shop.orders` is
// `orders`, because that is what queries refer to; the last part of a dotted
// name is captured. Quoting is removed by the name fix, so "Line Items" is
// `Line Items`, `users` (backticks) is users and [dbo] style brackets are
// dropped.
//
// The inferred query is replaced: it takes only a plain identifier as a name,
// so every schema-qualified object was missing, and it does not know CREATE
// MATERIALIZED VIEW or CREATE SCHEMA.
func init() {
	registerDefinitions("sql", sqlDefinitions, true)
	registerNameFix("sql", sqlName)
}

// sqlName removes the quoting around an identifier: "x", `x`, [x] and 'x'.
func sqlName(name string) string {
	if len(name) >= 2 {
		first, last := name[0], name[len(name)-1]
		switch {
		case first == '"' && last == '"', first == '`' && last == '`',
			first == '[' && last == ']', first == '\'' && last == '\'':
			name = name[1 : len(name)-1]
		}
	}
	return strings.TrimSpace(name)
}

const sqlDefinitions = `
(create_view_statement . [(identifier) @name (dotted_name (identifier) @name .)]) @definition.class
(create_materialized_view_statement . [(identifier) @name (dotted_name (identifier) @name .)]) @definition.class
(create_type_statement . [(identifier) @name (dotted_name (identifier) @name .)] (#not-match? @name "^(?i:to|from)$")) @definition.class
(create_function_statement . [(identifier) @name (dotted_name (identifier) @name .)] (#not-match? @name "^(?i:if)$")) @definition.function
(create_trigger_statement . [(identifier) @name (dotted_name (identifier) @name .)]) @definition.function
(create_schema_statement . [(identifier) @name (dotted_name (identifier) @name .)] (#not-match? @name "^(?i:authorization|if)$")) @definition.package
(create_index_statement . [(identifier) @name (dotted_name (identifier) @name .)] (#not-match? @definition.variable "^(?i:create[ \t\r\n]+(unique[ \t\r\n]+)?index[ \t\r\n]+on[ \t\r\n])")) @definition.variable
(create_index_statement (unique_constraint) . [(identifier) @name (dotted_name (identifier) @name .)]) @definition.variable
(create_statement (create_sequence . [(identifier) @name (dotted_name (identifier) @name .)]) @definition.variable)
`
