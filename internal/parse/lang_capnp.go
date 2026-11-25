package parse

// Cap'n Proto. The inferred query names type_definition, which is only the
// alias half of a `using` directive, so it is replaced.
//
// What is a definition: struct, enum and named union or group (class),
// interface (interface), interface methods (method), const (constant),
// annotation declarations and `using Name = Type` aliases (type_alias).
// Fields, enumerants, union members, parameters and `using X = import "..."`
// import bindings are not.
func init() {
	registerDefinitions("capnp", `
(struct (type_identifier) @name) @definition.class
(enum (enum_identifier) @name) @definition.class
(union (type_identifier) @name) @definition.class
(group (type_identifier) @name) @definition.class
(interface (type_identifier) @name) @definition.interface
(method (method_identifier) @name) @definition.method
(const (const_identifier) @name) @definition.constant
(annotation (annotation_definition_identifier) @name) @definition.type_alias
(replace_using (type_definition) @name) @definition.type_alias
`, true)
}
