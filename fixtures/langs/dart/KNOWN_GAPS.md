# Dart: known gaps

## A class with an annotation starts on the annotation line

`@Deprecated('...') class Optional<T> {` and `@freezed abstract class Equals`
are recorded at the annotation's line, because the grammar makes the metadata
the first child of `class_definition`. The name and kind are right. Functions,
methods and variables are not affected: their metadata is a sibling node.

```
(class_definition (annotation ...) (abstract) (identifier) ...)
```

## An unnamed extension has no symbol

`extension on int { ... }` declares no name, so there is nothing to capture;
its members are recorded as methods.

```
(extension_declaration (type_identifier) (extension_body ...))
```

## The primary constructor of an extension type is not a separate symbol

`extension type Meters(double value)` is recorded once, as the class `Meters`.
Its representation field and primary constructor are the same tokens as the
declaration, not nodes of their own (`representation_declaration`).

## A symbol's range is only the signature

The grammar does not wrap a declaration: a function is a `function_signature`
followed by a sibling `function_body`, a file-level variable is a type node
followed by an `initialized_identifier_list`. The recorded start line is right,
but the range ends with the signature, so a call inside a body is not
attributed to the function that contains it by line range.

## Not recorded by design

`library` and `part of` directives, imports and exports, locals, parameters
(including field-formal `this.x` parameters), and anonymous functions.

## Return types (internal/parse/returns.go)

A function or method records the type written before its name, and `var y = make()`
links `y` to that call, so `y.f()` binds through what `make` returns. A function
body is now a scope of its own, so a method's locals no longer leak into the class
as fields. Only a bare call is linked, not `a.b()`; a `void` or untyped function
records nothing.
