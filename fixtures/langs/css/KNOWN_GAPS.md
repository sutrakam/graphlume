# CSS known gaps

## Legacy hacks make the parse partial

The grammar has no rule for the IE hacks old stylesheets carry (`*zoom: 1;`,
`filter: progid:...`, `\9`). Each becomes an `ERROR` node inside the block, and
in a long file the recovery sometimes wraps the whole stylesheet in one
`ERROR`:

```
(ERROR (rule_set ...) (rule_set ...) (media_statement ...) ...)
```

The query therefore also accepts a bare-class rule set that is a direct child
of `ERROR`. In such a file, a rule set that really sits inside an `@media`
block can be flattened into that `ERROR` and reported as a top-level class, and
a rule on the same line as an error can be lost. On the minified Bootstrap 2.3
sample that is 25 extra and 15 missing symbols out of about 300. A clean
stylesheet is not affected.

## Not captured, by choice

- Class and id selectors used in descendant, compound, listed or pseudo
  selectors (`.card .title`, `a.link`, `.btn.primary:hover`, `.a, .b`): these
  use a name, they do not define it.
- Rule sets nested in `@media`, `@layer`, `@supports` or a parent rule.
  Capturing them would report `.card` once per breakpoint.
- `@font-face` without a quoted `font-family` (an unquoted family is a
  `plain_value`, which the query does not read).
