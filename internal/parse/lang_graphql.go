package parse

// GraphQL. What the index holds, see fixtures/langs/graphql/expected.tsv:
//
//   - object types, input types, enums and unions are a class;
//   - interfaces are an interface;
//   - scalars are a type alias (a name for a leaf type);
//   - directive definitions are a function;
//   - named operations (query X, mutation X, subscription X) and fragments
//     are a function. An anonymous operation has no name and is skipped.
//
// Fields, arguments, enum values and input fields are not definitions. An
// `extend type X` adds to X and does not define it again, so extensions are
// not captured.
//
// The grammar puts a type's """description""" inside the type node, in front
// of the keyword, so the node starts on the description's line. A type
// without a description is captured as the whole node. For one with a
// description the name is captured instead, which puts the symbol on the line
// of the keyword and name but spans only the name: the grammar has no node for
// the declaration without its description.
func init() {
	registerDefinitions("graphql", graphqlDefinitions, true)
}

const graphqlDefinitions = `
(object_type_definition . (name) @name) @definition.class
(object_type_definition (description) . (name) @name @definition.class)
(input_object_type_definition . (name) @name) @definition.class
(input_object_type_definition (description) . (name) @name @definition.class)
(enum_type_definition . (name) @name) @definition.class
(enum_type_definition (description) . (name) @name @definition.class)
(union_type_definition . (name) @name) @definition.class
(union_type_definition (description) . (name) @name @definition.class)
(interface_type_definition . (name) @name) @definition.interface
(interface_type_definition (description) . (name) @name @definition.interface)
(scalar_type_definition . (name) @name) @definition.type_alias
(scalar_type_definition (description) . (name) @name @definition.type_alias)
(directive_definition . (name) @name) @definition.function
(directive_definition (description) . (name) @name @definition.function)
(operation_definition (operation_type) . (name) @name) @definition.function
(fragment_definition (fragment_name (name) @name)) @definition.function
`
