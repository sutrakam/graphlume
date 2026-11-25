# Odin: known gaps

## A proc type declared by name

```odin
Callback :: proc(code: int) -> bool
```

parses as a `procedure_declaration` with a `procedure` and no `block`, the same
tree as a forward declaration or a `foreign` proc. It is extracted as a
`function`, not a `type_alias`, and is not in `expected.tsv` for that reason:
the tree has nothing to tell the two apart.

## Plain aliases and constants

`Alias :: int` and `X :: other_name` are both a `const_declaration` whose value
is an `identifier`. Both are recorded as `constant`. Only `distinct` types and
`bit_set` types are recorded as `class`.

## Files the grammar cannot read to the end

The grammar fails on parts of real Odin (an unbalanced brace, `where` clauses
and the like). When it does, the whole file is wrapped in one `ERROR` node:

```
(source_file (ERROR (package_declaration ..) (import_declaration ..)
  (procedure_declaration (identifier) (procedure ..)) ...))
```

Direct children of that top-level `ERROR` are accepted as package-level, so
the declarations before the failure point are found. Declarations after the
point where recovery gives up are lost; Linguist's `sample.odin` loses nine of
its 28 top-level procs this way.

## Attributes move the start line

A declaration with `@(private)` or similar starts on the attribute line, since
the attributes are inside the declaration node.
