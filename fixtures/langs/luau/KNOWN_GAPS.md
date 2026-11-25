# Luau: forms that are not extracted

Everything listed for Lua in `../lua/KNOWN_GAPS.md` applies here too (class
objects built by a call, plain constants, fields assigned inside function
bodies, tables passed as arguments). In addition:

- **Anything after a generic function type.** The grammar cannot read
  `Some: <T>(value: T) -> T` (a function type that declares its own type
  parameters) and does not recover: the rest of the file becomes one ERROR
  node, so every definition after it is lost. Tree fragment for
  `type Fns = { Some: <T>(value: T) -> T }` followed by `local function after() end`:

  ```
  (type_definition (identifier) (object_type (identifier) (generic_type (identifier) (identifier))))
  (ERROR (ERROR (function_call (method_index_expression (identifier) (ERROR (identifier) (identifier) (identifier)) ...
  ```

  The alias itself is still found; `after` and what follows are not. The
  extraction reports the file as `partial`.
- **Cast expressions that name an object type at the end of a file**
  (`return (Option :: any) :: { Some: <T>(value: T) -> Option<T> }`) break the
  parse in the same way; nothing before them is lost.
- **A generic parameter with a default** (`type Gen<T, U = string> = T`) gives an
  ERROR node inside the alias. The name is still captured, the file is
  reported as `partial`.
