# Arduino: forms that are not extracted

- **Declarations deeper than two levels of `#if` nesting** are not read as file
  scope: the patterns cover a declaration directly in a file, a namespace, an
  `extern "C"` block, and inside one `#if`/`#ifdef` with an `#else`/`#elif`
  branch or a second nested `#if`. Functions, types and macros are found at any
  depth.
- **A `const char *p`** is recorded as a constant, although only the pointee is
  const; the grammar gives the same `type_qualifier` node either way.
- **Static data members defined outside their class** (`int Sensor::count = 0;`):
  the declarator is a qualified identifier, not a name; the in-class
  declaration (`static int count;`) is the field that is extracted.
- **Enumerators** (`OFF`, `ON`) and **function-pointer variables**
  (`void (*cb)(int) = 0;`) are not definitions here; a function-pointer
  typedef is.
- **Prototypes are recorded as well as definitions**, so a function declared
  and defined in one sketch appears twice (once per line).
