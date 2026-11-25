package parse

// SCSS. What the index holds, see fixtures/langs/scss/expected.tsv:
//
//   - @mixin and @function names are a function;
//   - a variable ($name: value) assigned at the top level of the file, and a
//     custom property (--name: value) wherever it is declared, are a variable.
//     A $variable inside a mixin, function, rule or control block is local and
//     is not captured;
//   - a %placeholder rule set, @keyframes, @property, @counter-style and
//     @layer names are a class;
//   - a top-level rule set whose whole selector is one bare .class or #id is a
//     class. Descendant, compound, listed or nested selectors are uses, and
//     so is any class or id whose name contains interpolation
//     (.#{$prefix}navbar): the name is only known after compilation.
//
// When the grammar fails inside a mixin or function (a call with a named
// argument such as color.channel($c, "hue", $space: hsl) does), the name is
// read from the @mixin or @function token inside the error node.
func init() {
	registerDefinitions("scss", scssDefinitions, true)
}

const scssDefinitions = `
(mixin_statement (identifier) @name) @definition.function

(function_statement (identifier) @name) @definition.function

(ERROR
  ["@mixin" "@function"] . (identifier) @name @definition.function)

(stylesheet
  (declaration
    (property_name) @name
    (#match? @name "^\\$")) @definition.variable)

(ERROR
  (declaration
    (property_name) @name
    (#match? @name "^\\$")) @definition.variable)

(declaration
  (property_name) @name
  (#match? @name "^--")) @definition.variable

(rule_set
  (selectors . (placeholder (identifier) @name) .)) @definition.class

(keyframes_statement (keyframes_name) @name) @definition.class

(at_rule
  (at_keyword) @kw
  (keyword_query) @name
  (#match? @kw "^@(property|counter-style|layer)$")) @definition.class

(stylesheet
  (rule_set
    (selectors . (class_selector . (class_name) @name .) .)
    (#not-match? @name "[#{}]")) @definition.class)

(stylesheet
  (rule_set
    (selectors . (id_selector (id_name) @name) .)
    (#not-match? @name "[#{}]")) @definition.class)

(ERROR
  (rule_set
    (selectors . (class_selector . (class_name) @name .) .)
    (#not-match? @name "[#{}]")) @definition.class)

(ERROR
  (rule_set
    (selectors . (id_selector (id_name) @name) .)
    (#not-match? @name "[#{}]")) @definition.class)
`
