# R: known gaps

R has no declaration syntax, so every definition here is an assignment or a
call that happens to define something.

## Plain assignments are not extracted

`max_items <- 10` and `values <- c(1, 2, 3)` are a `binary_operator` with an
`identifier` on the left and any expression on the right. They are not
extracted: in R they are script state, and a query that matched them would also
need to tell `x <- function(...)` apart from them (a variable outranks a
function when both match). A deliberate choice, not a grammar limit.

## Local and conditional definitions

Only assignments directly under the program, or directly in the braces of a
top-level `if`, are definitions. A function assigned inside another function
(`helper <- function(y) y + 1` in a body), inside a `for` loop, or inside
`local({ ... })` is not extracted.

## Classes and methods are recognised by call name

`setClass`, `setRefClass` and `R6Class` give a class; `setGeneric` a function;
`setMethod` a method; and the `methods` list of `setRefClass`, or the `public`,
`private` and `active` lists of `R6Class`, give methods. Wrappers around these
(`my_setClass(...)`), classes built with `new_class` (S7), and classes whose
name is not a string literal are not recognised. `setValidity`, `setClassUnion`
and `setAs` are not extracted. R5 `fields` and R6 data members are not
extracted.

## Names are as written

A backtick name keeps its backticks (`` `%+%` ``). A quoted name
(`"quoted_fn" <- function() NULL`) is recorded without its quotes.
