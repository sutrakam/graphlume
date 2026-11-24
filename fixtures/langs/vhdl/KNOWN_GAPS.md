# VHDL: known gaps

## Subprogram bodies lost to a grammar error on `'attribute-1 downto 0`

A constraint that does arithmetic on an attribute, such as
`alias b : std_ulogic_vector(Binary'length-1 downto 0) is Binary;` in a
subprogram's declarative part, is a syntax error in this grammar. Error recovery
then reads the function header as a `subprogram_declaration` inside an `ERROR`
node, and the body that follows is no longer a `subprogram_definition`, so the
function is not extracted. Later subprograms in the same package body can be
lost with it.

```
(package_definition_body
  (subprogram_declaration (function_specification (identifier) ...)
    (ERROR (identifier) (subtype_indication (name (library_type)
      (ERROR (ERROR)) (attribute (attribute_function)))) ...)))
```

Matching `subprogram_declaration` inside a package body would recover the first
function, but it would also match forward declarations and would still lose the
ones after it, so the query does not do it.

## Not extracted by choice

Subprogram prototypes in a package header, ports, generics, variables, locals,
instantiation labels, aliases, attribute declarations, and signals declared
inside a `generate` or `block` (they are not in an `architecture_head`).
