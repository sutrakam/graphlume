# Scale testing

How GraphLume is tested at the size of a large repository, what it measured,
and how to run the same measurement on your machine.

The suite is `tests/scale`. It runs in the default `go test ./...` at a small
size and takes the real measurement when asked for a larger one.

## Contents

- [Why a separate suite](#why-a-separate-suite)
- [Running it](#running-it)
- [What it asserts](#what-it-asserts)
- [Measured results](#measured-results)
- [What the suite found](#what-the-suite-found)
- [Adding a test](#adding-a-test)

## Why a separate suite

Unit tests check correctness on a dozen symbols. A query that is correct on a
dozen symbols can still read every relationship in the index once per file, and
nothing but size shows it. That exact defect was written while this suite was
being built: `graphlume hotspots` passed every unit test and did not return in two
minutes on a 19,000-relationship index.

## Running it

```bash
go test ./tests/scale/                               # the CI gate: 5,000 symbols, a few seconds
```

```bash
go test ./tests/scale/ -v -timeout 40m -scale.symbols=250000 -scale.source-files=5000 -scale.commits=2000
```

```bash
go test ./tests/scale/ -v -timeout 90m -scale.symbols=1000000 -scale.source-files=20000
```

| Flag | Environment variable | Default | Sets |
|---|---|---|---|
| `-scale.symbols` | `GRAPHLUME_SCALE_SYMBOLS` | 5,000 | Symbols in the synthetic graph |
| `-scale.source-files` | `GRAPHLUME_SCALE_SOURCE_FILES` | 150 | Files in the generated source tree |
| `-scale.commits` | `GRAPHLUME_SCALE_COMMITS` | 400 | Commits in the synthetic history |

Run with `-v`: each test logs what it built and what it measured.

It is not behind a build tag. A tagged suite is invisible to the compiler, to
`go vet` and to the default run, so it rots until the night someone needs it. A
suite that always runs small cannot.

A million symbols needs about 4 GB of memory for the in-memory fixture.

## What it asserts

Three properties, most important first.

**1. Every answer is bounded.** A response to an agent must fit the host's
inline limit (24 KB) whatever the size of the index. An answer over the limit is
written to a file by the host and read back in pieces, which costs more than the
search the tool replaced. `TestEveryToolAnswerStaysBoundedAtScale` calls every
registered tool and fails if a tool has no call in the test.

**2. Memory is bounded.** A streaming scan must not keep memory in proportion to
what it scanned. The suite compares live heap before and after each operation,
after a collection on both sides.

**3. Time grows no worse than linearly.** A budget is a fixed allowance plus an
allowance per symbol or per file:

```go
budget{fixed: 2 * time.Second, perUnit: 60 * time.Microsecond}
```

So the same test fails at 5,000 symbols and at a million for the same reason:
something became quadratic. Budgets are several times looser than the measured
figures below. They are tripwires for a change in the shape of a cost; a tight
budget on a shared CI runner is a flaky test.

Two kinds of fixture are used:

| Fixture | Built by | Finds |
|---|---|---|
| Synthetic graph | Rows inserted straight into the store, hub-heavy like a real call graph | Query plans, traversal bounds, answer size |
| Generated source tree | Go files on disk, then `graphlume init`, `index` and `resolve` | Parsing, extraction, resolution, the commit |

## Measured results

One run, Apple M4 Pro, Go 1.26, no race detector, on 2026-11-12. The fixture was
250,000 symbols and 1,258,823 relationships across 2,500 files, with a history
of 2,000 commits. Reproduce before treating any figure as a target.

### Whole-repository reports

| Operation | Time | Heap left behind |
|---|---|---|
| Unused-code scan (10,638 unreferenced definitions found) | 4.2 s | 1.1 MB |
| Unused-code scan, one directory | 3.9 s | 0.3 MB |
| Hotspot ranking (2,375 changed files) | 3.0 s | none |
| Path between two symbols | 10 ms | none |
| Impact from a hub with 1,000 callers | 1.0 s | 170 MB (the symbol index, cached once per server) |
| Test discovery for one symbol | 2 ms | none |

The two unused-code rows were measured before the page-order fix listed under
[what the suite found](#what-the-suite-found). The same scan on the same fixture
took 1.7 s afterwards, measured with other test suites running, so the idle
figure is lower still and has not been re-taken.

### The test-only pass of the unused-code scan

`graphlume dead-code --include-test-only` (`include_test_only` on
`deadcode`) adds a second pass that asks, of every referenced definition,
whether all its callers are in test files. It has its own tests,
`TestTestOnlyScanIsBoundedAtScale` and `TestTestOnlyAnswerStaysBoundedAtScale`,
and its own budget: 2 s plus 90 µs per symbol for the two passes together.

Measured on the same machine on 2026-11-12, but **with other test suites
running at the same time**, so these are upper figures and not idle ones:

| Symbols | Relationships | Test files | Reached only from tests | The pass itself | Both passes | Heap left behind |
|---|---|---|---|---|---|---|
| 5,000 | 20,428 | 5 of 50 | 120 | 34 ms | 48 ms | 0.1 MB |
| 100,000 | 503,491 | 100 of 1,000 | 2,439 | 0.49 s | 0.87 s | 0.6 MB |
| 250,000 | 1,258,823 | 250 of 2,500 | 6,037 | 1.86 s | 3.48 s | 0.8 MB |

That is 5 to 7 µs per symbol at every size. The pass stops at the first caller
outside a test file, so a definition with a thousand ordinary callers costs a
few lookups; only a definition every one of whose callers is a test has all its
relationships read.

### Every tool, warm, 95th percentile of five calls

Measured with nothing else running on the machine. The same test run alongside
other test suites reported `deadcode` at 15 s instead of 4.8 s: a p95 of
five samples moves a great deal under load, so take these on an idle machine.

| Tool | p95 | Answer |
|---|---|---|
| `symbol`, `definition`, `coverage`, `search`, `diff`, `tests`, `owners` | 2–5 ms | 0.5–1.7 KB |
| `callers`, `callees`, `references`, `implementations`, `impact`, `changes` | 54–78 ms | 0.4–7.4 KB |
| `trace`, `rename`, `context`, `plan` | 85–95 ms | 1.7–18.3 KB |
| `endpoints`, `config`, `overview` | 174–347 ms | 0.7–0.8 KB |
| `blindspots` | 1.5 s | 10.3 KB |
| `hotspots` | 3.3 s | 2.4 KB |
| `deadcode` | 4.8 s | 5.6 KB |

Every answer was under the 24 KB limit. The largest was `context` at
18.3 KB.

`communities` was added after that run and measured on its own, with other
test suites running on the machine, so read both figures as upper bounds. On the
same 250,000-symbol fixture it took 3.6 s and answered with a refusal: the
synthetic graph links each file to about a hundred others, which puts it over
the tool's bound of 250,000 file-to-file links, and past that bound it asks for
a path instead of partitioning part of the graph. The time is the fold of every
relationship into file-to-file links, which is linear in relationships. At
50,000 symbols (226,730 relationships, 86,847 links) it partitioned the graph in
0.64 s.

Two of these were several times slower in the first run and were fixed by
bounding their test probes: `plan` went from 4.4 s to 92 ms and
`blindspots` from 5.5 s to 1.5 s.

### Indexing a generated source tree

| Files | Symbols | `init` + `index` + `resolve` | Throughput |
|---|---|---|---|
| 2,502 | 13,752 | 6.5 s | 385 files a second |
| 5,002 | 27,502 | 13.4 s | 374 files a second |

Doubling the tree multiplied the time by 2.06. On the larger tree, a query for
the callers of a function called from every file took 69 ms, `dead-code` took
0.6 s and returned only the planted unused functions, and re-indexing with
nothing changed took 1.0 s.

## What the suite found

| Defect | Measured | Fix |
|---|---|---|
| The unused-code query read every relationship once per candidate when the database had no statistics | 8.5 s at 5,000 symbols | Each probe names its index: 36 ms. Pinned by `TestUnreferencedSymbolsUsesItsIndexes` |
| The hotspot query read every relationship once per file | Over 2 minutes on a 19,000-relationship index | Join order forced. Pinned by `TestFileStructureForUsesTheDestinationIndex` |
| `blindspots` ran one count query per exported function and loaded every symbol | 2.4 s at 5,000 symbols | One aggregate query |
| A hotspot score of zero for every file with no recorded size | All scores 0 | Size floors at one byte |
| The unused-code query sorted its page: the planner reached symbols through an index and ordered them by id afterwards, so every page probed every remaining symbol before `LIMIT` applied | 1.69 s at 100,000 symbols with three pages; one more full pass for every 2,000 unreferenced definitions | Symbols are read in key order, so a page is a seek and `LIMIT` stops it: 0.40 to 0.66 s on the same fixture under the same load. Pinned by `TestUnreferencedSymbolsPagesInKeyOrderWithoutSorting` |

Still open:
`deadcode` and `hotspots` take 3 to 5 seconds at this size, because
each visits every definition or every changed file once; and the symbol index
holds about 170 MB for 250,000 symbols once loaded.

## Adding a test

1. Use the shared fixture (`load(t)`); do not build another. At a million
   symbols the build is the expensive part.
2. Wrap the operation in `measure`, then call `requireWithin` with a budget whose
   two terms are written at the call site, and `requireRetainedUnder` if the
   operation streams.
3. Never call `t.Parallel()`. Heap figures are process-wide.
4. If the test adds a tool, add its call to `TestEveryToolAnswerStaysBoundedAtScale`.
   The test fails until you do.
5. Pin a query plan with `EXPLAIN QUERY PLAN` in the package that owns the query
   when a fix depends on the plan. A plan regresses without any test of the
   result noticing.
