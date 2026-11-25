# Gleam: known gaps

## Constructors of a custom type

`Circle(radius: Float)` inside `pub type Shape { ... }` is a `data_constructor`
with a `constructor_name`. It is not listed as a definition: the type is the
definition, and a constructor is a way to build it. This is a decision, not a
grammar limit.

## Removed pre-1.0 syntax

Gleam dropped `try x = ...` and `pub external fn f(..) -> T = "mod" "fun"`
before 1.0. The grammar does not parse them. A function whose body uses `try`
becomes an `ERROR` node that swallows the rest of the file up to the next
declaration the parser can resynchronise on:

```
(function (visibility_modifier) (identifier) (function_parameters ...) (type ...))
(ERROR (identifier) (identifier) (quoted_content) (quoted_content) ...)
```

The `ERROR` node holds loose identifiers and string fragments, not a
declaration, so no pattern can name a function in it without matching other
things. Definitions after such a body can be lost (`decode_page`, `send`,
`receive`, `await_upgrade` in the Linguist samples). Code written for current
Gleam (`use`, `@external(...)`) is unaffected and is what the fixture covers.
