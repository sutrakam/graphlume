# C++: known gaps

## `using` aliases

`using Callback = int (*)(int);` is an `alias_declaration`; the query has no
pattern for it. `typedef` is extracted.

## Declarations

A method declared in a class and defined elsewhere is recorded once, where it
is defined. A prototype without a body is never a definition here.

## Conditional compilation

An `#if` group that cuts a declaration apart (see `conditional.cc`) is read
with one branch: the first that is not `#if 0`, with two groups that test one
macro read in the same world. A definition that exists only in a branch that
is not kept is not extracted; the number of branches blanked is in the file's
stored parse text. A group whose branches are each a complete declaration
is not touched, and both branches are extracted.

Not helped: a macro call written as a statement with braces in its arguments
(`BM_DEFINE_kvpairs(benchmark_context, {});`), which leaves one error node
over everything before it, and a long run of `FMT_`-style macro calls with no
semicolon before an `#if`.
