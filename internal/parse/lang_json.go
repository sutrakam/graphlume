package parse

// JSON. What the index holds, see fixtures/langs/json/expected.tsv: the schema
// names a JSON document declares at its top level, each a type alias:
//
//   - the entries of a root "$defs" or "definitions" object (JSON Schema,
//     Swagger 2.0);
//   - the entries of "components"."schemas" at the root (OpenAPI 3).
//
// These names are declared once in a file and referred to by name from other
// places ("$ref": "#/$defs/Customer"). Nothing else in JSON is a definition:
// the configuration analyser (internal/analyze/config) already reports every
// key of a configuration .json file, and keys such as name, version or type
// are in nearly every document. A "$defs" or "definitions" that is not a direct
// member of the root object (nested sub-schemas, or a property that happens to
// be called "definitions") is deliberately not matched: restricting to the root
// is what keeps an ordinary object's keys out.
func init() {
	registerDefinitions("json", jsonDefinitions, false)
}

const jsonDefinitions = `
((document
  (object
    (pair
      (string (string_content) @kw)
      (object
        (pair . (string . (string_content) @name .)) @definition.type_alias))))
 (#match? @kw "^(\\$defs|definitions)$"))

((document
  (object
    (pair
      (string (string_content) @kw1)
      (object
        (pair
          (string (string_content) @kw2)
          (object
            (pair . (string . (string_content) @name .)) @definition.type_alias))))))
 (#eq? @kw1 "components")
 (#eq? @kw2 "schemas"))
`
