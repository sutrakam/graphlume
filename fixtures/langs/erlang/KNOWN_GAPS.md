# Erlang: forms not extracted

- **-callback** declarations (the interface a behaviour requires) are not
  recorded. They have no body and no kind of their own among the kinds the
  index uses.
- **-spec** is not recorded: it describes a function that is defined elsewhere.
- **Preprocessor conditionals** (`-ifdef`, `-else`, `-endif`) are not
  understood; the functions inside every branch are recorded.
- **Macros defined with `-define(?NAME, ...)`-style names that are not an atom
  or a variable** are not recorded.
