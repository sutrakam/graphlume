# ReScript: known gaps

## Destructuring lets

`let (a, b) = (1, 2)` binds two names through a pattern:

```
(let_declaration (let_binding (tuple_pattern (tuple_item_pattern (value_identifier)) ...) (tuple ...)))
```

It is not extracted. The names are `value_identifier` nodes inside a pattern and
cannot be told from the names a pattern uses elsewhere without matching local
patterns too.

## Let values of an unlisted expression type

A let is a function when its value is a `function` node; every other let is a
variable, but the query has to name the value node types (number, string,
list, record, call_expression, ...) because a pattern cannot say "any node
but a function", and `function` ranks below `variable` when a declaration is
tagged both ways. A value of an expression type that is not in the list is
missed. None showed up in the fixture or in the two Linguist samples.

## Not definitions here

Variant constructors (`Circle(float)`), record fields, and lets inside function
bodies are not extracted. `module type` and `module` both come out as
`package`: the tree does not mark the difference.
