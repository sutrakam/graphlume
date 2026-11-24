# Protocol Buffers: known gaps

What is not in `expected.tsv` and why.

- **Proto2 `group` fields** (`optional group Result = 2 { optional string url = 3; }`)
  make the grammar report a syntax error from the group on, so the rest of the
  enclosing message and file may be missed and the file is reported partial.
  The group is deprecated and absent from proto3; the tree for
  `group Result = 2 {` is an `ERROR` node followed by a stray `}`:

  ```
  message_body (extensions) (extend) (ERROR) ... (ERROR "}")
  ```
- **Fields, `oneof` groups and enum values are not definitions here.** They
  belong to the message or enum that holds them, and a schema of a few hundred
  messages has a few thousand of them (the Kubernetes `core/v1/generated.proto`
  alone has 238 messages and enums and several thousand fields). A decision,
  not a limitation: the query could capture `(field (identifier))`,
  `(oneof (identifier))` and `(enum_field (identifier))` as they were captured
  before for enum values. The one exception is a field declared inside an
  `extend` block, which adds a name to the package that no message declares
  (custom options, proto2 extensions).
- **The `extend` block itself** has no name (`extend google.protobuf.MessageOptions`
  names the message being extended, which is declared elsewhere).
- **`package`, `import`, `option`, `reserved`, `extensions`** declare nothing
  indexable.
