# Benchmarking

How to reproduce every number this project publishes, and what each one does and
does not mean.

This page is operational. The **method** for resolution accuracy — why judgement
is restricted to unambiguous lines, why recall is not reported, where the
remaining errors come from — is in [accuracy.md](accuracy.md), and this extends
that document rather than restating it. The performance numbers and the
measurements that chose each default are in [performance.md](performance.md).

The reason both exist: until they did, every confidence label in this tool was a
design intention rather than a measurement. If you disagree with a number, the
commands below are how you produce a competing one.

---

## What is measurable, and with what

| Claim | Command | Answers |
|---|---|---|
| Confidence tiers mean something | `doctor --resolution-precision` | precision per tier, against compiler ground truth |
| Retrieval returns the right code | `bench <cases.yml>` | recall and precision against hand-written cases |
| Indexing is reproducible | `index --full --digest` | one sha256 over a run's content |
| The graph is internally sound | `doctor --verify-index` | dangling edges, orphans, unproven `exact` claims |
| Operations cost what we say | `index`, `resolve`, query commands | wall clock, printed by the tool itself |
| Impact analysis predicts what a change touches | `scripts/bench/impactaccuracy` | precision, recall and F1 against a repository's own commit history |

The first five run against your repository, not ours. That is the point of
shipping them as commands rather than as a table. The sixth is a harness in this
repository that drives the binary through its command line; it is described in
[section 6](#6-figures-other-tools-publish).

## Prerequisites

```bash
CGO_ENABLED=0 go build -o /tmp/graphlume ./cmd/graphlume   # Go 1.26+
```

Zero CGO is not incidental: it is what makes `go install` work without a C
toolchain, and it means the binary you benchmark is the binary that ships.

Ground-truth indexers, pinned to the versions every published figure was taken
against:

| Tool | Version |
|---|---|
| `scip-go` | 0.2.7 |
| `scip-typescript` | 0.3.14 |
| `scip-python` | 0.6.6 |

`graphlume indexers install --yes` provisions these at pinned versions with
verified digests. `graphlume indexers list` tells you what applies to a
repository and what is missing, without installing anything.

## Corpora

Two documents use different corpora, deliberately, and neither set is
interchangeable with the other.

**[accuracy.md](accuracy.md) — real third-party code.** `--depth 1` clones of
prometheus (Go), nest (TypeScript), scrapy and requests (Python), plus
GraphLume's own source. Third-party code the resolver was never tuned against is
the only kind that can falsify a resolution claim; GraphLume's own numbers are
the least trustworthy in the table for exactly that reason, and they are the
highest.

The Python scope analysis was measured on four, pinned by tag rather than by
"whatever the default branch held": scrapy at the SHA the nightly corpus gate
pins (`c7eacdbb`), requests `v2.32.3`, httpx `0.28.1` and django `5.1.4`. Four
because one was not enough — a rule that agreed with the compiler index 8 times
in 8 on scrapy agreed 1,486 times in 4,122 on django, and the reason was the
compiler index, not the rule. See "Python: the receiver's class, read from the
source" in accuracy.md.

**`fixtures/pycalls` — hand-written ground truth.** A small Python package in
which every interesting call site says, in a comment on its own line, which
definition it reaches, or that it is dynamic and nothing may claim it at `high`.
It exists for the shapes a compiler index cannot adjudicate — a call through a
parameter has no SCIP target to disagree with — and it is a regression gate
rather than a published figure: `go test ./internal/cli -run
TestPythonCallGroundTruth -v` scores it and prints the table. Its markers were
checked against `scip-python` once; the one disagreement is recorded in the
fixture's README.

**[performance.md](performance.md) — three corpora, each measuring a different
thing.** `fixtures/payment-platform` (9 files) measures fixed costs — process
start, migrations, transaction setup — rather than throughput. GraphLume's own
source measures a real mid-size repository. A 5,000-file synthetic Go corpus
(`go5k`, ~20 MB, 210,000 symbols) measures scaling, and is byte-for-byte
reproducible because each file's content is a pure function of its index.

**Do not measure accuracy on the synthetic corpus.** Its identifiers repeat
across packages by construction, so `resolve` reports 0 resolved and 80,000
ambiguous. It measures the throughput of the binding path and says nothing about
binding correctness. For the same reason a synthetic *Java* corpus was
deliberately not used to measure Java: uniform generated code makes resolution
easy and would report an inflated precision, which is worse than no number.

---

## 1. Resolution precision

The measurement that turns the confidence labels from an intention into a
number. Four commands:

```bash
graphlume index && graphlume resolve
scip-go --output index.scip          # compiler ground truth
graphlume scip import index.scip      # lands as REFERENCES/exact/compiler
graphlume doctor --resolution-precision
```

Order matters: index, resolve, then import. A heuristic `CALLS` edge is
adjudicated by asking the compiler what the reference at that source position
resolves to; agreement means both name the same definition.

Reproduced on this repository's own working tree — 476 files discovered, 422
indexed, 6,259 symbols, against a 12.7 MB `scip-go` 0.2.7 index:

```
  TIER    VERDICT  JUDGED  AGREE  DISAGREE  PRECISION
  high    act on   5,190   5,086  104       98.0%
  medium  show     49      17     32        34.7%
  low     ignore   263     0      263       0.0%
  all     —        5,502   5,103  399       92.7%

COVERAGE
  Judged 5,502 of 11,703 heuristic CALLS edge(s) (47.0%); 6,201 were not adjudicated
  at all and are neither right nor wrong above.
  Against 25,736 compiler fact(s), on the 14,280 line(s) the compiler resolved to
  exactly one target; 4,844 ambiguous line(s) were excluded.

LANGUAGES
  LANGUAGE    FILES  CALLS EDGES  COMPILER FACTS  STATUS
  go          399    11,683       25,736          measured
  java        8      18           0               NOT MEASURED — no SCIP index imported for it
  typescript  1      2            0               NOT MEASURED — no SCIP index imported for it
```

[accuracy.md](accuracy.md) records **97.8% / 34.1% / 0.0%** over 4,619 judged
edges for GraphLume's own source. The run above is the same measurement on a
larger, later tree, and it agrees to within a fraction of a point. **The two
numbers are not comparable as a before/after** — different file counts, different
`scip-go` output, different resolver. That is the single most important
discipline on this page, and it has its own section below.

### What this number is

- **It is precision.** SCIP records that one symbol *references* another, not
  that it *calls* it, so the compiler's edge set is a superset of the resolver's
  by construction. Dividing by it would penalise the resolver for doing exactly
  what it was designed to do. Recall for calls needs a ground truth that
  distinguishes call sites from type references, which `scip-go` does not emit.
- **It is not a claim about proven edges.** Compiler-backed edges are not scored
  against themselves. They are `exact` because a typechecker produced them, and
  `doctor --verify-index` separately enforces that nothing may claim `exact`
  without `source=compiler`.
- **It covers less than the whole graph, and says how much.** 47.0% of heuristic
  `CALLS` edges were adjudicated in the run above. The rest are neither right
  nor wrong in that table.
- **It names what it could not measure.** A language with heuristic edges and no
  imported SCIP index is listed as `NOT MEASURED` rather than omitted. Unmeasured
  is not the same as accurate.

### Why coverage is not higher

Judgement is restricted to lines the compiler resolved to **exactly one target**,
and that restriction is load-bearing because neither looser option is sound.
Matching by line alone is too generous — on GraphLume's own source, 25.8% of
referenced lines carry more than one distinct target, so a heuristic edge could
be scored against a reference it was never about. Matching by line *and* column
is too strict — the two tools do not share a column convention (for
`svc.Method()` the resolver records the column of `svc` and `scip-go` records
the column of `Method`), and requiring exact columns once discarded two thirds
of the comparison without either tool being wrong.

Everything undecidable is reported as uncovered rather than scored. A
measurement that quietly counts cases it cannot decide is worse than a smaller
one that says so.

### Gating on it in CI

`doctor`'s exit status is deliberately **not** affected by the measurement. Add
`--json` and the report lands under `resolution_precision` alongside the checks,
for you to threshold yourself.

---

## 2. Retrieval quality

```bash
graphlume bench fixtures/bench/payment.yml
graphlume bench fixtures/bench/payment.yml --fail-on-regression   # exit 1 on any failure
```

Reproduced against a copy of the bundled payment fixture:

```
CASE                              RESULT  RECALL  TOKENS  MISSING
change-transaction-status-column  pass    100%    1567    —
idempotent-callback               pass    100%    1368    —
notification-failure-handling     pass    100%    1388    —

3/3 passed, mean recall 100%.
```

A case names a task, the code a correct answer must contain (`expect`), and the
code it must not (`forbid`):

```yaml
- id: idempotent-callback
  task: Add idempotency to payment callback processing
  expect: [CallbackController, TransactionService, TransactionRepository, transactions]
  forbid: [MarketingReport]
  max_tokens: 8000
```

**The `forbid` list is what makes the score mean anything.** Recall alone is
trivially gamed by returning the whole repository, and a forbidden item
retrieved is reported as a precision failure rather than averaged into the mean.

**What it does not measure:** whether an agent completed the task. Only whether
the context contained what a human said it should, and whether it stayed inside
its budget. Those are different claims, and conflating them would make a
retrieval regression indistinguishable from a bad day at the model provider.

To benchmark your own repository, write your own cases. The bundled file is a
worked example, not a standard.

---

## 3. Determinism

```bash
graphlume index --full --digest
```

The digest is a sha256 over one run's content. The property is verified **both
ways**: stable across worker counts, and *sensitive* to a single added symbol. A
digest that never changed would pass the stability half while detecting nothing.

Reproduced on this repository's working tree, three full rebuilds from an empty
database:

```
Digest: 74f82dd64551014cbaad1be87228d2ac720c7ac0ca49ccfd856eb0bfe4a5d54b  13493ms  (workers=1)
Digest: 74f82dd64551014cbaad1be87228d2ac720c7ac0ca49ccfd856eb0bfe4a5d54b   7346ms  (workers=4)
Digest: 74f82dd64551014cbaad1be87228d2ac720c7ac0ca49ccfd856eb0bfe4a5d54b   5731ms  (workers=8)
```

Three rules make this possible, and `internal/diffgraph` inherits all three:
rows are read under an explicit total `ORDER BY` over content columns rather
than by rowid; autoincrement ids are excluded and edges reference their
endpoints by `(file path, fqn, start line, start col)`; and no timestamps are
covered, only content hash and size.

This is not a formality. Running the precision measurement above is what
surfaced a determinism defect nothing else could: three distinct digests across
six repeats over byte-identical source, caused by a "first declaration wins"
rule reading from a `SELECT` with **no `ORDER BY`** — and an unordered `SELECT`
has no first. [accuracy.md](accuracy.md) has the full account. The transferable
rule is that `ORDER BY` is a correctness clause whenever a query feeds a
first-wins rule, and that a digest checked only in CI against small fixtures
will not catch an ordering bug that needs a real name collision to appear.

Also worth running alongside:

```bash
graphlume doctor --verify-index      # graph invariants
go test -race ./...                 # full suite
```

---

## 4. Performance

Every figure in [performance.md](performance.md) names the corpus it came from
and prints three trials rather than an average, because the spread between
trials on a shared developer laptop is the honest picture.

The method, reduced:

```bash
CGO_ENABLED=0 go build -o /tmp/di-docs ./cmd/graphlume
cp -R <corpus> /tmp/work/repo && cd /tmp/work/repo && git init && git commit -am init
for trial in 1 2 3; do
  rm -rf .graphlume && /tmp/di-docs init
  /tmp/di-docs index --full          # full index into an empty database
  /tmp/di-docs index                 # re-index, nothing changed
  echo '// touched' >> <first file>
  /tmp/di-docs index                 # re-index, one file changed
  /tmp/di-docs resolve
done
```

Durations are the ones GraphLume prints itself, so they exclude process start.
Database sizes are `index.db` on disk. Peak memory is
`/usr/bin/time -l … | grep 'maximum resident set size'`.

Reference points for "is my run in the right range", from performance.md's three
real repositories on `darwin/arm64` native:

| Repository | Files indexed | Symbols | Index time | Peak RSS |
|---|---|---|---|---|
| prometheus (Go) | 1,030 | 21,429 | 8.8 s | 664 MB |
| nest (TypeScript) | 1,929 | 13,916 | 3.7 s | 590 MB |
| spring-boot (Java) | 9,236 | 94,304 | 46.4 s | 756 MB |

Reproduced here on this repository's working tree: 422 files indexed, 6,259
symbols, full index 10.0 s, `resolve` 1.3 s, `scip import` of a 12.7 MB index
1.8 s.

Two things a real repository showed that a synthetic one could not, both
recorded in performance.md: `DefaultDrainThreshold` at 1 GiB meant the pool
never drained on a 1,262-file Go repository and the threshold *was* the peak;
and `MaxInFlightBytes` does not bound memory despite having said so — it bounds
admission, while the drain threshold bounds memory.

---

## 5. Agent benchmark

Does an agent do better with GraphLume than with search alone? This is the
measurement other code-graph tools publish, and the one that matters most. One
round has been run. Its result is not flattering and is reported as measured.

### Round 1: direct callers of a uniquely named function (2026-11-12)

**Setup.** Five functions in this repository, each with a name defined exactly
once, with 1, 2, 4, 11 and 33 real callers. Each question was asked of two
agents: one limited to ordinary file search and reading, and one also given
`graphlume tool call callers`. Ten runs, one per task and arm, the same
model for all (Claude Sonnet), each agent starting cold.

**Ground truth** was computed without GraphLume: a 90-line Go program walks
every file's syntax tree and lists the functions whose bodies contain a call
expression naming the target. For a name defined once, that is exact.

| Function (callers) | Arm | Correct | Tokens | Tool calls | Seconds |
|---|---|---|---|---|---|
| `staleAgainst` (1) | search only | 1 / 1 | 58,481 | 4 | 14.6 |
| | with GraphLume | 1 / 1 | 58,808 | 4 | 21.0 |
| `checkListenAddr` (2) | search only | 2 / 2 | 58,546 | 3 | 12.1 |
| | with GraphLume | 2 / 2 | 59,815 | 4 | 21.5 |
| `mergeJSONKey` (4) | search only | 4 / 4 | 59,641 | 3 | 12.5 |
| | with GraphLume | 4 / 4 | 59,635 | 4 | 16.8 |
| `openReadOnly` (11) | search only | 11 / 11 | 59,649 | 3 | 21.3 |
| | with GraphLume | 11 / 11 | 59,477 | 3 | 14.2 |
| `openQuery` (33) | search only | 33 / 33 | 60,958 | 3 | 16.1 |
| | with GraphLume | 33 / 33 | 61,722 | 5 | 21.0 |
| **Total** | search only | 51 / 51 | 297,275 | 16 | 76.6 |
| | with GraphLume | 51 / 51 | 299,457 | 20 | 94.5 |

**Result: no advantage.** Both arms were right every time. The agents with
GraphLume used 0.7% more tokens, four more tool calls and 23% more time,
because each called the tool and then checked its answer with a search.

**What this does and does not show.**

- For a function whose name is unique, one search for `name(` is already a
  complete and exact answer, and an agent finds it in one step. A code graph
  cannot beat that, and this round confirms it does not.
- About 58,000 of each run's tokens are the agent's own fixed overhead, so the
  task itself cost a few thousand tokens in either arm. A benchmark of small
  lookups cannot show a large saving whichever way it goes.
- Five tasks, one run each, in one 400-file Go repository. It is an indication,
  not a statistic.
- It does not test where a search stops being enough: a name defined many
  times (`Close`, `Run`, `handle`), a method reached through an interface, what
  a change reaches several calls away, which tests cover a symbol, or a
  repository too large to search and read. Those are the cases the tool is
  built for, and none has been measured yet.

**Do not quote a token saving for GraphLume.** There is no measurement that
supports one.

The product follows the same rule. `graphlume stats` and the `stats`
tool report what a session used (calls, answer sizes, an estimated token count
of those sizes, time) and print no saving. The one comparison they make is the
size of the indexed files the answers named, labelled as an upper bound on
reading those files whole, which an agent with search would not necessarily
have done.

### Round 2: methods several types define, and tests one call away (2026-11-14)

Round 1 asked a question a search answers exactly. This round asks the
questions it was said a search could not: who calls a method whose name several
types share, and which tests reach it through one function in between. The
result is the same, and it is reported as measured.

**Setup.** This repository at commit `5121219` (1,068 files indexed, 13,628
symbols), a clean clone indexed and resolved by the build of that commit. Four
questions, each asked of two agents, eight runs, one at a time, the same model
for all (Claude Sonnet), each agent starting cold. One arm had file search and
reading only. The other also had `graphlume tool call` (`callers`,
`tests` and the rest), was told the index was current, and was free to
search as well.

**Ground truth** came from the Go type checker, not from GraphLume:
`scripts/bench/agenttruth` loads every package with `go/packages` and lists the
functions whose bodies contain a call the checker resolves to the target
method. The targets are methods on a name that at least four types define and
no interface in the repository declares.

| Question (true answers) | Arm | Correct | Wrong | Tokens | Tool calls | Seconds |
|---|---|---|---|---|---|---|
| Callers of `Edge.Validate`, 7 types define `Validate` (14) | search only | 14 | 0 | 65,855 | 7 | 42.3 |
| | with GraphLume | 13 | 1 | 66,679 | 9 | 40.8 |
| Callers of `Model.Validate` (11) | search only | 11 | 0 | 62,411 | 5 | 20.7 |
| | with GraphLume | 11 | 0 | 64,514 | 5 | 24.6 |
| Callers of `Confidence.Valid`, 4 types define `Valid` (37) | search only | 37 | 0 | 74,949 | 7 | 43.4 |
| | with GraphLume | 37 | 0 | 70,882 | 8 | 47.8 |
| Tests that call `Edge.Validate` or a function that calls it (30) | search only | 30 | 0 | 71,682 | 7 | 41.8 |
| | with GraphLume | 30 | 0 | 83,046 | 8 | 53.9 |
| **Total** | search only | 92 / 92 | 0 | 274,897 | 26 | 148.2 |
| | with GraphLume | 91 / 92 | 1 | 285,121 | 30 | 167.1 |

**Result: no advantage, again.** The agent with search alone was right on all
92 answers. The agents with GraphLume used 3.7% more tokens, four more tool
calls and 13% more time, and made the round's one mistake.

**Why, from what the agents did.**

- **The tool's own answer was incomplete on every question, and said so.** Of
  the true callers it had bound 5 of 14, 8 of 11 and 19 of 37 (and 2 of 8 for a
  fourth method, `IndexRun.Complete`, that was not put to the agents): a call
  written `e.Validate()` on a local variable is ambiguous to a resolver that
  does not know the variable's type, and four to seven types define the name.
  Each agent read `INCOMPLETE`, did the whole search anyway, and so paid for
  the tool call on top of the search.
- **A search with reading is exact at this size.** An agent finds every
  `.Validate(` in one command and settles each receiver's type by reading a few
  lines. In a thousand-file repository that takes five to seven steps.
- **The one wrong answer came from the tool's output.** `callers` prints a
  calling method by its bare name (`AddEdge`) without the type that owns it.
  The agent supplied the type and guessed `Registry.AddEdge`; it is
  `Writer.AddEdge`.
- **`tests` answered a different question.** It returned 97 tests, the
  closure at any depth, with no depth on a row, where 30 were one call away.
  The agent called it "noisy" and did not use it.
- Its first line says "N caller(s)" for N call sites: 56 for a method called
  from 18 functions.

**What this does and does not show.**

- Two rounds, nine questions, one repository of a thousand files, one model.
  It does not test a repository where a common name returns thousands of
  matches, a question several calls deep, or a language where reading does not
  settle a receiver's type.
- It does show that an answer the tool marks incomplete is worth nothing to an
  agent that can search: the agent must do the search to complete it, and then
  has no use for the part it was given. A code graph earns its place with an
  agent only where its answer is complete enough to act on without checking.
  For Go that means type-checked edges, which today require importing a
  compiler index by hand.

**Do not quote a token saving for GraphLume on the strength of round 2.**
Round 3 below is the first measured advantage, for Go only.

### Round 3: the same caller questions, with exact Go answers built in (2026-11-14)

Round 2 showed that an answer marked incomplete is worth nothing to an agent
that can search. Since then `resolve` type-checks Go with `go/types` from the
standard library (no external indexer), so every call in a checked file is an
exact edge, references the checker ruled out no longer make an answer
incomplete, and a caller is printed with its owner (`Writer.AddEdge`). The
three caller questions of round 2 were asked again, of a clean clone indexed
by the build of commit `4038617`, with the same wording and the same truth.

| Question (true callers) | Arm | Correct | Wrong | Tokens | Tool calls | Seconds |
|---|---|---|---|---|---|---|
| `Edge.Validate` (14) | search only (round 2) | 14 | 0 | 65,855 | 7 | 42.3 |
| | GraphLume, round 2 | 13 | 1 | 66,679 | 9 | 40.8 |
| | **GraphLume, exact Go** | **14** | **0** | **60,494** | **3** | **19.6** |
| `Model.Validate` (11) | search only (round 2) | 11 | 0 | 62,411 | 5 | 20.7 |
| | GraphLume, round 2 | 11 | 0 | 64,514 | 5 | 24.6 |
| | **GraphLume, exact Go** | **11** | **0** | **61,989** | **4** | **16.6** |
| `Confidence.Valid` (37) | search only (round 2) | 37 | 0 | 74,949 | 7 | 43.4 |
| | GraphLume, round 2 | 37 | 0 | 70,882 | 8 | 47.8 |
| | **GraphLume, exact Go** | **37** | **0** | **63,296** | **4** | **17.4** |
| **Total** | search only | 62 / 62 | 0 | 203,215 | 19 | 106.4 |
| | GraphLume, round 2 | 61 / 62 | 1 | 202,075 | 22 | 113.2 |
| | **GraphLume, exact Go** | **62 / 62** | **0** | **185,779** | **11** | **53.6** |

**Result: an advantage, for the first time.** Against search alone, the agents
with exact answers were as accurate, used 42% fewer tool calls, half the time,
and 8.6% fewer tokens. Each read a complete, compiler-proven list, checked a
few call sites by reading, and stopped, where in round 2 each did the whole
search.

**What this does and does not show.**

- The token saving is small because about 58,000 tokens of every run are the
  agent's own fixed overhead; the work itself shrank more than the total
  shows. The time and tool-call savings are the larger effect.
- Three questions, one run each, one repository of a thousand files, Go only,
  one model. The search-only figures are from round 2, run the day before on
  the same commit's tree with the same wording, not re-run.
- The question about tests one call away was not repeated: `tests`
  still returns the closure at any depth with no depth on a row (97 rows where
  30 were asked for), and is the next thing to fix.
- It says nothing yet about other languages, where the answers are still
  heuristic and round 2's result stands.

### Round 4: TypeScript, a method another class shares by name (2026-11-14)

honojs/hono at commit `f6fbaf0`, indexed by the build of commit `ed27e3e`, which
runs the installed scip-typescript and imports it (no `node_modules`). The
question: every call site under `src/` of `HonoRequest.header`, which
`Context.header` shares by name (`c.req.header(...)` against `c.header(...)`).
Truth from the TypeScript 5.9.3 compiler API over every file under `src/`: 63
sites, plus one real call (`src/client/client.test.ts:1152`) the truth program
does not reach and both arms listed, counted as correct.

| Arm | Correct (of 64) | Wrong | Tokens | Tool calls | Seconds |
|---|---|---|---|---|---|
| search only | 64 | 3 | 64,297 | 4 | 21.4 |
| with GraphLume | 64 | 0 | 70,175 | 5 | 29.8 |

**Result: more accurate, not cheaper.** Both found every site. The search-only
agent also listed three lines of a doc comment as calls; the agent with
GraphLume made no mistake. It used 9% more tokens and 39% more time: the
tool's answer was complete and compiler-proven, but the agent still ran a text
search to check it, and the answer carried 14 rows outside `src/` that it had
to read and drop. One question, one run per arm.

### Round 5: Python, a method thirteen definitions share (2026-11-14)

pallets/click, indexed and resolved by the build of commit `5dc2afb` with
scip-python installed. The question: every call site, in source and tests, of
`ParamType.to_info_dict` (`src/click/types.py:122`), which thirteen
definitions of the name share and which subclasses override or inherit. Truth
from jedi's `goto` on every call of the name: 9 sites. Reading found one more
that jedi misses (`tests/test_info_dict.py:276`, a class defined inside a test
function that does not override the method), so the truth used is 10.

| Arm | Correct (of 10) | Wrong | Tokens | Tool calls | Seconds |
|---|---|---|---|---|---|
| search only | 8 | 0 | 62,507 | 4 | 16.2 |
| with GraphLume | 10 | 0 | 63,194 | 5 | 29.2 |

**Result: more complete, not cheaper.** The search-only agent excluded two
calls made on a value declared `ParamType` (`self.type.to_info_dict()` and
`ty.to_info_dict()`) because at runtime they may reach an override; by static
type they reach line 122, which is what the truth counts. The agent with
GraphLume counted them. Its tokens were 1% higher and its time 80% higher.

**What the tool's own answer did.** It listed 9 of the 10 sites, all
compiler-proven, and opened INCOMPLETE: six references in
`tests/test_info_dict.py`, a file the compiler read, stayed ambiguous, and
line 276 was among them. The agent resolved them with a grep, which is the
check this tool exists to make unnecessary.

**Limits.** One question, one run per arm, one repository, one model. The
truth needed a manual correction, so it is a better measure of agreement
between the arms than of either one's accuracy. Java was not run: its compiler
indexer needs Maven, Gradle or Docker, none of which is available on the
machine used, so a Java run would measure only the heuristic answers.

**Round 5, after the Python environment fix (same day).** The six references
the tool left ambiguous were not a join bug: click's tests did `import click`,
which resolved to an older click in site-packages, so the compiler spelled the
checkout's symbols as another copy's. scip-python is now given the project's
`src` as an analysis path (a one-line `pyrightconfig.json` for the run; putting
`src` on `PYTHONPATH` was rejected because the indexer's interpreter would
import repository code). The compiler then proves 3,310 calls (was 902) and
the answer lists all 10 sites; `convert` and `get_metavar` match jedi
exactly. The agent again found 10 of 10 at 63,494 tokens, 6 tool calls and
25 seconds, against 62,507 tokens, 4 calls and 16 seconds for search alone: the
four references that remain unlisted (calls on untyped pytest parameters) are
real unknowns and keep the answer INCOMPLETE, so the agent still grepped.

### Round 6: Frontend & Full-Stack (Next.js / TSX, React / TSX, Vue 3, Gin) (2026-11-14)

Heavy open-source frontend and backend projects were benchmarked to verify resolution accuracy, index performance, and MCP caveat behavior on modern frontend frameworks (React, Next.js App Router, Vue 3 SFCs, and Tailwind CSS component wrappers):

| Repository | Tech Stack | Files | Index | Resolve | SCIP / Exact Edges | Relationships | Peak RSS |
|---|---|---|---|---|---|---|---|
| `shadcn-ui/taxonomy` | Next.js 13 App Router, React 18, TSX | 188 | 1.31 s | 2.09 s | 701 (613 TSX, 88 TS) | 701 | 185 MB |
| `tremorlabs/tremor` | React, TSX, Tailwind CVA | 198 | 1.59 s | 2.62 s | 1,564 (1,487 TSX, 77 TS) | 1,780 | 196 MB |
| `vuejs/core` | Vue 3 SFC, TypeScript monorepo | 704 | 9.64 s | 53.16 s | 31,677 (31,241 TS, 436 TSX) | 35,136 | 742 MB |
| `gin-gonic/gin` | Go backend framework | 130 | 2.18 s | 3.94 s | 6,352 exact | 6,352 | 215 MB |

**Key findings & fixes on frontend codebases:**

1. **JSX closing tag false-alarm elimination:** In component trees (e.g., `<MainNav>...</MainNav>`), closing tags `</MainNav>` could be recorded as unrecorded value uses in `internal/registry/valueuse.go`. This triggered spurious `INCOMPLETE FOR THIS SYMBOL ... Run search` warnings even when 100% of callers were compiler-proven, prompting LLM agents to distrust the graph and run redundant grep sweeps. Adding `isJsxClosing` eliminated this false alarm completely: `graphlume callers MainNav` on `taxonomy` now returns all 3 layout callers cleanly with 0 false-alarm caveats.
2. **Component wrapper resolution:** Added `"cva": true` (Tailwind Class Variance Authority) and `"dynamic": true` (Next.js dynamic imports) to `wrapperCallees` in `internal/parse/lang_javascript.go`, ensuring modern wrapped React components are catalogued as callable definitions rather than opaque variable constants.
3. **Caveat framing recalibration:** Updated `internal/mcp/signal.go` so when compiler-proven references exist (`References > 0`), the MCP response clarifies that proven callers are listed and directs dynamic search only if runtime coverage is explicitly required, ending the framing paradox.

**Reproducing round 2.** Build the truth program (`cd scripts/bench/agenttruth
&& go build -o agenttruth .`; it is its own module so that the tool does not
depend on `golang.org/x/tools`), then `agenttruth -dir <clone> list` for
candidate names, `callers Type.Method` and `tests Type.Method` for the truth.
Ask each agent the question in the form of round 1, naming the type and the
file and line of the method, and score the final `CALLERS:` or `TESTS:` line.

### Reproducing round 1

1. Index the repository: `graphlume init && graphlume index && graphlume resolve`.
2. Compute the truth for each target with a syntax-tree walk over the same
   files (exclude `fixtures/`).
3. Give each agent one question: "find every function or method whose body
   directly calls `<name>`, defined in `<file>`; answer `CALLERS: a,b,c`".
   Forbid the search-only arm from running `graphlume` or reading
   `.graphlume`; give the other arm the `callers` command.
4. Record tokens, tool calls and wall time from the agent runtime, and score
   the answer line against the truth.

---

## 6. Figures other tools publish

Three figures other code-graph tools publish and this project had not: how well
impact analysis predicts a change, how long an update takes after two files
change, and how long the Linux kernel takes to index. All three were measured on
2026-11-12 at commit `6fadd51`, on an Apple M4 Pro with 14 cores and 24 GiB that
other work was sharing. The harnesses are in `scripts/bench/`.

The two timings are in
[performance.md](performance.md#figures-other-tools-publish-measured-here), with
the commands that produced them. The accuracy measurement is here, because its
method needs more explaining than its result.

### The binary that was measured

```bash
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -o /tmp/bench/graphlume ./cmd/graphlume
/tmp/bench/graphlume version     # graphlume dev (commit none, built unknown, darwin/arm64)
```

The Go toolchain on the measuring machine is `darwin/amd64` under Rosetta 2
([pitfall 1](#pitfall-1-check-your-goarch-before-you-benchmark-anything)), so
the binary was cross-compiled to the machine's own architecture. An amd64 build
was not measured.

### Impact-analysis accuracy

**The question.** A commit changed a function. Did `impact`, asked about
that function at the commit's parent, name the other files the commit went on to
change?

```bash
git clone --depth 400 https://github.com/pallets/flask /tmp/bench/flask
go run -tags bench ./scripts/bench/impactaccuracy \
    -repo /tmp/bench/flask -bin /tmp/bench/graphlume -n 1000 -budget 20m
```

The harness is a Go program behind the `bench` build tag, so the default build,
vet and lint do not compile it. It uses the standard library only and drives the
binary through its command line, so it scores any build, including a released
one. Give it a clone made for the purpose: it checks out revisions and deletes
`.graphlume`. `-out results.jsonl` writes one line per commit examined.

**Method.** For each non-merge commit, newest first:

1. List the files it modified or deleted. A file it added did not exist at the
   parent, so nothing indexed there could name it; added files are left out of
   both sides.
2. Check out the parent, delete `.graphlume`, and run `init`, `index` and
   `resolve`. Every commit is scored against a fresh full index of its own
   parent, which is why the run is slow and why N is small.
3. Keep the changed files the index holds in a language GraphLume binds
   references for (the `typed` and `resolved` tiers of `graphlume languages`).
   A commit with fewer than two is skipped, and so is one with more than 50.
4. For each such file, the seed symbols are the innermost functions, methods,
   types, fields, constants and variables whose lines overlap a hunk of the
   diff.
5. Call `impact` for each seed symbol by its fully-qualified name, once
   per confidence floor (`min_confidence=high`, `medium`, `low`). The predicted
   set is the files of the rows returned, minus the seed file.
6. The truth is the other source files the commit changed.

Counts are summed over commits and then divided (micro-averaged). Three
aggregations are printed:

| Aggregation | Seed |
|---|---|
| One seed per commit, any file | The file with the most changed lines |
| One seed per commit, not a test file | The same rule over the files that are not tests |
| Every changed file as a seed | Each file of the commit in turn, against the rest |

The second exists because the first turned out to ask a question with an empty
answer. A commit that fixes a function and extends its test usually changes more
lines in the test, and nothing depends on a test. In gson the first rule chose a
test file in 37 of 65 commits. A test file is recognised by its path (`tests/`,
`*_test.go`, `test_*.py`, `*.spec.ts`, `*Test.java` and the like); the rule is in
`isTestPath`.

Every tally is printed beside a **baseline that needs no index**: predict the
other source files in the seed's own directory. It is scored on the same commits
against the same truth.

**The known bias, in both directions.** Files that change together are not all
dependents of one another, so a correct analysis is charged with "missing" files
it had no reason to name: recall is understated. And a commit does not touch
everything that depends on a change, so a real dependent that kept compiling is
scored as a false positive: precision is understated too. The truth is partial
on both sides. These figures compare one build with another on one history, and
the tool with the baseline. They are not the probability that a reported file
breaks.

**Results.** One seed per commit, not a test file; every row the tool returned.
`medium` is the floor `impact` applies when none is given.

| Repository | Language | Commits | Floor | Predicted | Correct | Wrong | Missed | Precision | Recall | F1 |
|---|---|---|---|---|---|---|---|---|---|---|
| GraphLume | Go | 14 | high | 73 | 8 | 65 | 42 | 11.0% | 16.0% | 0.130 |
| | | | medium | 77 | 8 | 69 | 42 | 10.4% | 16.0% | 0.126 |
| | | | low | 77 | 8 | 69 | 42 | 10.4% | 16.0% | 0.126 |
| | | | same directory | 591 | 11 | 580 | 39 | 1.9% | 22.0% | 0.034 |
| flask | Python | 74 | high | 198 | 31 | 167 | 216 | 15.7% | 12.6% | 0.139 |
| | | | medium | 239 | 38 | 201 | 209 | 15.9% | 15.4% | 0.156 |
| | | | low | 239 | 38 | 201 | 209 | 15.9% | 15.4% | 0.156 |
| | | | same directory | 1,147 | 104 | 1,043 | 143 | 9.1% | 42.1% | 0.149 |
| gson | Java | 57 | high | 342 | 30 | 312 | 194 | 8.8% | 13.4% | 0.106 |
| | | | medium | 384 | 34 | 350 | 190 | 8.9% | 15.2% | 0.112 |
| | | | low | 384 | 34 | 350 | 190 | 8.9% | 15.2% | 0.112 |
| | | | same directory | 851 | 35 | 816 | 189 | 4.1% | 15.6% | 0.065 |
| nest | TypeScript | 12 | high | 0 | 0 | 0 | 19 | — | 0.0% | 0.000 |
| | | | medium | 4 | 0 | 4 | 19 | 0.0% | 0.0% | 0.000 |
| | | | low | 5 | 0 | 5 | 19 | 0.0% | 0.0% | 0.000 |
| | | | same directory | 69 | 0 | 69 | 19 | 0.0% | 0.0% | 0.000 |
| zod | TypeScript | 10 | high | 3 | 1 | 2 | 19 | 33.3% | 5.0% | 0.087 |
| | | | medium | 4 | 1 | 3 | 19 | 25.0% | 5.0% | 0.083 |
| | | | low | 4 | 1 | 3 | 19 | 25.0% | 5.0% | 0.083 |
| | | | same directory | 200 | 6 | 194 | 14 | 3.0% | 30.0% | 0.055 |

The other two aggregations, at the default floor, with the baseline's F1 on the
same seeds:

| Repository | Aggregation | Seeds | Predicted | Correct | Wrong | Missed | Precision | Recall | F1 | Baseline F1 |
|---|---|---|---|---|---|---|---|---|---|---|
| GraphLume | any file | 14 | 53 | 6 | 47 | 44 | 11.3% | 12.0% | 0.117 | 0.047 |
| | every changed file | 56 | 185 | 18 | 167 | 374 | 9.7% | 4.6% | 0.062 | 0.053 |
| flask | any file | 80 | 208 | 32 | 176 | 225 | 15.4% | 12.5% | 0.138 | 0.150 |
| | every changed file | 301 | 621 | 161 | 460 | 1,976 | 25.9% | 7.5% | 0.117 | 0.264 |
| gson | any file | 65 | 241 | 25 | 216 | 223 | 10.4% | 10.1% | 0.102 | 0.080 |
| | every changed file | 300 | 634 | 55 | 579 | 3,506 | 8.7% | 1.5% | 0.026 | 0.167 |
| nest | any file | 12 | 4 | 0 | 4 | 19 | 0.0% | 0.0% | 0.000 | 0.000 |
| | every changed file | 16 | 4 | 0 | 4 | 29 | 0.0% | 0.0% | 0.000 | 0.000 |
| zod | any file | 11 | 4 | 1 | 3 | 21 | 25.0% | 4.5% | 0.077 | 0.048 |
| | every changed file | 15 | 5 | 1 | 4 | 30 | 20.0% | 3.2% | 0.056 | 0.065 |

Direct callers only, against every row (direct, indirect and dispatch), for the
non-test seed at the default floor:

| Repository | Rows | Predicted | Correct | Precision | Recall | F1 |
|---|---|---|---|---|---|---|
| GraphLume | direct | 14 | 4 | 28.6% | 8.0% | 0.125 |
| | all | 77 | 8 | 10.4% | 16.0% | 0.126 |
| flask | direct | 165 | 30 | 18.2% | 12.1% | 0.146 |
| | all | 239 | 38 | 15.9% | 15.4% | 0.156 |
| gson | direct | 216 | 32 | 14.8% | 14.3% | 0.145 |
| | all | 384 | 34 | 8.9% | 15.2% | 0.112 |
| nest | direct | 2 | 0 | 0.0% | 0.0% | 0.000 |
| | all | 4 | 0 | 0.0% | 0.0% | 0.000 |
| zod | direct | 2 | 1 | 50.0% | 5.0% | 0.091 |
| | all | 4 | 1 | 25.0% | 5.0% | 0.083 |

What each run covered:

| | GraphLume | flask | gson | nest | zod |
|---|---|---|---|---|---|
| Start revision | `6fadd51` | `49943b5` | `a961b0f` | `956af70` | `19c40d2` |
| Clone | full history | `--depth 400` | `--depth 400` | `--depth 400` | `--depth 400` |
| Files in the index at the start revision | 723 | 235 | 315 | 2,756 | 743 |
| Symbols | 10,743 | 2,152 | 5,687 | 19,293 | 6,255 |
| Relationships | 20,640 | 1,650 | 9,255 | 4,904 | 3,055 |
| Budget | 20 min | 20 min | 20 min | 20 min | 40 min |
| Stopped by | history exhausted | budget | budget | budget | budget |
| Commits scanned | 40 | 393 | 209 | 31 | 17 |
| Commits scored | 14 | 80 | 65 | 12 | 11 |
| Skipped: fewer than two files modified or deleted | 19 | 164 | 65 | 4 | 4 |
| Skipped: fewer than two changed source files | 5 | 147 | 78 | 14 | 2 |
| Skipped: more than 50 changed source files | 1 | 0 | 0 | 0 | 0 |
| Skipped: no indexed symbol on a changed line | 0 | 2 | 1 | 1 | 0 |
| Skipped: no parent in the clone | 1 | 0 | 0 | 0 | 0 |
| Commits with a non-test seed | 14 | 74 | 57 | 12 | 10 |
| `impact` calls | 243 | 3,165 | 2,052 | 81 | 93 |
| Answers that dropped rows at the 50-row cap | 21 | 75 | 90 | 0 | 0 |
| Answers whose walk stopped at a limit | 86 | 465 | 203 | 7 | 8 |
| Failed calls | 0 | 0 | 0 | 0 | 0 |
| Non-test seeds with no file predicted (default floor) | 2 of 14 | 32 of 74 | 31 of 57 | 10 of 12 | 8 of 10 |
| Seconds indexing parents, of the total | 255 of 403 | 405 of 1,201 | 602 of 1,202 | 1,143 of 1,356 | 1,346 of 2,679 |

The machine was loaded throughout, and far more so once the kernel index
recorded in performance.md started. The flask, gson and GraphLume runs were
almost over by then; the nest run overlapped it and the zod run was entirely
inside it, which is why the two TypeScript runs scored so few commits. A fresh
index of nest took 16 s early in the session and between 31 s and 249 s per
commit during its run, and zod's took between 42 s and 260 s. zod was given
40 minutes for that reason. The commit counts are what this machine could do on
the day, not what the harness needs.

**What the figures say.**

- **F1 is between 0.000 and 0.156.** Where there are enough commits to say
  anything (flask and gson), about one predicted file in six to one in eleven
  was changed by the commit, and about one changed file in seven was predicted.
  Read with the bias above, both are understated, and both are still low.
- **Against the same-directory rule the result depends on the seed.** With a
  non-test seed the tool has the higher F1 on four repositories and ties at zero
  on nest, by a wide margin on GraphLume and gson and by 0.156 to 0.149 on
  flask. With any file as the seed it is behind on flask (0.138 to 0.150). With
  every changed file as a seed it is ahead only on GraphLume, and on flask and
  gson the rule finds 5.6 and 17.8 times as many of the changed files (908
  against 161, and 980 against 55). Much of a change set is a neighbourhood,
  and a call graph is not a neighbourhood.
- **Precision is where the tool is ahead.** On every repository with a correct
  prediction, the tool's precision is 1.7 to 8 times the rule's for a non-test
  seed. Its recall is lower on all of them: it names few files, and the rule
  names many.
- **The confidence floor barely moves the result.** `medium` and `low` give the
  same counts on four of the five repositories and differ by one file on nest.
  `high` removes between 1 and 42 predicted files. These indexes carry few
  `low` edges, so the floor has little to remove.
- **Indirect rows cost precision.** Direct callers alone are 1.1 to 2.8 times as
  precise as the full answer on GraphLume, flask and gson. They find fewer
  files, and on gson the wider answer finds two more correct files for 166 more
  wrong ones.
- **The tool often has nothing to say.** 43% of the non-test seeds on
  flask, 54% on gson, 8 of 10 on zod and 10 of 12 on nest predicted no file at
  all. An empty answer costs recall and nothing else, so these F1 figures are
  set more by how often the tool had no caller to report than by how often a
  reported caller was wrong.
- **TypeScript is the weakest of the four languages**, and the sample is the
  smallest. nest scored no correct file in 12 commits, and neither did the
  baseline: nest keeps each package's tests in a separate `test/` directory,
  so the files next to a seed are rarely the ones its commit touched. zod
  scored one correct file in 10 commits. `graphlume resolve` bound 20.5% of
  nest's references that name something the index defines.
- **N is small.** 10 to 14 commits is an anecdote. 57 to 74 is enough to see
  the order of magnitude and not enough to rank two builds that differ by a
  point.

**Not comparable to the published figure.** Another code-graph tool publishes F1 0.693,
precision 0.546 and recall 1.000 over 13 commits in 6 repositories. Its README,
read on 2026-11-12, describes the truth as the changed files plus the files with
call or import edges into them, taken from the same graph the predictor walks,
and calls the recall of 1.0 a circular upper bound. That measures whether a
blast radius agrees with the graph it was computed from. The measurement here
asks whether it agrees with what a developer went on to change, which is a
harder question with a noisier answer. Neither number says anything about the
other, and the 0.1 here is not evidence that one tool is five times worse.

**What the run found.** Not fixed; recorded for the next change to the resolver.

| Finding | Measured |
|---|---|
| An overloaded method has no callers in the index | gson's `Gson.fromJson` has 11 overloads. All 792 references to `fromJson`, in 92 files, are recorded as `ambiguous` and bound to none of them. `impact` answers "0 direct, 0 indirect. Risk low (score 20)" for the library's main entry point. TypeScript overload signatures do the same: nest's `Reflector.getAllAndMerge` is declared three times, and all 12 references to it are `ambiguous` |
| Overloads cannot be told apart by name | All 11 share the fully-qualified name `gson/src/main/java/com/google/gson/Gson.java:fromJson`. Asking about `fromJson` returns "matches 14 symbols; re-ask with a fully-qualified name" followed by the same name five times. 972 of gson's 3,561 methods share a fully-qualified name with another symbol |
| An ambiguous name is reported as an internal failure | The same answer carries `error_code: internal` and "GraphLume failed internally", for a request the caller can correct |
| The documented default floor is wrong | `docs/reference/mcp-tools.md` and the tool's own schema say `min_confidence` defaults to `low` for `impact`. It defaults to `medium` |

To reproduce the first row:

```bash
git clone --depth 1 https://github.com/google/gson && cd gson
graphlume init && graphlume index && graphlume resolve
graphlume tool call impact symbol=fromJson
graphlume tool call impact symbol=gson/src/main/java/com/google/gson/Gson.java:fromJson
graphlume query "SELECT resolution, COUNT(*) FROM symbol_references WHERE index_run_id = :run AND name = 'fromJson' GROUP BY resolution"
```

### Reproducing the two timings

Both scripts print every run, average nothing, and need a clone they are allowed
to modify.

```bash
# Incremental update: cold index, then three two-file edits, then the same under `watch`.
git clone --depth 1 https://github.com/django/django /tmp/bench/django
scripts/bench/incremental.sh /tmp/bench/graphlume /tmp/bench/django \
    django/utils/text.py django/db/models/query.py

# Cold index, one step at a time, with wall time and peak memory per step.
git clone --depth 1 https://github.com/torvalds/linux /tmp/bench/linux
scripts/bench/cold-index.sh /tmp/bench/graphlume /tmp/bench/linux
```

`cold-index.sh` stops a step that passes `BENCH_MAX_SECONDS` (default 7,200) or
`BENCH_MAX_RSS_MB` (default none) and reports how far it got. Run it on an idle
machine if you can: the figures in performance.md were taken on a shared one and
say so.

## Pitfalls

### Pitfall 1: check your `GOARCH` before you benchmark anything

Every performance figure published before one particular revision of
performance.md was silently emulated. The development machine ran an amd64 Go
toolchain under Rosetta 2 on Apple Silicon for the entire life of the
repository, and Go's benchmark headers print `goarch: amd64` and
`cpu: VirtualApple @ 2.50GHz` — the emulated view, not the silicon.

Measured cost of the emulation on GraphLume's own source:

| Operation | amd64 under Rosetta | arm64 native | Native is |
|---|---|---|---|
| Full index | 1,801 ms | 1,264 ms | 1.43x faster |
| Re-index, nothing changed | 124 ms | 67 ms | 1.84x faster |
| Re-index, one file changed | 206 ms | 129 ms | 1.60x faster |

Query-side commands gain more, because they are dominated by process start:
startup went 64.4 ms → 20.4 ms (3.2x) on a 135,000-symbol corpus.

```bash
go env GOARCH ; uname -m      # if these disagree, your numbers are a ceiling
```

Treat the published figures as roughly 1.4–3x above what a native build
delivers. Released binaries are unaffected — CI cross-compiles `darwin/arm64`.

### Pitfall 2: re-baseline on the same tree before comparing anything

This is the pitfall that most often produces a wrong conclusion, and
accuracy.md's own "Changes since the table above was measured" section exists
because of it.

A `--depth 1` prometheus clone of 727 Go files scores **86.7%** on an unmodified
tree against the documented **86.8%** for a 1,030-file checkout. That difference
is the corpus, not a regression. Every figure in that section is therefore paired
with a baseline re-measured on the same tree and the same `scip-go` index, rather
than compared against the published number — because **a −2.4pp regression
measured against the wrong baseline reads as −0.1pp of rounding.**

The same applies to the run reproduced at the top of this page: 98.0% here and
97.8% in accuracy.md are two different trees, not a change.

### Pitfall 3: `--full` discards resolved edges

`graphlume index --full` rebuilds from scratch and drops the call graph. Re-run
`graphlume resolve` afterwards, or every relationship query will report that no
references have been resolved yet. A plain incremental `graphlume index` does not
have this effect.

Benchmark scripts that loop over `index --full` and then query without resolving
are measuring an empty graph.

### Pitfall 4: one index database holds one committed run

`registry.CommitRun` publishes a run and deletes the rows of the one it replaces.
So `graphlume diff` across two commits normally needs **two index files** — the
working index and a second index built at the other ref — which is what
`--from-index` and `--to-index` are for:

```bash
graphlume diff --from-index ../release-1.4/.graphlume/index.db --from 8f3c1ab --to 42
```

A ref that matches no indexed run fails and says so; it never falls back to
whichever run happens to be committed, because a diff against the wrong run is
worse than no diff at all.

### Pitfall 5: a filter that removes candidates is not automatically safe

Recorded here because it will recur, and because it was learned by measurement
rather than review. Two resolver changes adopted from a competitor both looked
plainly safe and neither survived measurement:

- "A bare call cannot name a method in Go or Rust" — true of the languages,
  false of the parser, which leaves `Receiver` empty when its query could not
  express the receiver. Every chained Go call was read as bare, and
  `graphlume callers Save` went from two bound callers to zero. **Every unit test
  passed throughout**, because they set `Receiver` directly; only the end-to-end
  CLI path exercised the parser.
- "Production code never binds to a test definition" — cost **2.4pp** of `high`
  precision. Shrinking the candidate set tipped references across the
  disambiguation thresholds, so 426 ambiguous references resolved: 64
  right, **362 wrong**. It converted honest ambiguity into confident wrong
  answers.

The rule the resolver now states: removing candidates also removes the ambiguity
that was protecting the caller. "Fewer candidates" and "fewer wrong edges" are
different claims, and only the second is worth having.

### Pitfall 6: benchmark with a stamped, matching build

Every index run records the build that wrote it, and a query from a different
build warns on stderr before answering. If you are comparing two measurements
and one of them printed this, the comparison is between two sets of resolution
rules:

```
level=WARN msg="This index was written by graphlume dev and you are running 9.9.9-test.
Resolution rules change between builds, so these results may come from different rules
than this binary would produce. Re-index to refresh."
```

Re-index rather than reasoning past it. `-q` silences the warning and does not
fix the cause.

---

## Reporting a number that disagrees

Include: the corpus and how it was obtained (`--depth 1` clone at a named
commit), `go env GOARCH` and `uname -m`, the `scip-*` version, the GraphLume
build, and the coverage line from `doctor --resolution-precision` — not only the
precision line. A precision figure without its coverage cannot be compared to
anything.

If a claim in these documents cannot be reproduced, the convention this project
follows is to **delete the number rather than restate it with a caveat**, keep
the reasoning it supported, and name the commit so the original evidence is one
`git show` away. Where a document and the code disagree, the code is the answer.
