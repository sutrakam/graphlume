package parse

// Circom. The inferred query already captures function_definition and calls,
// so this adds to it rather than replacing it.
//
// What is a definition: template (class), function (function, from the
// inferred query), and the `component main` entry point (function, named
// main). Signals, variables and component instances inside a template are
// locals of that template and are not definitions.
//
// The grammar lexes the identifier after `template` with the white space in
// front of it when both are on one line, so `template Num2Bits(n)` captures
// " Num2Bits". cleanName trims that for every language, which is what makes
// the usual one-line form usable here.
func init() {
	registerDefinitions("circom", `
(template_definition (identifier) @name) @definition.class
(main_component_definition "main" @name) @definition.function
`, false)
}
