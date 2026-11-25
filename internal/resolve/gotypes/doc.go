// Package gotypes makes GraphLume's answers about Go code exact by default,
// using Go's own type checker from the standard library: go/parser, go/build,
// go/types. No `go list`, no child process, no network, no tool to install.
//
// # What it produces
//
// For every Go file the index holds, Check returns facts about objects
// declared in the repository: each use of a function, method, type,
// package-level variable or constant, with the declaration it resolves to and
// the declared function (or package-level variable) it sits in; whether the use
// is the callee of a call, taken from the syntax tree this package holds and not
// from a parser's line table; and which repository types implement which
// repository interfaces, per type and per method. Result.FileFacts shapes them
// for scipimport.Importer.ImportFacts, which lands them as `exact` edges through
// the same writer a SCIP index uses: this package does not stamp a confidence
// and does not touch a database.
//
// # How packages are loaded
//
// Every go.mod under the root is a module; a module's packages are the
// directories with Go files that are not inside a nested module. Files are
// those go/build.Context.MatchFile accepts for the host platform, so build
// constraints and _GOOS/_GOARCH suffixes apply. Each directory is checked as the
// go tool checks it: the non-test files (what importers get, memoised, with
// import-cycle detection), the package with its in-package tests (the facts for
// test files come from this one), and the external _test package against the
// test-augmented package under test.
//
// Imports resolve in order: a package of a repository module, from source; the
// standard library from GOROOT/src (found from the environment or PATH without
// executing anything), with GOROOT/src/vendor for the standard library's own
// vendored imports; the repository's vendor directory; the module cache at the
// version the module's go.mod requires, with replace honoured and the cache's
// case-escaping; otherwise a stub package so checking continues. Dependencies
// and the standard library are checked with IgnoreFuncBodies, and their
// function bodies are blanked before parsing so a generated 10 MB file costs
// kilobytes (strip.go).
//
// # Why an exact edge from here is not a guess
//
// go/types does not guess, so a use it resolves is the object the compiler
// would pick. That holds only while every type involved is really known, and
// guard.go is the code that refuses when it is not:
//
//   - A selection through embedding is withheld when an embedded field of an
//     invalid or stub-package type sits where it could have shadowed or collided
//     with the member found.
//   - A package with a file that does not parse is skipped, never partly read.
//   - A file that dot-imports a stub is skipped.
//   - An implementation is kept only when no type involved in it is unknown.
//   - Generic methods and variables map to their declaration through Origin.
//   - Positions are reported as written, not as //line directives claim.
//
// # What it does not cover
//
// Only the host platform's view of build constraints is checked: a file the
// constraints exclude is not checked, keeps its heuristic edges, and nothing is
// claimed for other platforms. Packages with type errors are checked and keep
// what the checker resolved; packages with stubbed imports lose what depends on
// the stubs. cgo packages are checked with a fake "C". Type-parameterised types
// and interfaces are not part of the implementation relation.
package gotypes
