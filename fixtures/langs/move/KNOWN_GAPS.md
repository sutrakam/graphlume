# Move known gaps

## The Move 2024 module label is read from ERROR nodes

`module shop::market;` (no braces, the form most Sui code uses) is not in the
bundled grammar. Everything after it becomes one flat run of identifiers:

```
(module_declaration
  (ERROR (module_identity (identifier) (identifier))
         (module_identity ...) (ERROR) (identifier) (identifier) ...))
```

`fun`, `struct`, `enum` and `const` are anonymous tokens in that run, so
`lang_move.go` takes the identifier that follows each of them
(`(ERROR "fun" . (identifier) @name)`), and the first `module_identity` of an
ERROR as the module. They are reserved words, so a match is the declaration.
`label.move` pins this. It is recovery, and has these limits:

- `use fun helper as u64.help;` is also `"fun"` followed by an identifier, so a
  label-form module with a `use fun` alias gets a spurious function named after
  the aliased function (`helper`) on the `use` line. The query cannot look at
  the token before `fun`.
- The module label form, when something unusual precedes the first
  declaration, may not yield the module symbol.
- Only fun, struct, enum, const and the module are recovered this way.

## `macro fun`

`public macro fun twice($x: u64): u64 { ... }` is not parsed. In a braced
module the rest of the module after it is lost as well (tree fragment: an
`(ERROR ...)` starting at the macro, the next `fun` reported only when the
grammar resynchronises), and in a label-form module the macro itself and the
declarations after it are not found. Not in the fixture, because the lost
declarations would make the list depend on error recovery.

## Not definitions (decided, not gaps)

`spec name { ... }` blocks (conditions on a function declared elsewhere),
struct fields, enum variants, locals and `use` declarations. `spec fun` is a
function and is extracted.
