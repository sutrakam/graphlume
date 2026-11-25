package parse

// Less. What the index holds, see fixtures/langs/less/expected.tsv:
//
//   - a mixin definition, .name(...) { }, is a function, wherever it sits (a
//     namespace such as #ns { .m() { } } or inside a rule);
//   - an @variable (or detached ruleset) assigned at the top level of the file,
//     and a custom property (--name: value) wherever it is declared, are a
//     variable. An @variable inside a rule or @media block is local and is not
//     captured;
//   - @keyframes names, and a top-level rule set whose whole selector is one
//     bare .class or #id (which includes a namespace), are a class. Descendant,
//     compound, listed and nested selectors are uses.
//
// A plain .class { } rule that is called as a mixin looks the same as a rule
// set, so it is a class here, not a function.
func init() {
	registerDefinitions("less", lessDefinitions, true)
}

const lessDefinitions = `
(mixin_def (class_selector (class_name) @name)) @definition.function

(stylesheet
  (variable_def . (variable) @name) @definition.variable)

(ERROR
  (variable_def . (variable) @name) @definition.variable)

(declaration
  (property_name) @name
  (#match? @name "^--")) @definition.variable

(keyframes_statement (keyframes_name) @name) @definition.class

(stylesheet
  (rule_set
    (selectors . (class_selector . (class_name) @name .) .)) @definition.class)

(stylesheet
  (rule_set
    (selectors . (id_selector (id_name) @name) .)) @definition.class)

(ERROR
  (rule_set
    (selectors . (class_selector . (class_name) @name .) .)) @definition.class)

(ERROR
  (rule_set
    (selectors . (id_selector (id_name) @name) .)) @definition.class)
`
