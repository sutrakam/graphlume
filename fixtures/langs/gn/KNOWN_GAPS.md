# GN known gaps

## A target is two sibling nodes

The grammar does not wrap a target. `executable("demo") { ... }` is a call
expression and, as its next sibling, a block:

```
(primary_expression (call_expression (identifier) (primary_expression (string (string_content)))))
(primary_expression (block ...))
```

The query matches that pair. A call with one literal string argument followed
by a block is therefore taken to be a target whatever its name is, so a
construct of that shape that is not a declaration would be extracted. The two
known ones, `set_defaults("t") { }` and `tool("cc") { }`, are excluded by name.
Unknown constructs of the same shape are not.

## Targets named by an expression

`source_set(target_name) { }` and `action(target_name + "_gen") { }` inside a
template are not extracted: the name is only known when the template runs. A
literal that interpolates (`"${target_name}_gen"`) is skipped for the same
reason.

## Assignments

Only plain `=` at the top level, and inside `declare_args()`, is a variable. A
variable that is assigned more than once at the top level is listed once per
assignment. Variables assigned inside a template, a target or an `if` block
are not indexed.

## Not captured, by choice

`import()`, `deps` and `configs` labels, `foreach`, `forward_variables_from`,
`assert` and `print`.
