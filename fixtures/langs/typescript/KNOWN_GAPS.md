# TypeScript known gaps

## Recovered by a mask (internal/parse/mask_typescript.go)

A variable named `using` (`const using = ...; using.length`) is read as the start of
a resource-management declaration and costs the rest of the block. Each such
`using` is rewritten as `usinG`, the same length; `using x = ...`, comments and
strings are left alone.

## Known and not recovered

One real file (pulse's `sku-catalog.ts`) fails as a whole although each statement
in it parses on its own; the cause was not found. A file whose own source is broken
(an `import {` inside another import, an unescaped apostrophe in a string, a
corrupt byte in a comment) is reported partial and nothing can be recovered.
