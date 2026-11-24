# C: known gaps

## File-level variables, enums and unions

`static counter_t total;`, `enum color { RED, GREEN };` and `union U { ... };`
are not extracted. The definition query predates this fixture and names
functions, structs and typedefs only; the fixture records that rather than
widening it in a change about macros.

## Macros

A function-like `#define` is a function by kind and by name
(`#define BIT_OF(n) ...` is `BIT_OF`). An object-like one (`#define LIMIT 64`,
`#define cpu_alias __cpu_alias`) is not recorded: there are too many of them to
be definitions a person searches for, and a call through an alias
(`cpu_alias(x)`) therefore binds to nothing.

A function that exists only after the preprocessor has run is not extracted:
`ATOMIC_OP(add)` that expands to `atomic_add()`, or the `scan_` + key function a
`#define` writes. The macro is the definition the text contains.

A function written as a macro call and a block at file scope
(`SYSCALL_DEFINE2(poke, int, fd, long, val) { ... }`, `BPF_CALL_2(name, ...) {
}`) is recorded under what the macro defines when the head is an upper-case
word and the first argument is a plain name: `poke` for `NAME(poke, ...)`,
`sys_poke` and `compat_sys_poke` for the system call macros, `suite_case` for
`TEST_F(suite, case)`. A head whose first argument is not the name of what it
defines (`DEFINE_SOMETHING(x) { ... }` that declares a struct) is read as a
function called `x`.

## Macros the mask does not recognise

Declarations behind a macro are read after the macro is masked (see
`internal/parse/cmacro.go` and `cmacro_c.go`). A macro written in mixed case
(`Py_LOCAL_INLINE(int)`) is not a candidate in C++ or Objective-C; in C an
identifier that starts with two underscores is an annotation when it stands
between a type and a name or after a declarator, and a short list of other
words is (`asmlinkage`, `notrace`, `noinline`, `noinstr`). A statement macro
without a block (`list_for_each_entry(...)` followed by one statement) leaves
a missing `;` in the tree; the file still reads, and the macro is a call.

## Conditional compilation

An `#if` group that cuts a declaration apart (see `conditional.c`, a parameter
list with a different tail in each branch) is read with one branch: the first
that is not `#if 0`, with two groups that test one macro read in the same
world. `extern "C" {` guards are kept, both halves together. A definition that
exists only in a branch that is not kept is not extracted, and the number of
branches blanked is in the file's stored parse text.
