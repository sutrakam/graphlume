package parse

// HTML. What the index holds, see fixtures/langs/html/expected.tsv: an element
// that carries an id attribute is a variable named by the id value, without
// quotes, on the line its start tag begins. An id is unique within a document
// and is how scripts, stylesheets and links ("#content") name an element, so
// it is the one thing in markup a developer looks up by name.
//
// Not captured: tag names (a custom element such as <my-widget> is not a
// definition), class and name attributes, data-* attributes, label for and
// anchor targets, an empty id, an id with white space (not a valid id), and an
// id value that is template syntax ({{ }}, ${ }, <% %>, @). Elements inside
// <script>, <style> and comments are raw text to the grammar and are not seen.
func init() {
	registerDefinitions("html", htmlDefinitions, false)
}

const htmlDefinitions = `
(start_tag
  (attribute
    (attribute_name) @attr
    (quoted_attribute_value (attribute_value) @name))
  (#match? @attr "^[iI][dD]$")
  (#match? @name "^[^\\s{}<>$%@]+$")) @definition.variable

(start_tag
  (attribute
    (attribute_name) @attr
    (attribute_value) @name)
  (#match? @attr "^[iI][dD]$")
  (#match? @name "^[^\\s{}<>$%@]+$")) @definition.variable

(self_closing_tag
  (attribute
    (attribute_name) @attr
    (quoted_attribute_value (attribute_value) @name))
  (#match? @attr "^[iI][dD]$")
  (#match? @name "^[^\\s{}<>$%@]+$")) @definition.variable

(self_closing_tag
  (attribute
    (attribute_name) @attr
    (attribute_value) @name)
  (#match? @attr "^[iI][dD]$")
  (#match? @name "^[^\\s{}<>$%@]+$")) @definition.variable
`
