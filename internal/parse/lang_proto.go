package parse

// Protocol Buffers. What the index holds, see fixtures/langs/proto/expected.tsv.
//
// The inferred query tags message, enum, service and rpc correctly but with
// kinds the index does not know (message, service and rpc became `function`),
// so listing classes returned almost nothing; and it also tags every enum
// value. It is replaced.
//
//   - message is a class, nested messages included;
//   - enum is a class, nested enums included;
//   - service is an interface, and each rpc in it is a method;
//   - a field declared inside an `extend` block (a custom option or an
//     extension of another message) is a field: it adds a name to the package
//     that no message declares.
//
// Not definitions: ordinary message fields and oneof groups (they belong to the
// message that holds them and a large schema would add thousands of them),
// enum values, `reserved`, `option`, and the `extend` block itself, which has
// no name of its own.
func init() {
	registerDefinitions("proto", protoDefinitions, true)
}

const protoDefinitions = `
(message (message_name (identifier) @name)) @definition.class
(enum (enum_name (identifier) @name)) @definition.class
(service (service_name (identifier) @name)) @definition.interface
(rpc (rpc_name (identifier) @name)) @definition.method
(extend (message_body (field (identifier) @name) @definition.field))
`
