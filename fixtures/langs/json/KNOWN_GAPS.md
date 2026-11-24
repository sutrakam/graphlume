# JSON: what is and is not extracted

JSON has one declaration form worth indexing: the schema names a document
declares at its root. Everything else is data. `expected.tsv` states the rule.

## Not extracted, by design

- **Keys.** `name`, `version`, `type`, `description`, `id` and the like are in
  nearly every document, repeat within one, and name a value rather than a
  thing a developer looks up. Every key of a configuration `.json` is already
  a `configuration` symbol from `internal/analyze/config` (`jsonKeys`), and
  `package.json` is read as a dependency manifest by `internal/analyze/deps`.
- **`properties` entries** of a schema. A property name such as `id` or `name`
  occurs in every schema; it is a field of the data, not a declared type.
- **`$defs` / `definitions` / `components.schemas` below the root.** A nested
  sub-schema may carry its own `$defs` (`order.schema.json` has
  `Address.$defs.PostalCode`), and a property of a schema may be named
  `definitions`. A query cannot tell either from a schema keyword by position
  alone, so only the root is matched.
- **OpenAPI parameters, responses, operation ids and paths.** The analysers do
  not cover OpenAPI documents (`internal/analyze/api/routes.go` says so), but a
  path is a route and an operation id is not declared as a symbol anywhere; they
  are left to text search.
- **Other containers of named schemas.** `components.parameters`,
  `components.responses`, AsyncAPI `components.messages`, and JSON Schema
  `patternProperties` are not matched.

## Not extracted, grammar limits

- **A schema name that contains an escape** (`"With\"Quote"`, `"café"`).
  The grammar splits the key into several `string_content` and
  `escape_sequence` children, and a query on the first would name the schema
  `With`. The name must be a single `string_content`, so an escaped name is
  skipped (`order.schema.json` line 45).
- **An empty key** (`""`) has no `string_content`.
- **A root array** (`[ { "$defs": ... } ]`) has no root object.
- **`.jsonc` and `.json5`** are other languages, not this grammar. A `.json`
  file with `//` comments still parses cleanly and its schema names are
  extracted.
