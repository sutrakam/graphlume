# Kotlin known gaps

## Recovered by a mask (internal/parse/mask_kotlin.go)

- `i++; continue`: a postfix `++` or `--` followed by `;` and another statement on
  one line is an error. `++;` is rewritten as `+1;`, the same length.
- A block comment on the line before `abstract class` in a file whose `package`
  line has no semicolon is an error. The comment is rewritten as line comments,
  the same length, with its newlines kept.

## Stated types (internal/parse/shape.go)

A function parameter (`r: Repo`), a constructor property (`private val repo: Repo`),
a `lateinit var` and a local with an explicit type (`val y: Repo = make()`) give
their calls that type. Before, the first three were untyped `name` receivers and
the last ignored its type for the call that produced it.
