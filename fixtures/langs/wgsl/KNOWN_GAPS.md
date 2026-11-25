# WGSL known gaps

The bundled grammar is older than the WGSL specification. It has no node for
`alias`, `const` or `requires`, and it fails on some current syntax.

## `alias` and `const` are read from an ERROR node

`alias Vec3 = vec3<f32>;` and `const PI = 3.14;` parse as

```
(ERROR (identifier) (identifier))
```

The keyword is the first identifier and the name the second. The query matches
that shape directly under `source_file`, with the keyword's text compared to
`alias` or `const`, so function-local `const` (which sits inside a function
body) is not captured. This is the only way to see these two forms, but it
means they are found only while the grammar recovers the same way.

## Module-scope `let` is not extracted

Before 2022 WGSL spelled a module-scope constant `let x = 1;`. The grammar
parses that as `global_constant_declaration`, the same node `override` uses.
The query takes only the `override` ones, because when the grammar fails inside
a function body (see below) the function's own `let` statements are re-read as
module-scope `global_constant_declaration` nodes, and that produced eight
false constants in a real file. Current WGSL has no module-scope `let`.

## Line of an attributed function

`@vertex` on the line above `fn vs_main` is part of the declaration. Which line
the symbol gets depends on how the grammar recovered: the attribute's line when
the file parses cleanly, the `fn` line when an earlier construct produced an
error. The name and kind are right in both cases; the fixture's file has errors
on purpose, so its list holds the `fn` line.

## Constructs the grammar cannot read

`switch` with `case` labels that are not literals, `#import` preprocessor lines
and some attribute forms put an ERROR node in the tree. Definitions after the
error are normally still found, those inside the unreadable region may be
missing, and the file is reported as `partial`.
