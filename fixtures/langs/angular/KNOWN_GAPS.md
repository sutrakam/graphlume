# Angular: no definition query is registered

An Angular template declares almost nothing. The candidates were template
reference variables (`#name`), `@let` declarations, and `ng-template` names. None
is a definition a developer would search a code index for, so no query is
registered and there is no `expected.tsv`.

## What the tree has

```
@let total = price * qty;
  (let_statement (control_keyword "let")
    (assignment_expression (identifier "total") (binary_expression ...)))

<input #search type="text" />
  (self_closing_tag (tag_name "input")
    (attribute (attribute_name "#search")) ...)

<ng-template #empty let-msg="message">
  (start_tag (tag_name "ng-template")
    (attribute (attribute_name "#empty"))
    (attribute (attribute_name "let-msg") (quoted_attribute_value ...)))
```

- A reference variable is an `attribute_name` leaf whose text starts with `#`.
  The grammar has no node for the name alone, so a query could only name the
  symbol `#search`, and `attribute_name` is also the node for `class`, `type`
  and every other attribute, so the only way to pick references out is a text
  match on the leading `#`.
- `@let` has a clean `identifier` for its name, but it is a variable that exists
  only inside the one template.
- `ng-template` is named only through the same `#name` attribute.

## Why none of them is a definition

- They are scoped to one template file. Nothing outside it can import, call or
  inherit from them, so there is no cross-file relationship for the graph to
  hold. The things an Angular template uses (components, directives, pipes,
  inputs, outputs) are declared in TypeScript, which has its own grammar.
- In a real application they are rare. In the 11 `.component.html` files of the
  RealWorld Angular example app (`gothinkster/angular-realworld-example-app`)
  there is not one reference variable, `@let` or `ng-template`, and no
  `@ViewChild` in its TypeScript. Components, which that app has plenty of, are
  declared in `.ts` files.

## The grammar is not reached by extension

The angular grammar registers no file extension in gotreesitter, and Angular
templates are ordinary `.html` files (`*.component.html`), so they go to the
html grammar and the angular parser is never chosen for a repository file. A
query registered for it would only run when the grammar is named. Telling an
Angular template from other HTML would need a content check in
`internal/langdetect`, which is outside this change.
