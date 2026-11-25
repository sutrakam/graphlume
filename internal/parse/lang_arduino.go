package parse

import "strings"

// Arduino. The grammar is C++ with sketch conventions (setup, loop, .ino).
// What the index holds, see fixtures/langs/arduino/expected.tsv.
//
// The inferred query takes function definitions, typedefs and named
// classes and structs, and nothing else: no global (pin numbers, state
// variables and objects such as `Servo myServo;` are most of a sketch's named
// declarations), no #define, no enum, no union, no `using`, no method
// declaration in a class. It also records a struct as a "type". It is replaced.
//
//   - a function definition or prototype is a function; the same inside a
//     class body, or with a qualified name (`int Sensor::read()`), is a
//     method. Constructors, destructors (named with the ~) and operators are
//     methods;
//   - class, struct, union and enum (also `enum class`) with a body are
//     classes; `typedef struct { } Name;` is a class named Name; any other
//     typedef and `using Name = ...` is a type_alias; a namespace is a
//     namespace;
//   - #define NAME is a constant and #define NAME(args) is a function;
//   - a global (file or namespace scope) declaration is a constant when its
//     type says const or constexpr and a variable otherwise; arrays and
//     pointers count, `int a = 1, b = 2;` gives both, a prototype does not;
//   - a field of a class, struct or union is a field;
//   - locals, parameters, enumerators and forward declarations of types
//     (`class Foo;`) are not definitions.
func init() {
	registerDefinitions("arduino", arduinoDefinitions(), true)
}

func arduinoDefinitions() string {
	// The name of a function in a declarator.
	fname := `[
    (identifier) @name
    (field_identifier) @name
    (destructor_name) @name
    (operator_name) @name
    (qualified_identifier (identifier) @name .)
    (qualified_identifier (destructor_name) @name .)
    (qualified_identifier (operator_name) @name .)
  ]`
	// A function declarator, possibly under pointer or reference declarators
	// (`char *name()`, `Foo &get()`).
	fdecl := "(function_declarator . " + fname + ")"
	shapes := []string{
		fdecl,
		"(pointer_declarator " + fdecl + ")",
		"(reference_declarator " + fdecl + ")",
		"(pointer_declarator (pointer_declarator " + fdecl + "))",
	}
	// A qualified name is a method wherever it is written.
	qual := `(function_declarator . [
    (qualified_identifier (identifier) @name .)
    (qualified_identifier (destructor_name) @name .)
    (qualified_identifier (operator_name) @name .)
  ])`
	qualShapes := []string{qual, "(pointer_declarator " + qual + ")", "(reference_declarator " + qual + ")"}

	var b strings.Builder

	for _, s := range shapes {
		b.WriteString("(function_definition " + s + ") @definition.function\n")
		b.WriteString("(translation_unit (declaration " + s + ") @definition.function)\n")
		b.WriteString("(namespace_definition (declaration_list (declaration " + s + ") @definition.function))\n")
		b.WriteString("(field_declaration_list (function_definition " + s + ") @definition.method)\n")
		b.WriteString("(field_declaration_list (declaration " + s + ") @definition.method)\n")
		b.WriteString("(field_declaration_list (field_declaration " + s + ") @definition.method)\n")
	}
	for _, s := range qualShapes {
		b.WriteString("(function_definition " + s + ") @definition.method\n")
	}

	b.WriteString(`
(class_specifier . (type_identifier) @name (field_declaration_list)) @definition.class
(struct_specifier . (type_identifier) @name (field_declaration_list)) @definition.class
(union_specifier . (type_identifier) @name (field_declaration_list)) @definition.class
(enum_specifier . (type_identifier) @name (enumerator_list)) @definition.class
(namespace_definition . (namespace_identifier) @name) @definition.namespace

(type_definition
  [(struct_specifier (field_declaration_list)) (union_specifier (field_declaration_list))
   (enum_specifier (enumerator_list)) (class_specifier (field_declaration_list))]
  . (type_identifier) @name .) @definition.class
(type_definition
  . [(primitive_type) (sized_type_specifier) (type_identifier) (qualified_identifier) (template_type)]
  . (type_identifier) @name .) @definition.type_alias
(type_definition
  (function_declarator (parenthesized_declarator (pointer_declarator (type_identifier) @name)))) @definition.type_alias
(alias_declaration . (type_identifier) @name) @definition.type_alias

(preproc_def . (identifier) @name) @definition.constant
(preproc_function_def . (identifier) @name) @definition.function

(field_declaration_list (field_declaration (field_identifier) @name) @definition.field)
(field_declaration_list (field_declaration (pointer_declarator (field_identifier) @name)) @definition.field)
(field_declaration_list (field_declaration (array_declarator . (field_identifier) @name)) @definition.field)
`)

	// Globals. A declarator is a bare name, an array, or a pointer, with or
	// without an initialiser.
	names := []string{
		"(identifier) @name",
		"(array_declarator . (identifier) @name)",
		"(array_declarator (array_declarator . (identifier) @name))",
		"(pointer_declarator (identifier) @name)",
		"(init_declarator . (identifier) @name)",
		"(init_declarator . (array_declarator . (identifier) @name))",
		"(init_declarator . (array_declarator (array_declarator . (identifier) @name)))",
		"(init_declarator . (pointer_declarator (identifier) @name))",
	}
	// File scope is also what sits inside #if, #ifdef and their #else and
	// #elif branches, two levels deep.
	scopes := []string{
		"(translation_unit %s)",
		"(namespace_definition (declaration_list %s))",
		"(linkage_specification (declaration_list %s))",
	}
	cond := []string{"preproc_if", "preproc_ifdef"}
	branch := []string{"preproc_else", "preproc_elif", "preproc_if", "preproc_ifdef"}
	for _, c := range cond {
		scopes = append(scopes, "(translation_unit ("+c+" %s))")
		for _, br := range branch {
			scopes = append(scopes, "(translation_unit ("+c+" ("+br+" %s)))")
		}
	}
	for _, scope := range scopes {
		for _, n := range names {
			plain := "(declaration " + n + ") @definition.variable"
			konst := "(declaration (type_qualifier) @q " + n + " (#match? @q \"^(const|constexpr)$\")) @definition.constant"
			b.WriteString(strings.Replace(scope, "%s", plain, 1) + "\n")
			b.WriteString(strings.Replace(scope, "%s", konst, 1) + "\n")
		}
	}
	return b.String()
}
