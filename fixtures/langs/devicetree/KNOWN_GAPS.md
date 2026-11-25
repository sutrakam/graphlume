# Devicetree known gaps

## The unit address is not part of the name

`memory@80000000 { }` and `cpu@0 { }` are indexed as `memory` and `cpu`. The
unit address is a separate `unit_address` node, and a symbol name is the text
of one node, so the two cannot be joined by a query. `cpu@0` and `cpu@1` are
therefore two symbols called `cpu`, told apart by line.

## A label equal to the node name

`ethernet: ethernet@10100000 { }` has the label `ethernet` and the node name
`ethernet`. Both are captured, but they share a line and a name, and the index
keeps one symbol for that pair (the node, as a class). The label is not
separately searchable in that case. Labels that differ from the node name
(`uart0: serial@...`) are indexed.

## Overrides are uses

`&uart0 { }` and `&{/soc/ethernet@10100000} { }` modify a node defined
elsewhere and are not definitions. `/delete-node/` is not one either.

## Partial parses

Files that mix preprocessor lines or unusual property syntax can come back
partial; a node inside a region the parser could not read can be missing.
