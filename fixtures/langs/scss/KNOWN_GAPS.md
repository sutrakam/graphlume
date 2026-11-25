# SCSS known gaps

## The statement after a one-line map literal is lost

A map written on one line breaks the grammar, and the next statement is
swallowed into the map's declaration:

```
$s: (small: 480px);      ->  (declaration (property_name) (ERROR (plain_value) (ERROR) (ERROR)) (integer_value (unit)))
$d: 4px;                     (this declaration is inside the one above and has no node of its own)
```

`$s` is extracted; the `$d`, `@mixin` or `:root { --x }` that follows it is
not. The same map written over several lines parses cleanly. Nothing in the
tree names the lost statement, so no query can recover it.

## `@use ... as`, `!default` on some values, `@forward`

These produce `ERROR` nodes. They hold no definition themselves; a file full
of them is reported as partial and a later definition can be lost with them.
`$name: "text" !default;` is extracted.

## `@font-face` family

The SCSS grammar gives a quoted string no `string_content` child, so the
family name cannot be taken without its quotes. `@font-face` is not extracted
(CSS extracts it).

## Not captured, by choice

Same as CSS: classes and ids in descendant, compound, listed, pseudo or
nested selectors, rule sets nested in a block, `$variables` inside a mixin,
function, rule or `@if`/`@each` block, and `@include`/`@extend` uses.

## Module-system calls with named arguments (Bulma 1.x)

`color.channel($value, "hue", $space: hsl)`, `color.adjust($c, $lightness: 10%)`
and similar calls (`name: value` inside the arguments of a function call) are
not in the grammar. The error spreads: the rest of the enclosing mixin and the
statements after it are re-parsed as loose nodes under an `ERROR` or directly
under the stylesheet, and one failure can leave the whole file `partial`
(`recovered.scss`). Two things follow.

- A `@mixin` or `@function` caught in the error keeps its name only as an
  `@mixin` token followed by an identifier, and the query reads it from there.
  It is lost when the grammar also splits the name: in
  `@mixin generate-on-scheme-colors(...)` the `or` inside `color` is lexed as a
  keyword, the name arrives as nine tokens (`generate`, `-`, `on`, ...) and no
  one node holds it. That mixin is not recorded.
- The statements of a mixin that was lost this way are loose in the tree, so a
  `$local: ...;` inside it looks exactly like a top-level variable and is
  recorded as one (`$on-scheme-l` in Bulma's `css-variables.scss`). Likewise a
  rule set nested in an `@include ... { }` block whose `@include` failed to parse
  is a direct child of the error node, indistinguishable from a top-level rule
  set; only the column tells them apart and a query cannot see it. An
  interpolated name is excluded whatever its position, which removes most of
  these in Bulma.

## Interpolated selectors

`.#{$prefix}navbar { }` is never a definition (see expected.tsv). The name is
known only after compilation.
