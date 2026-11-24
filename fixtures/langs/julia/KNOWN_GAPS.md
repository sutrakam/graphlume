# Julia: forms that are not extracted

- **Functors and parenthesised constructors**: `(f::Callable)(x, y) = ...`,
  `function (f::Callable)(x) ... end` and
  `function (LittleSet{T, Tuple{Vararg{T}}} where {T})(data) ... end`. They
  define a method on a type, not a function with a name of its own; the
  closest name is the type, but it sits inside a parenthesised expression of
  several shapes. Fragment:

  ```
  (function_definition (signature (call_expression (parenthesized_expression (typed_expression (identifier) (identifier))) (argument_list (identifier)))))
  ```

  Tagging the type would also tag every `(x::T)(...)` call-like left side, so
  it is left out.
- **Short-form methods inside a function body**: `helper(x) = x + n` is a local
  closure, and in the tree it is the same `assignment` as a top-level method;
  only its position tells them apart, so it is not extracted. A long-form
  `function` is extracted at any depth.
- **Short-form methods in other nesting**: only the top level of a file, module
  or struct, and the branches of an `if` or `@static if` there, are read. A
  method inside `let`, `begin` or a `for` at the top level is not.
- **Enum members**: `@enum Color red green blue` gives the class `Color` only.
- **Non-const globals**: `counter = 0`, `global total = 10`. A script variable
  is not a declaration of anything stable.
- **Definitions built by `@eval`**: `@eval f(x) = ...` and `@eval function ...`
  are read as the short or long form when the macro wraps one directly, but
  names generated in a loop (`for op in (:a, :b) @eval $op(x) = ... end`) are
  interpolated and not extracted.
- **Extensionless Julia scripts** with a `#!` line are not detected as Julia
  (language detection, outside this fixture).
- **A file the grammar gives up on.** When one construct cannot be read, the
  root of the tree is an `ERROR` and the file's items are its children. The
  long-form `function` and `const` that could still be read are found as ever;
  the short-form methods (`f(x) = ...`) and a header whose body was lost
  (`function name(args)` as a `function` keyword and a `signature` with no
  `function_definition` around them) are found only as direct children of that
  root `ERROR` (or of an `ERROR` directly in the source file), not in an
  `ERROR` deeper down, where a short form is as likely a local closure:

  ```
  (ERROR (line_comment) ... (assignment (call_expression ...) (operator) ...)
         (function_definition ...) (ERROR "function" (signature ...) ...))
  ```

  The construct that does it in StatsBase's `signalcorr.jl` is a ternary whose
  else-branch is a parenthesised `\` expression that is then indexed,
  `r[i,j] = l == 0 ? 1 : (a \ (b))[end]` (without the index the error stays
  local). Everything after the break is read as the rows of a matrix
  (`matrix_row`), so a short-form method after it is lost; the long forms,
  structs and consts after it are still found. A definition inside the broken
  function's body is not extracted.
