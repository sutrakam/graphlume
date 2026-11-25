# OCaml: known gaps

- **Items of a module that is a functor application or an anonymous argument.**
  `module Applied = Make (struct let seed = 1 end)` is indexed (as a package),
  but `seed` is not: the structure is the argument of a `module_application`,
  not the body of a module binding.

  ```
  (module_definition (module_binding (module_name) body: (module_application
      functor: (module_path) argument: (parenthesized_module_expression (structure (value_definition ...))))))
  ```

  The same holds for a `struct` passed as a first-class module. Matching every
  `structure` regardless of its parent would index the contents of
  `let module M = struct ... end in` inside function bodies, which are locals.
  (The query now asks the other way round: an item is a definition unless it has
  a let binding, a top-level expression, a class binding, an extension node,
  an `ERROR` or a functor application above it. The functor application is on
  that list on purpose, which is why this gap remains. Nesting depth is no
  longer limited.)
- **A function-valued binding whose body is not a literal `fun` or `function`**
  is a variable, not a function: `let f = (fun x -> x)`, `let g = List.map h`,
  `let choose = min_binding`. Whether a name is a function is not syntactic
  there; the rule stated in `expected.tsv` is parameters or a literal
  `fun`/`function` body.
- **`val` specifications of a parenthesised or otherwise unusual type that
  contains `->`** (`val f : (int -> int)`, `val o : < m : int -> int >`) are not
  indexed. A `val` is a function when its type node is a `function_type` and a
  variable when it is a constructed or tuple type or its text has no `->`; a
  tree-sitter query cannot test that a node is anything other than a given type.
- **Variant constructors and record fields** (`Red`, `x : float`) are not
  indexed; instance variables (`val`) are, but only inside classes. `open` and
  `include` declare nothing.
- **Pattern bindings** (`let (a, b) = ...`, `let { x; y } = ...`) declare names
  inside a pattern; they are not indexed.
- **Destructive substitutions are not definitions.** `type t := u` in a
  signature, and the `with type t := u` of `include S with type t := u`, remove
  a type and declare none. They are not recorded as `type_alias`, and an
  `include ... with type ...` line is not a `type_alias` named after the type.
- **A top-level `include sig ... end` in an `.mli`** is a syntax error for the
  grammar (it is read fine inside a `module type ... = sig`), so what follows
  it is read through error recovery.
- **Items under an `ERROR` node** are not definitions, because a function body
  that failed to parse looks the same as a signature that did.
