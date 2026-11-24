# Bash: known gaps

## A function next to zsh-only syntax can be lost

`z.sh` defines `_z_zsh_tab_completion() { ... reply=(${(f)"$(...)"}) }`. The
`${(f)...}` parameter flag is zsh syntax; the grammar emits `ERROR` nodes for
the region and the function is not a `function_definition` any more. Nothing
can be captured from inside an `ERROR`. Fixed only by a grammar that reads zsh.

```
(ERROR (word) (compound_statement ...))
```

## Not recorded by design

- Variables assigned inside a function, loop, `case` or `if` (they are locals
  or conditional re-assignments, and recording them would flood a script that
  sets `i=$((i+1))`), and `local`.
- A bare `export NAME` or `readonly NAME`: they re-declare a name that is
  assigned elsewhere.
- An alias whose name is itself expanded (`alias ${_Z_CMD:-z}='...'`) has no
  static name, and the `alias -- name=value` form.
- Functions and assignments inside here-documents and strings (correctly not
  parsed as code).

## Detection is outside this query

An extensionless script with a `#!/usr/bin/env bash` line is not detected as
bash by file name; that is `internal/langdetect`, not a definition query.

## Recovered by a mask (internal/parse/mask_bash.go)

- `for arg do ... done`, the POSIX form with no `in` that every Gradle wrapper
  (`gradlew`) contains, is an error. The head is rewritten as `while :;do`, the
  same length. A loop variable of one or two letters is too short to fit and is
  left alone.
- `${1:?message (with|punctuation)}`: the `(`, `)`, `|`, `;`, `&`, `<` and `>` of
  a `:?` message are overwritten with `_`.
