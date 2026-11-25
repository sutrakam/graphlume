# Elixir: known gaps

## Clauses are folded by adjacency and head arity, not by the compiler's rules

The query matches one clause head at a time. The parser folds consecutive
clauses of one name into one symbol (`registerClauseMerge`), so
`def handle_cast({:push, item}, state)` and `def handle_cast(:clear, _state)`
are one symbol that spans both, as is a bodiless head such as `def floor(number,
precision \\ 0)` with the clauses that follow it.

Two limits. The arity is the number of arguments written in the head, so
`def f(a, b \\ 1)` is f/2 and a separate `def f(a)` is f/1, which is how the
language numbers them. And "consecutive" means no other symbol of the module
lies between the clauses: a clause of `f` that follows `g` is a second symbol
named `f`, as are clauses of `f/1` and `f/2`. The compiler warns about the
non-adjacent case, so it is rare in real code.

## A function whose name is generated

`def unquote(:"name_#{name}")(), do: ...` declares functions with computed
names; the tree has a call as the head, no identifier:

```
(call (identifier) (arguments (call (call (identifier) (arguments ...)) (arguments))) ...)
```

Nothing is recorded for it. (`defp power_of_10(unquote(x))`, where only the
arguments are generated, is recorded.)

## Declarations with no name of their own

`defstruct` and `defexception` are not recorded: the struct is the module that
holds it, which is already a `package`. A `defimpl` is recorded under the name
of the protocol it implements (as a `class`), because the implementation
module's name (`Protocol.Type`) is not a node.

## Not recorded by design

`@spec`, `@doc`, `@moduledoc`, `@impl`, `@behaviour` and the other attributes
the compiler reads; `use`, `import`, `alias`, `require`; ExUnit `test`,
`describe` and `setup` blocks; operator definitions (`def a <+> b`); anonymous
functions.
