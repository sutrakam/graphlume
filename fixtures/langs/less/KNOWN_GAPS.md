# Less known gaps

## Namespaced mixin calls with named arguments break what follows

A call such as `#gradient > .vertical(@start-color: @a; @end-color: @b);` is
read as a selector, and the error recovery that follows swallows the rest of
the block and the rules after it:

```
(ERROR (selectors (child_selector (child_selector (class_selector (class_name)) (tag_name)) ...)))
(rule_set (selectors (child_selector ... (ERROR (ERROR)) (pseudo_class_selector (class_name) (arguments (variable ...)))))
```

Definitions after the first such call can be missing. In Bootstrap 3's
`theme.less`, which uses these calls throughout, 9 of 27 definitions were not
extracted (all after the first call). Nothing in the tree names the lost
rules, so no query can recover them. The fixture contains the simpler
`#namespace > .mixin();` call, which only produces a local `ERROR`.

## Not captured, by choice

- A mixin call (`.rounded();`) and a guard that is not on a definition.
- Classes in descendant, compound, listed, interpolated (`.@{x}-wide`) or
  nested (`&-title`) selectors, and rule sets nested in a block.
- An `@variable` inside a rule or `@media` block (it is local).
- `@import` and `@plugin`.
- `@font-face` (the grammar's string value has no content node, so the
  family cannot be read without its quotes).

## A rule set that is also used as a mixin

`.rounded { ... }` and `.rounded();` use one syntax, so a plain `.class { }`
is a class here even though Less lets it be called as a mixin. Only a
definition with a parameter list `()` is a function.
