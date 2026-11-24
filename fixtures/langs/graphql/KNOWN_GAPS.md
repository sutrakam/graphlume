# GraphQL: forms not extracted

- **A type's span when it has a description.** The grammar puts the
  `"""description"""` inside the type node, before the keyword, so the node
  starts on the description's line. To report the keyword's line the query
  captures the name instead for a type that has a description, and the symbol
  then spans the name only, not the body. A type without a description spans
  the whole declaration. The grammar offers no node for the declaration without
  its description.
- **Extensions** (`extend type X`, `extend enum E`, ...) are not recorded: they
  add to a type declared elsewhere.
- **Anonymous operations** (`query { ... }`, `{ ... }`) have no name.
- **Fields, arguments, enum values, input fields, variables and the `schema { }`
  block** are not definitions.
