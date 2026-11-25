package parse

// CSS. What the index holds, see fixtures/langs/css/expected.tsv:
//
//   - a custom property declaration (--name: value) is a variable, once per
//     declaration, wherever it sits;
//   - @keyframes names are a class;
//   - @property, @counter-style and @layer names are a class, and so is the
//     font-family a @font-face declares;
//   - a rule set whose whole selector is one bare class (.card) or id (#main)
//     and that sits at the top level of the file is a class. A class used in
//     a descendant, compound or listed selector, or in a nested rule, is a
//     use of the name and is not captured.
func init() {
	registerDefinitions("css", cssDefinitions, true)
}

const cssDefinitions = `
(declaration
  (property_name) @name
  (#match? @name "^--")) @definition.variable

(keyframes_statement (keyframes_name) @name) @definition.class

(at_rule
  (at_keyword) @kw
  (keyword_query) @name
  (#match? @kw "^@(property|counter-style|layer)$")) @definition.class

(at_rule
  (at_keyword) @kw
  (block
    (declaration
      (property_name) @prop
      (string_value (string_content) @name)))
  (#eq? @kw "@font-face")
  (#eq? @prop "font-family")) @definition.class

(stylesheet
  (rule_set
    (selectors . (class_selector . (class_name (identifier) @name) .) .)) @definition.class)

(stylesheet
  (rule_set
    (selectors . (id_selector (id_name) @name) .)) @definition.class)

(ERROR
  (rule_set
    (selectors . (class_selector . (class_name (identifier) @name) .) .)) @definition.class)

(ERROR
  (rule_set
    (selectors . (id_selector (id_name) @name) .)) @definition.class)
`
