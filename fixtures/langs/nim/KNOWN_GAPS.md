# Nim: forms that are not extracted

- **Tuple deconstruction**: `let (a, b) = pair` and `var (_, name, ext) = splitFile(p)`.
  The names sit in a `tuple_deconstruct_declaration`, not in a
  `symbol_declaration_list`, and the targets are values, not declarations a
  reader would look up.
- **Types, constants, let and var in nested scopes**: inside a routine they are
  locals; inside a `when` that is itself inside another block, or a `when`
  nested in a `when`, only the first level of `when` under the file is read.
  Routines are the exception and are read at any depth.
- **Enum members and object fields**: `kA`, `x*` are not definitions.
- **Definitions made by a template or macro expansion** (`defineTypes(Foo)`):
  only the call is in the tree.
- **A tuple written as a bare list** (`type Tup = (int, string)`) is a
  `tuple_construction`, the same node as a value, and is recorded as a
  type_alias; `tuple[a: int]` is recorded as a class. The two spell the same
  kind of type, the grammar gives them different nodes.
- **Nimble task blocks** (`task build, "...":`) and `runnableExamples:` are
  calls with a body, not declarations.
