# Hare: forms that are not extracted

- **The second and later names in a comma-separated `type` or `def`
  declaration**: `type a = int, b = str;` and `def P: int = 1, Q: int = 2;`.
  The grammar does not group each name with its type: the declaration is one
  node holding a flat run of children, so `b` and `Q` are bare `identifier`
  siblings that look the same as an identifier used as a type or a value:

  ```
  (type_declaration (identifier) (builtin_type) (identifier) (builtin_type))
  (constant_declaration (identifier) (builtin_type) (number) (identifier) (builtin_type) (number))
  ```

  Only the first name is a definition here. A pattern for the others would
  also match `type a = b, c = d;` wrongly. (A comma-separated `let` or `const`
  is read correctly, because each binding is its own `global_binding`.)
- **Enum members and struct fields** are not definitions.
- **Locals** (`let`, `const` inside a function body) are not definitions.
- The extraction records no `@test` marker: a test function is an ordinary
  function named as written.
