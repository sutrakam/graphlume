# Zig: known gaps

## Types and values are not told apart by syntax

`const Allocator = std.mem.Allocator;` (a type) and `const assert =
std.debug.assert;` (a function) have the same tree. A TitleCase name bound to a
name, a field access or a call is recorded as a `type_alias` (Zig's own naming
convention for types); a snake_case one is a `constant`. A type produced by a
comptime call under a snake_case name, or a value under a TitleCase name, is
recorded under the wrong one of the two.

```
(variable_declaration (identifier) (field_expression (identifier) (identifier)))
```

## Members of types that are not declared by name

Fields and variants are recorded for a type that is declared (`const T =
struct {...}`), or returned from a function (`return struct {...};`). The
members of a type written inline as the type of a local variable
(`var state: enum { a, b } = .a;`), of a function's result type
(`fn f() !struct { bytes: usize }`), or of any container inside a function body
are not recorded, nor are the members of an error set. A function declared in
such a container is recorded as a `function`, not a `method`.

## Not recorded by design

`@import` bindings and `const Self = @This();`; locals (declarations in a
function body); `test someFn {}` (a doc test: it names an existing function);
`comptime {}` blocks; `usingnamespace`.
