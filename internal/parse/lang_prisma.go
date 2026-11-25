package parse

// Prisma. What the index holds, see fixtures/langs/prisma/expected.tsv: every
// named block. model and view are a class, so is a composite type and an enum,
// and datasource and generator are a variable (a named configuration the
// schema declares). Fields are not extracted, nor are enum values, attributes
// (@id, @@map, @relation), the keys inside a datasource or generator, and
// calls such as env() or autoincrement().
//
// The inferred query is replaced: it tags enum_declaration with a kind that
// does not exist (type) and records every call_expression, which in a schema
// is an attribute function or env(), as a call.
func init() {
	registerDefinitions("prisma", prismaDefinitions, true)
}

const prismaDefinitions = `
(model_declaration . (identifier) @name) @definition.class
(view_declaration . (identifier) @name) @definition.class
(type_declaration . (identifier) @name) @definition.class
(enum_declaration . (identifier) @name) @definition.class
(datasource_declaration . (identifier) @name) @definition.variable
(generator_declaration . (identifier) @name) @definition.variable
`
