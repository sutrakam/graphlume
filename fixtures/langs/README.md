# Language conformance fixtures

One directory per language, named exactly as the grammar is (`go`, `c_sharp`,
`objc`). Each holds source files written to exercise every way that language
declares something, and an `expected.tsv` that lists every definition those
files contain.

`TestLanguageConformance` in `internal/parse` parses each file with that
language's parser and compares what it extracts with the list. The two must be
equal: a definition in the list that is not extracted fails the test, and so
does a symbol that is extracted and not in the list. The fixture is the
specification of what "supported" means for the language.

## `expected.tsv`

Tab-separated, one definition per line:

```
file	line	kind	name
```

- `file` is relative to the language directory.
- `line` is the 1-based line the declaration starts on.
- `kind` is one of `function`, `method`, `class`, `interface`, `type_alias`,
  `package`, `constant`, `variable`, `field`. Structs, enums and unions are
  `class`; traits and protocols are `interface`; modules and namespaces are
  `package`.
- Lines starting with `#` and blank lines are ignored.

## Writing a fixture

Write the source by hand; do not copy files from other projects. Cover each
declaration form the language has, including the awkward ones: a method on a
type, a nested type, a generic, an exported and an unexported name, a
multi-line signature, a declaration after a construct that has confused the
grammar before. Include things that must NOT be extracted (a call, a local
variable, a type used in a parameter list) so over-capture fails too.

List only what a developer would expect a code index to hold. A local variable
is not a definition. Whether a top-level constant or a struct field is one
depends on the language; decide, write the decision as a comment at the top of
`expected.tsv`, and be consistent.

A language may also carry `KNOWN_GAPS.md`: declaration forms that are not yet
extracted, each with the reason. A gap listed there is not in `expected.tsv`.
The file exists so that an incomplete language says so in the tree.

## Adding or fixing a language

Everything for one language lives in two places, and nothing else needs to
change:

- `internal/parse/lang_<language>.go`: the definition query, registered from
  `init` with `registerDefinitions(language, query, replaceInferred)`.
- `fixtures/langs/<language>/`: the sources, `expected.tsv`, and
  `KNOWN_GAPS.md` when something cannot be extracted yet.

`<language>` is the grammar's name as `graphlume languages --verbose` prints
it.

The steps:

1. Write the fixture sources and `expected.tsv` first, from what the language
   declares, not from what is extracted today.
2. Print the syntax tree for a fixture file, with the query in force and what
   it extracts:

   ```
   GRAPHLUME_DUMP_FILE=$PWD/fixtures/langs/nim/sample.nim GRAPHLUME_DUMP_LANG=nim \
       go test -count=1 -run TestDumpTree -v ./internal/parse/
   ```

3. Write the query against the node types the tree really has. A pattern that
   names a node type the grammar does not have makes the whole query invalid,
   and the language then extracts nothing; the dump prints that error.
4. Register it. Pass `replaceInferred = true` when the query the library infers
   is wrong for the grammar (it captures things that are not definitions, or
   names nodes that do not exist); pass `false` to add patterns to it.
5. Run `go test -count=1 -run 'TestLanguageConformance/<language>' ./internal/parse/`
   until the extraction equals the list.

A query uses tree-sitter tag captures: `@name` on the identifier and
`@definition.KIND` on the declaring node. The kinds are `function`, `method`,
`constructor`, `class`, `struct`, `enum`, `interface`, `trait`, `type_alias`,
`module`, `namespace`, `constant`, `variable` and `field`. A kind that is not
in that list is recorded as `function`, so do not invent one. A call is
`@reference.call` with the same `@name`; leave calls out unless the language's
fixture also covers them.

When a declaration form cannot be captured by a query at all (the grammar
parses it as an error, or the name is not a node of its own), do not
approximate it with a pattern that also matches other things. Write it in
`KNOWN_GAPS.md` with the reason and the tree fragment that shows it.

A language that was examined and found to declare nothing worth indexing has a
directory with only a `KNOWN_GAPS.md` that says so, with the evidence, and no
`expected.tsv`. It must not register a query.
