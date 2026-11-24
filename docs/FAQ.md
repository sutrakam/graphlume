# FAQ

The questions an evaluator actually asks. Where the honest answer is
unflattering, it is here in full — a tool whose limits you have to discover by
being burned by them is one you stop using.

Numbers cited are from [accuracy.md](accuracy.md) and
[performance.md](performance.md); where this page reproduces one itself, it says
so and names the corpus.

---

## What happens if I never give it a SCIP index?

You get a working system in which **nothing is ever labelled `exact`**, and that
is by construction rather than by omission.

A tree-sitter parse can tell you that `svc.updateStatus()` is a call. It cannot
tell you *which* `updateStatus`, because that needs a type for `svc`, which needs
imports, which needs the build graph. So the heuristic resolver tops out at
`high` confidence and says so. `model.Edge.Validate` refuses `exact` unless the
evidence source is `compiler`, a SQL `CHECK` in migration 0001 refuses it again,
and `registry.validateRun` refuses it a third time before a run is published.
`doctor --verify-index` then checks the whole graph for edges claiming compiler
certainty without compiler evidence.

Practically: `high` is 86.8–96.1% across five repositories in three languages,
so the graph is useful. What you lose is the ability to call a finding a **fact**
rather than a **lead** — `verify` grades an architecture violation on a
heuristic edge as a lead and exits 0 (`advisory`), and grades the same violation
on a compiler edge as a fact and exits 1. Without SCIP, `verify` can advise but
cannot fail a build on architecture.

You also lose recall. On this repository's own working tree, `resolve` binds 40.9% of
references; the rest are calls into dependencies that live outside the tree, or
names too ambiguous to bind. Adding `scip-go` took the same tree from 11,720
heuristic edges to 25,736 additional compiler facts.

## Why is `medium` only 5.7–37.5%, and what am I supposed to do with it?

Show it. Do not act on it. That is the whole recommendation.

*Updated 13 November 2026.* The 5.7–37.5% below was measured against compiler
ground truth on five repositories before the resolver bound a call by what it
was made on. On the current build, a hand check of 14 projects found `medium`
right on 249 of 259 sampled edges (83–100% by project), and `medium` now has a
floor of 60%; see [accuracy.md](accuracy.md#2026-11-13-the-second-verification-and-what-is-still-wrong).
The advice stands for a different reason: `medium` is still a lead to check.
The `low` tier is the poor one now (8 of 30 sampled edges right in Python, 9 of
30 in Kotlin, 14 of 30 in C#). The text that follows is the earlier measurement,
kept because the accuracy page records the correction rather than editing it.

`medium` is the tier the resolver assigns when the evidence narrowed the
candidates but did not make one unique — a receiver type matching an overload
set, or several matches among the imports. Measured against compiler ground
truth across five repositories it lands between **5.7%** (prometheus) and
**37.5%** (a Python corpus). At 5.7% it is wrong roughly nineteen times out of
twenty.

In Python `medium` also holds something the other languages' does not: every
method call bound on its NAME because the receiver's class is unknown —
`target.save()` bound to the only `save` in the file. They are `medium` because
they measure at 26–48%; see "Python: the receiver's class, read
from the source" in [accuracy.md](accuracy.md).

`medium` does not mean "probable": measurement does not support that reading
([accuracy.md](accuracy.md)).

Two consequences worth internalising:

- `medium` is **not rare** — 6,473 of the 30,975 adjudicated edges. That is why
  the all-tiers precision figure sits between 65% and 94% while `high` alone
  stays near 90%. An agent consuming every edge equally gets the low number; one
  passing `--min-confidence high` gets the high one.
- The tier is worth *rendering* because a hint that a name exists is still
  information for a human deciding where to look. It is not worth *gating* on,
  and a CI check that blocked a merge on a 5.7%-precision edge would be removed
  within a week — taking the facts with it. That is the reasoning behind the
  fact/lead split in `verify`.

`low` is 0.0–2.8%. Treat it as "this name exists somewhere", nothing more.

## Why is an empty result not proof that nothing calls this?

Because the index resolves only part of any repository, and the two statements
"nothing calls this" and "nothing I could resolve calls this" have opposite
consequences. A developer told the first one deletes the method.

So every relationship query that finds nothing reports the index's measured
resolution coverage instead of a bare empty list:

```
$ graphlume callees profileTools
profileTools (function) at internal/mcp/server.go:129

No callees found.

41% of 29506 references are bound to a definition (15381 point outside this
repository, 2063 were too ambiguous to bind). An empty result is a lower bound,
not proof that nothing uses this symbol.
```

The same discipline runs through the rest of the tool. `arch` reports how many
symbols belong to no declared component and were therefore not checked. `diff`
ends with what it could not compare. `verify` prints `NOT CHECKED` and `UNKNOWN`
sections. `doctor --resolution-precision` reports the coverage its precision was
computed over and names the languages it could not measure at all. Every
capability report carries a `Completeness` value that
`capability.NewCompleteness` constructs so the lower-bound and dynamic-dispatch
caveats **cannot** be omitted by a caller who forgets.

Three different situations produce an empty answer — the call does not exist,
the file never parsed, or the reference was seen and could not be bound — and
they have different remedies. `internal/cli/query.go` diagnoses which one you
are looking at rather than rendering all three identically.

## Why doesn't it use an LLM?

Because every part of this tool that a model could plausibly do is a part whose
value depends on it being reproducible.

The argument is made in four package docs, and it is the same argument each
time:

- **`internal/context`** — "the same task against the same index produces a
  byte-identical Pack, because an agent that gets a different answer on a retry
  cannot build a cache, a test, or trust."
- **`internal/compile`** — a contract that changed between two runs of the same
  input could not be cached, tested, or trusted, and "the brief drifted" is a
  worse failure than "the brief was thin".
- **`internal/verify`** — a verdict that changed between two runs of the same
  inputs could not be gated on.
- **`internal/bench`** — an LLM judge would make the score depend on a model
  version and a sampling temperature, and the regression this most needs to
  catch is exactly the one a generous judge forgives.

There is a second reason, specific to resolution. A model asked "which
`updateStatus` does this call?" produces an answer that is *plausible*, and a
plausible answer stamped with a confidence level is precisely the lie the
confidence system exists to prevent. Compiler output is the only input GraphLume
accepts as proof, and `internal/resolve/scip` is the only package permitted to
produce `exact` edges.

The determinism is load-bearing elsewhere too: `graphlume diff` is only meaningful
because two index runs over the same tree are byte-identical, and
`graphlume docs --check` can only gate CI because the generated documents are
reproducible. Neither works on top of a non-deterministic pass.

See [NON-GOALS.md](NON-GOALS.md#it-does-not-use-a-model-as-a-resolver).

## What does it cost to index a large repository?

From [performance.md](performance.md), three real repositories cloned at
`--depth 1`, indexed and resolved on `darwin/arm64` native:

| Repository | Language | Files discovered | Indexed | Symbols | Edges | Index time | Peak RSS |
|---|---|---|---|---|---|---|---|
| prometheus | Go | 1,262 | 1,030 | 21,429 | 35,787 | 8.8 s | 664 MB |
| nest | TypeScript | 2,297 | 1,929 | 13,916 | 4,064 | 3.7 s | 590 MB |
| spring-boot | Java | 11,851 | 9,236 | 94,304 | 119,480 | 46.4 s | 756 MB |

A 9,000-file Java repository indexes in under a minute. Reproduced here on this
repository's own working tree — 476 files discovered, 422 indexed, 6,259 symbols
— a full index took 10.0 s and `resolve` 1.3 s.

Three caveats that matter more than the numbers:

1. **The first index is the expensive one.** Afterwards it is incremental, and
   files are compared by **content hash rather than mtime** — `git checkout`
   rewrites timestamps wholesale, and an mtime-based diff would reparse the
   whole repository after every branch switch. On the 5,000-file synthetic
   corpus a full index is 20–35 s while an incremental pass over an unchanged
   tree is under a second and a one-file change is around 0.8 s.
2. **Peak memory scales with `--workers`, not with repository size.** The
   default is half the CPUs, from 4 to 8, and fewer when the soft memory limit
   (768 MiB unless `--memory-limit` sets another) cannot hold that many.
   performance.md publishes the worker/RSS table behind it.
3. **`exact` mode is a different order of cost**, because it runs your real
   build. `scip-go` on prometheus takes about eight minutes and produces a 57 MB
   index. Measured here: 43.7 s on a 305-file clone of this repository, and on
   its current 422-file working tree a 12.7 MB index that `scip import` landed
   in 1.8 s.

The published figures in performance.md were taken on an amd64 toolchain running
under Rosetta, so they are a **ceiling** — roughly 1.4–3x above what a native
build delivers. See [BENCHMARKING.md](BENCHMARKING.md#pitfall-1-check-your-goarch-before-you-benchmark-anything).

## Is Java supported?

Indexed and measured, yes. Measured against `scip-java` on
spring-petclinic/spring-petclinic-rest: `high` is 100.0% precision on 82 of 82
judged edges, 32.7% recall, F1 49.2%; `medium` and `low` are absent because no
edges landed in those tiers; 169 of 251 adjudicated call sites produced no
edge at all. See [accuracy.md](accuracy.md) "Java: measured" for the method.

Take the number seriously but keep its size in view: the judged pool is 82
edges, far smaller than the thousands adjudicated for Go, TypeScript and
Python, and Java's recall is the weakest of the four measured languages —
two thirds of its known call sites produce no edge. Precision is solid;
recall is the thing to watch.

Getting here took three earlier attempts across seven builds, also recorded
in accuracy.md: spring-boot needed a Gradle that was not on the host,
commons-lang builds but emits no SemanticDB because `-source 8` is below what
the javac plugin attaches to, and petclinic's GraalVM Maven extension needed
Java 17 while `scip-java` falls back through 21 → 17 → 11. None of those was
GraphLume's fault; spring-petclinic-rest was simply the first repository tried
that avoided all three.

Before a Java SCIP index is imported for a given repository,
`doctor --resolution-precision` says so by name rather than quietly leaving it
out of the table — for example, a run against a repository with no Java index
imported yet:

```
LANGUAGE    FILES  CALLS EDGES  COMPILER FACTS  STATUS
go          399    11,683       25,736          measured
java        8      18           0               NOT MEASURED — no SCIP index imported for it
typescript  1      2            0               NOT MEASURED — no SCIP index imported for it
```

A synthetic Java corpus was deliberately not used to measure Java: uniform
generated code makes resolution easy and would report an inflated precision,
which is worse than no number.

## Does my source code leave the machine?

No. Single binary, zero CGO, no network in any indexing or query path. The one
place GraphLume reaches the network at all is `indexers install`, which downloads
third-party SCIP indexers — pinned to exact versions, verified against recorded
digests, refusing rather than installing on a mismatch.

What *does* leave the machine is whatever your agent sends to its model
provider, which is why `internal/security` is the last thing that runs before
indexed content is served. Secret values are redacted before storage, not on the
way out. The error modes there are treated as asymmetric on purpose: a false
positive costs a line of context, a false negative ships a live credential to a
provider's logs and no release fixes that.

`graphlume config` is the sharpest expression of the same rule. It reports which
configuration keys exist and who reads them, and **never reads a value at all** —
not masked, not truncated. A data structure with nowhere to put a value cannot
leak one, which is a stronger guarantee than a redaction list that can be wrong
about a key nobody anticipated.

## Can a repository I cloned make GraphLume run something?

No, and this is enforced structurally rather than by convention.

GraphLume reads two config files with deliberately different trust levels.
`.graphlume.yml` lives in the repository, is therefore untrusted, and controls
*what gets indexed*. `~/.config/graphlume/config.yml` is yours alone and controls
*what gets executed* — accuracy mode, whether indexers may be provisioned, and
whether they run natively or in Docker.

The split exists because compiler-backed indexing runs build systems, and
Maven/Gradle plugins execute arbitrary code by design. If a repository could
name the indexer to run, `git clone && graphlume index` on a hostile repo would
be remote code execution. So repository config **structurally cannot express**
execution settings: the fields do not exist on the type, and unknown keys are a
parse error rather than a silently ignored setting. `TestRepoCannotSetExecutionSettings`
holds it there.

`internal/indexer` is the package where this bites hardest, and its doc comment
lists the six controls with the reason for each — the one worth knowing is that
**nothing is read from the repository except the names of marker files**.
Detection stats `pom.xml`, `go.mod`, `package.json`; it never opens them.

A repository also cannot disable secret redaction (there is no `security:` key,
and writing one is a parse error), and its exclude patterns are additive, so it
cannot un-exclude `.git` or `node_modules`.

## Why does `scip import` say "0 upgraded from heuristic"?

Because that is the correct result, and it took a note in the command's own
output to stop people reading it as a failure.

SCIP records that one symbol **references** another. It does not record that the
reference is a **call**. GraphLume will not stamp a guess about that with
`exact`, so compiler facts arrive as `REFERENCES` edges and sit *alongside* the
resolver's `CALLS` edges rather than replacing them. An upgrade happens only
where both tools agree on the relationship kind and the evidence line.

The same output line usually alarms people twice. `22974 with no counterpart in
the index` is also normal: a compiler sees local variables, struct fields and
generated declarations that a tags-based extractor never emits, so on a real Go
repository most of the compiler's symbols legitimately have no counterpart. Only
a very low match **rate** suggests the index and the working tree have diverged,
and the command says so when it does.

If the matched-document count is zero, that is the real failure and it is almost
always paths — an index built in a container records `/build/repo/src/Main.java`.
Pass `--path-prefix`.

## Why did a query print a warning about which build wrote the index?

Because an index is data *plus* the resolution rules that produced it.

Every run records the build that wrote it. Query with a different build and you
get, on stderr, before the answer:

```
level=WARN msg="This index was written by graphlume dev and you are running
9.9.9-test. Resolution rules change between builds, so these results may come
from different rules than this binary would produce. Re-index to refresh."
```

This project has already changed its resolution rules twice in ways that moved
real edges: one guard broke every chained Go call, and another converted 362
ambiguous references into confident wrong answers. An index written either side
of such a change holds different edges for identical source, every query against
it succeeds, and the results look exactly as trustworthy as current ones. There
is no symptom to notice, which is why the index has to say so itself.

It is a warning, not an error — a stale index still answers, and refusing to
answer would be the worse trade. `-q` silences it. An index with no stamp at all
reports *unknown* provenance instead, because "re-index to be sure" is different
advice from "these results came from other rules".

A **schema** mismatch is a different matter and does fail, because the reader
cannot safely interpret the rows:

```
error: index schema is v1 but this build of graphlume expects v2 — run 'graphlume index' to upgrade it
```

## What is the difference between `search`, `symbol`, `references` and `callers`?

They answer four different questions and are not interchangeable.

| Command | Question | Matching |
|---|---|---|
| `search` | which files contain this text? | full-text, identifiers split into parts |
| `symbol` | where is this defined? | exact name, not fuzzy |
| `references` | what points at it? | calls, imports, implementations, extensions, plain references |
| `callers` | what calls it? | call edges only |

`search` covers indexed files only — binary, generated and oversized files are
recorded in the index but carry no searchable text. `symbol` returns
`TransactionService`, not the forty symbols whose names contain it; use `search`
when you want the forty. `references` is strictly broader than `callers`,
because an import or an implementation is a reference but not a call.

The one that surprises people: identifiers are indexed by their **parts**, so
`callback` matches `handlePaymentCallback`. That is a correctness property, not
a nicety — SQLite's FTS5 tokenizer would otherwise index `handleCallback` as one
opaque token and a search for `callback` would find nothing. It also has a known
limit, recorded in performance.md: `SplitIdentifier` yields
`[data source health indicator]`, so the query `datasource` misses
`DataSourceHealthIndicator` while `data source` finds it — and `datasource` is
the phrasing Spring itself uses.

## Do I have to run `resolve` separately? Why isn't it part of `index`?

Yes, and because it cannot be.

Indexing extracts references but cannot bind them: binding needs the complete
symbol table, which does not exist until every file has been parsed. A file
parsed first cannot resolve a call to a symbol defined in a file parsed last.

Two practical consequences. `graphlume index --full` **discards resolved edges** —
the flag's own help says so — and you must re-run `resolve` after it; a plain
incremental `graphlume index` does not. And until you have run `resolve` once,
relationship queries have nothing to search, and will tell you exactly that
rather than returning a misleading empty result:

```
$ graphlume callers Redact
Redact (function) at internal/security/redact.go:67

No callers found.

29506 reference(s) were extracted but none have been resolved yet — run
'graphlume resolve' to build the call graph. Until then this query has nothing
to search.
```

## Is it deterministic? How would I know?

Yes, and the way you know is that the project publishes a digest and gates on
it. `graphlume index --full --digest` prints a sha256 over the run's content.
Indexing the same tree twice must produce the same digest, and the gate is
verified **both ways** — stable across 1/2/4/8/16 workers, and *sensitive* to a
single added symbol. A digest that never changed would pass the stability half
while detecting nothing.

Reproduced here, two full indexes of the same clone:

```
Digest:        1aa2f3d184544656a21062b12c8dc49f05231ca865198d63941f44b291288f12
```

This is not decorative. It is what makes `graphlume diff` sound: a graph delta
between two runs only means "the code changed" if the two runs are themselves
reproducible, and otherwise "this edge disappeared" might mean a worker pool
interleaved differently. It also caught a real defect that no unit test could —
`tableSymbols` picked a canonical symbol by "first declaration wins" over a
`SELECT` with no `ORDER BY`, and an unordered `SELECT` has no first. The full
story is in [accuracy.md](accuracy.md).

## Why does `status` say DEGRADED?

`DEGRADED` means the run published but not every file parsed cleanly. It is a
statement about completeness, not a failure: the index is queryable and every
command works. "A partial index beats none, but it must not claim to be
complete" is the rule in `registry`, and the status is how it declines to claim
it.

`DEGRADED` means the parse of at least one file failed outright or was stopped
before the end of the file, and `status` names those files with the reason. It
does not mean a file had syntax errors, declared nothing, or was skipped.

A file that records no symbol or reference is not `partial`: thirteen `doc.go`
files in this repository hold a package comment and nothing else, and the tree is
`READY`:

```
$ graphlume status
Index:          READY
Files:          723
Symbols:        11220
Relationships:  24950
Parsed:         708 (702 clean, 6 with syntax errors)
Syntax errors:  6 files read to the end with errors the parser recovered from; definitions inside the unreadable regions may be missing
Text only:      7 files searchable by text, no definitions (no definition query for the language): yaml 3, html 2, dockerfile 1, gomod 1
Skipped:        8 files not indexed: no_grammar 8
```

*In this output YAML and HTML are text only. This build parses YAML, JSON, HTML and Markdown with definition queries, so its `Text only` row lists fewer languages (for example Dockerfile, `go.mod` and diffs).*

The six files with syntax errors are SQL migrations the SQL grammar does not
fully accept. They are indexed; the status says so and does not call it a
failure.

The index records the disposition of every file, per path, so a gap is visible
rather than looking like the file never existed:

```
$ sqlite3 .graphlume/index.db "select parse_status, count(*) from files group by parse_status;"
ok|702
partial|6
skipped|15
```

Every row that is not `ok` carries its reason at the start of `parse_error`:
`syntax_errors:`, `budget_exceeded:`, `text_only:`, `too_large:`, `binary` and so
on. [CLI.md](CLI.md#status) has the table. `status --json` carries the same
breakdown under `parse`, beside `parse_failures` (files whose parse failed or
was stopped early) and `partial_parses` (files with `parse_status = 'partial'`).

## What does it deliberately not do?

Enough that it has its own page: [NON-GOALS.md](NON-GOALS.md). The short list —
it is not a code-search engine competing on retrieval, it does not use a model
as a resolver, it does not persist runtime spans alongside a static index, it
does not ship vulnerability data, it does not infer your architecture, and it
does not widen its MCP tool surface.

## Where do I go next?

| You want | Go to |
|---|---|
| A route through the tool for your role | [GUIDE.md](GUIDE.md) |
| Flags and exit codes | [CLI.md](CLI.md) |
| To reproduce a published number | [BENCHMARKING.md](BENCHMARKING.md) |
| The full reference | [user-manual.md](user-manual.md) |
| An error message you just saw | [troubleshooting.md](troubleshooting.md) |
