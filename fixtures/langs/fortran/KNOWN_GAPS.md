# Fortran: known gaps

## Preprocessor directives inside a unit

Fypp (`#:include 'macros.fpp'`, `$:GPU_DECLARE(...)`) and C preprocessor lines
(`#if defined(...)`, `#endif`) are not Fortran, and the grammar does not know
them. A directive before or inside a module can turn the unit's opening
statement into loose nodes:

```
(translation_unit (comment) (ERROR (ERROR))
  (program (include_statement (filename)) (comment)
    (ERROR (identifier))        <- `module m_finite_differences`
    (use_statement ...) (internal_procedures ...)))
```

The module name is then a bare `identifier` inside an `ERROR`, with nothing to
tell it from any other identifier, so the module is not extracted and its
variables are lost. Procedures after the damage can also be lost: in Linguist's
`m_fftw.fpp`, two of the three subroutines are missing. Plain `.f90` without
directives is unaffected.

## Type-bound procedures without a binding name

`procedure :: describe` (no `=>`) declares a binding with the same name as the
procedure that implements it. It is not extracted as a method; the
implementing `subroutine` or `function` is already a definition under that name.
`procedure :: perimeter => circle_perimeter` is extracted, as method
`perimeter`. Generic bindings (`generic :: operator(+) => add`) and
`interface operator(+)` blocks are not extracted.

## Variables are module-level only

Variables and `parameter` constants are extracted from a `module`. Those of a
main program, a submodule, a block data unit or a procedure are not. The
declarators handled are plain, initialised and sized ones (`x`, `x = 1`,
`x(10)`); a declarator the grammar builds as another node type is not
extracted. `common` blocks are not extracted.

## Kinds

Programs, modules, submodules and block data are all `package`: the symbol
kinds have no separate program or module-procedure kind. Both functions and
subroutines are `function`.
