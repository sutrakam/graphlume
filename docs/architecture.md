# Architecture

GraphLume turns a repository into a queryable knowledge graph. This document
describes the layers that do it, where the boundaries sit, and why each boundary
is where it is. Boundaries in this codebase are load-bearing: several of them are
the only thing standing between a heuristic guess and a claim presented as fact.

## The layers

```
                         .graphlume.yml        ~/.config/graphlume/config.yml
                        (UNTRUSTED, repo)         (TRUSTED, user)
                               |                        |
                               +-----------+------------+
                                           |
                                    internal/config
                                  resolved config.Config
                                           |
  +----------------------------------------v-----------------------------------+
  |                                                                            |
  |  acquire            internal/acquire/fs      internal/acquire/git          |
  |                     walk, gitignore, hash    HEAD, changed paths, recency  |
  |                              |                                             |
  |                              v  []fs.Discovered (sorted by path)           |
  |                                                                            |
  |  parse              internal/parse                                          |
  |                     tree-sitter tags -> symbols, references, imports       |
  |                     NO database. NO filesystem. NO network.                |
  |                              |                                             |
  |                              v  parse.Result                               |
  |                                                                            |
  |  analyze            internal/analyze  (api, db, config, deps)              |
  |                     endpoints, table accesses, config reads, manifests     |
  |                     pure functions over text + this file's symbol table    |
  |                              |                                             |
  |                              v  analyze.Result                             |
  |                                                                            |
  |  pipeline           internal/pipeline                                       |
  |                     workers, in-flight byte budget, arena drain, Sink      |
  |                              |                                             |
  |                              v  pipeline.FileFacts                         |
  |                                                                            |
  +------------------------------v---------------------------------------------+
                                 |          the persistence boundary
  +------------------------------v---------------------------------------------+
  |                                                                            |
  |  registry           internal/registry  -- the Repository adapter            |
  |                     index runs, two-phase swap, digest, invariant gate     |
  |                     owns SQL; returns pkg/model, never a *sql.Rows         |
  |                              |                                             |
  |  store              internal/store  -- the only package that names SQLite   |
  |                     migrations, DSN pragmas, FTS5 verification            |
  |                                                                            |
  +------------------------------+---------------------------------------------+
                                 |
  +------------------------------v---------------------------------------------+
  |                                                                            |
  |  resolve            internal/resolve/infer     internal/resolve/scip[import]|
  |                     heuristic binding, high..low   compiler facts, exact    |
  |                              |                                             |
  |  graph              internal/graph                                           |
  |                     level-synchronous BFS, confidence tracked per path      |
  |                              |                                             |
  |  search             internal/search                                          |
  |                     identifier tokenisation for FTS5. No internal imports.  |
  |                              |                                             |
  |  context            internal/context                                        |
  |                     retrieve, rank, budget -> one bounded pack             |
  |                              |                                             |
  |  capability         internal/capability  (impact, tests, architecture,      |
  |                     runtime). Query layers over the graph. None parse.     |
  |                              |                                             |
  |  mcp / cli          internal/mcp (`graphlume serve`)   internal/cli          |
  |                     bounded, framed responses over stdio; 24 CLI commands  |
  |                              |                                             |
  +------------------------------v---------------------------------------------+
                                 |
                          an AI coding agent
```

`pkg/model` is the domain core and sits underneath all of it. It imports `fmt`
and nothing else, which is checked on every run by
`tests/architecture/layering_test.go` rather than asserted here. Everything it
imports would become a dependency of every consumer of `model.Edge`,
`model.Symbol` and the confidence vocabulary — which is most of the product.

**`internal/registry` is the Repository adapter, not domain core.** It owns SQL
and imports `database/sql` by design; what it must never do is leak that
outward. It returns `pkg/model` types, never a `*sql.Rows`, a driver value or a
query string. [Engineering rules](development.md#engineering-rules) §1 and §2
are the authoritative statement of this. The registry is an adapter, not part of
the domain: it is built on SQLite and implements the Repository Pattern, so it
cannot also have zero dependencies on SQLite.

The arrow only points one way. An adapter may depend on the domain; the domain
and the layers above an adapter must not depend back on it. When the registry
needed to know whether an import path used a declared dependency, the answer was
a caller-supplied `registry.ImportMatcher`, not an import of
`internal/analyze/deps`. `TestRepositoryAdapterDoesNotDependUpward` fails if that
import comes back.

Everything above the persistence boundary is pure computation over bytes. The
single narrow contract across it is `pipeline.Sink`:

```go
type Sink interface {
    WriteFile(ctx context.Context, facts FileFacts) error
}
```

That one method is the whole reason `internal/pipeline` can be tested without a
database, and the reason the registry's storage decisions can change without any
parser noticing.

The diagram is not a call graph and the boxes are not strata a package may only
reach one level down. The direction is what is enforced: `internal/search`
imports no other GraphLume package at all, `internal/parse` imports only
`pkg/model`, `internal/graph` reaches `registry` and `store`, and
`internal/cli` — which assembles commands and owns no domain logic — reaches
almost everything. `go list -f '{{join .Imports "\n"}}' ./internal/...` is the
way to check that, and is worth preferring over this page.

## Boundaries, and why they exist

### `parse` never touches the database

`internal/parse` takes bytes and returns facts. It does no I/O of any kind. The
package comment states this as a hard rule, and it buys three things:

- Parsing runs across many goroutines with no coordination beyond a per-parser
  pool. A parser that could write would need to serialise on the single SQLite
  writer, which would make concurrency pointless.
- A parser test is a byte slice and an expected result. No fixtures on disk, no
  schema, no migration.
- Hostile input stays contained. A malformed file degrades to `StatusFailed` for
  that file and nothing else. There is no persistence state to corrupt because
  the package holds none.

### `parse` does not resolve references

Tree-sitter gives you a syntax tree per file. It cannot tell you that
`svc.updateStatus()` binds to `com.pay.TransactionService#updateStatus` — that
requires the complete symbol table for the repository, which does not exist until
the last file has been parsed.

So `parse` emits `parse.Reference` values that deliberately carry **no target**:

```go
type Reference struct {
    Name     string // "updateStatus"
    Kind     string // "reference.call"
    Receiver string // "transactionService", or "" when unqualified
    Range    model.Range
}
```

If parsing produced edges, syntactic guesses would enter the graph wearing the
same clothes as compiler-proven facts. The split is what keeps the confidence
model honest.

### `analyze` runs beside `parse`, but binds nothing

`internal/analyze` turns one parsed file into API and database facts: the
endpoints it declares, the tables its SQL touches, the configuration keys its
code reads, the dependencies its manifests declare. Like `parse` it is pure — it
imports `pkg/model` and its own four subpackages, and nothing else.

Attribution to the enclosing symbol happens here, because "the repository touches
`transactions`" is nearly worthless and "`TransactionRepository.save` writes
`transactions`" answers a real question. The rule is line containment against the
file's own symbols — the innermost definition whose range covers the statement —
the same rule `internal/resolve` uses for references, and for the same reason: it
is the one relationship the source states directly rather than implying.

Endpoints and tables become **symbols in the same graph**, not rows in a side
table. "What does this endpoint eventually write?" is a traversal, and it is only
a traversal if endpoints, functions and tables are all nodes.

What `analyze` deliberately does not do is bind. A `SELECT ... FROM transactions`
in a repository method names a table declared by a migration file that may not
have been parsed yet, and on an incremental update may not be reparsed at all.
Binding during indexing would mean parsing twice, or ordering the walk by file
type — and ordering a parallel walk to make one analyzer work is a constraint
that would outlive its reason. So accesses and config reads are staged
(`table_accesses`, `config_usages`) and bound in the same second pass as
references.

### `resolve` is a separate pass, and its output is persisted

References and imports are written to `symbol_references` and `file_imports`
(see `internal/store/migrations/0001_schema.sql`) rather than held in memory. Two reasons:

- Volume. Indexing GraphLume's own source — 262 files — extracts 17,931
  references against 3,955 symbols. Holding the references, the symbol table and
  the edges in memory scales with the repository, and the tree that motivated the
  memory work was forty times larger.
- Re-resolution — after the resolver improves, or once a compiler-backed SCIP
  index arrives — costs no reparsing. The expensive half of the work is already
  on disk.

Migrations 0003 (`table_accesses`) and 0004 (`config_usages`) are the same shape
for the same reason, which is why they exist as separate migrations rather than
columns on `symbols`. Three instances of one pattern: extract during indexing,
bind afterwards, keep the unbound fact rather than dropping it. An access that
binds to no table still states something true — "this service writes a table we
do not own" — and dropping it would make that unanswerable.

`internal/resolve/infer` returns edges and outcomes rather than writing them, so
the caller owns transaction boundaries and the package stays pure.

### `capability` and `context` query the graph and nothing else

`internal/capability` holds the questions an agent actually asks: impact ("what
breaks if I change this?"), tests ("what covers this?"), architecture ("does the
code match what we said we had?"), runtime ("what actually ran?"). None of them
parses, none writes, and none owns a schema. `internal/context` — retrieval,
ranking and budgeting into one bounded pack — is the same. Both import only
`registry`, `graph` and `pkg/model`.

The operational form of the rule is stated in `CONTRIBUTING.md`: adding a
language or an analyzer must never require editing the context engine. If it
does, the interface is wrong. That is what keeps language support a well-bounded
contribution instead of a change that ripples through the product.

Both layers declare their dependencies as ports rather than taking
`*registry.Registry` and `*graph.Graph` directly — `internal/context/ports.go`
is five registry methods and one graph method. Without that, the part of the
product with the most judgement in it and the least I/O could only be exercised
against a real SQLite database.

`ports.go` also records what that did *not* buy, and the honesty is the point:
the interface signatures still name `registry.Fingerprint`, `registry.FileMatch`
and `graph.Query`, so the package still imports those packages for their data
types and `go list -deps` still reaches the SQLite driver transitively. The ports
buy substitutability and testability. Severing the transitive import as well
would mean relocating those DTOs into `pkg/model`.

`internal/capability` carries one further rule worth naming here, because it is
structural rather than editorial. Every report it produces carries a
`Completeness` value, and `NewCompleteness` appends the lower-bound statement and
the dynamic-dispatch caveat itself rather than trusting each caller to remember
them. A static index resolves only part of any repository; an impact analysis
that under-reports makes a developer confident and wrong.

### `acquire/git` treats "not a repository" as normal

`git.Open` succeeds on a plain directory and reports `IsRepo=false`. GraphLume
indexes extracted tarballs and vendored trees, so callers branch on a boolean and
never on the text of an error. The git revision is metadata stamped onto the
index run for provenance; a missing one is recorded as empty and indexing
proceeds.

## The three invariants

These are enforced in code, in SQL, and by tests. They are not documentation of
intent.

### 1. Every fact carries provenance

An `Edge` without `Confidence` and `Evidence` is invalid. `model.Edge.Validate`
rejects it, and the `edges` table requires `confidence`, `evidence_file`,
`evidence_line` and `source` as `NOT NULL` columns with a `CHECK` on confidence.
`Evidence` names the exact file and range that justifies the relationship, so an
agent can verify rather than trust.

### 2. `exact` requires a compiler

```go
if e.Confidence == ConfidenceExact && e.Evidence.Source != SourceCompiler {
    return fmt.Errorf("edge %s %d -> %d: confidence=exact requires source=compiler, got %q", ...)
}
```

and, independently, in migration 0001:

```sql
CHECK (confidence <> 'exact' OR source = 'compiler')
```

and again in `registry.validateRun`, which re-counts violations across the whole
staging run immediately before it becomes visible.

Three checks for one rule is deliberate. The Go check lives in the writer; a
future writer, or a hand-run SQL statement during debugging, would bypass it. The
SQL `CHECK` covers inserts; a later migration could relax it. The commit-time
re-check is the gate neither can slip past. A bug in one of them must not be able
to turn a guess into a claim.

`internal/resolve/infer` never emits `ConfidenceExact` at all. That level is
reserved for compiler-backed SCIP data, which reaches the graph through
`graphlume scip import` and through `graphlume indexers install`. Edges at `exact`
are now ordinary rather than hypothetical: importing a `scip-go` index of
GraphLume's own source wrote 14,532 of them, and `graphlume doctor --verify-index`
still reported `unproven exact edges  none`.

### 3. Repository config cannot cause execution

`.graphlume.yml` is attacker-controlled the moment you clone an untrusted
repository. It may describe *what to index*. It may never influence *what to
run*. See "The trust-domain split" below for the mechanism.

## The two-phase atomic swap

A full index never mutates what readers can see. It builds a whole new run
alongside the current one and swaps.

Phase one — staging. `registry.BeginRun` inserts an `index_runs` row with
`status='indexing'` and `committed=0`. Every file, symbol, edge and search row
written during the run references that `index_run_id`. Readers filter on
`committed = 1`, so none of it is visible.

Phase two — publish. `registry.CommitRun` runs one transaction that:

1. re-validates the graph invariants across the staging run (`validateRun`),
2. counts rows and computes the registry digest over the content about to become
   visible,
3. sets the previous run's `committed = 0`,
4. sets this run's `committed = 1`, its status to `ready` (or `degraded` when
   there were parse failures), and stores the counts and digest,
5. deletes the previous run's rows.

Step 3 must precede step 4. A partial unique index forbids two committed rows:

```sql
CREATE UNIQUE INDEX idx_index_runs_current
    ON index_runs (committed) WHERE committed = 1;
```

SQLite checks unique indexes per statement, not at commit, so committing the new
run before un-committing the old one would fail. That ordering is invisible from
outside the transaction.

The property this buys: a crash at any instant either loses the new run
completely or publishes it completely. There is no observable intermediate state.
Rows from a run killed mid-index survive on disk but are unreachable;
`registry.ReclaimAbandoned` deletes them at the start of the next `graphlume
index`, which is why a long-lived index does not accumulate one carcass per
crash.

Cascade deletion depends on foreign keys being on. `files`, `symbols`, `edges`,
`symbol_references`, `file_imports`, `table_accesses` and `config_usages` all
declare `index_run_id ... ON DELETE CASCADE`, and `files_fts_map` reaches
`index_runs` one hop further along through `files`. That is why `deleteRunRows`
is a two-statement function ending at `DELETE FROM index_runs`. `files_fts` does
**not** — it is an FTS5 virtual table with no foreign key to cascade through —
so `deleteRunRows` deletes its rows explicitly, first, while the mapping rows
that identify them still exist. Orphaned FTS rows would otherwise pollute every
future search with hits on deleted files, and later abort indexing outright with
a UNIQUE violation once FTS5 recycles a rowid.

That cascade has a price, and the foreign-key indexes keep it small: a
`graphlume index --full` that retires a previous run costs about 15% more than a
first index into an empty database. See
[performance.md](performance.md#a-full-re-index-costs-about-what-a-first-index-costs).

### Incremental indexing makes a different trade

`graphlume index` without `--full` updates the committed run **in place**. Each
file is replaced in its own transaction, so the index stays continuously
queryable and the cost scales with the number of changed files rather than
repository size.

The trade is explicit: a full index is atomic all-or-nothing; an incremental pass
is atomic per file. Interrupting one leaves the index consistent but partially
updated. That is the right trade for a watcher that must not block an agent's
queries for the duration of a rebuild, and the wrong one for a rebuild you want
to be able to abandon.

Diffing is by **content hash, not mtime**. `git checkout` rewrites timestamps on
files whose content is unchanged, so an mtime-based diff would reparse the whole
repository after every branch switch.

Size and time decide only which files need hashing. A file whose size and
modification time match the index, and whose modification and inode change
times are more than two seconds older than the previous pass, is not opened;
everything else is hashed and the hash decides. The two-second margin is git's
"racily clean" rule: a time that close to the pass that read it cannot rule out
a second same-size write in the same second. `internal/acquire/fs/trust.go`
states the rule and what it does not cover, and `graphlume index --rehash`
reads everything.

A completed in-place pass moves the run's `started_unix` and `finished_unix` to
its own start and end (`registry.MarkUpdated`). They are the index age `status`
and tool provenance report, and `started_unix` is the moment the next pass
trusts file times against, so it is taken before discovery begins and written
only when the pass succeeds.

### The determinism digest

`registry.RegistryDigest` is a sha256 over the content of one run, and it is how
the pipeline proves it does not depend on scheduling. Three properties make it
work:

- **Order independence.** Rows are read under an explicit total `ORDER BY` over
  content columns, never by rowid.
- **No synthetic identity.** Autoincrement ids are excluded. Edges reference
  their endpoints by `(file path, fqn, start line, start col)` — content
  identity — rather than by `src_id`/`dst_id`, which are allocation artefacts
  that legitimately differ between two identical runs.
- **No timestamps.** `started`/`finished` are per-run by definition, and
  `mod_time_unix` differs after a fresh clone of the same commit. Content is
  covered by `content_hash` and `size` instead.

Fields are hashed length-prefixed rather than joined with a separator, so a doc
comment containing the separator cannot be crafted to collide with a different
row. The digest string is prefixed with `graphlume/registry-digest/v1`, which is
itself hashed, so an old digest can never silently compare equal under a new
scheme.

## The trust-domain split is a security boundary

Configuration lives in two files, and the division is not organisational.

`.graphlume.yml` — in the repository, committed, therefore **attacker-supplied**
when you clone someone else's code. It controls what gets indexed.

`~/.config/graphlume/config.yml` — writable only by the machine's owner,
therefore **trusted**. It controls what gets executed.

The reason: compiler-backed indexing runs build systems, and Maven and Gradle
plugins execute arbitrary code by design. If a repository could name the indexer
to run or the URL to fetch it from, `git clone && graphlume index` on a hostile
repository would be remote code execution.

The mechanism is structural rather than validating. `config.RepoConfig` has no
fields for accuracy mode, provisioning policy or indexer runner — they exist only
on `config.UserConfig`. And repo config is decoded with:

```go
dec.KnownFields(true)
```

so an unrecognised key is a **parse error**, not a silent no-op. A repository
that tries to set an execution field does not merely fail to apply it; the user
finds out:

```
$ graphlume status
error: repo config: parse .graphlume.yml: yaml: unmarshal errors:
  line 3: field accuracy not found in type config.RepoConfig
```

Two further asymmetries follow the same logic:

- `applyRepo` appends repo excludes to the defaults rather than replacing them,
  so a repository cannot un-exclude `.git` or `node_modules`. `fs.NewWalker`
  additionally discards any negated exclude pattern outright — rewriting `!**` as
  `**` would honour the letter of the rule while handing a repository a way to
  blank its own index.
- A repository cannot express an opinion about secret redaction at all.
  `RepoConfig` has no `security` block, so `security.redact_secrets` is a parse
  error like any other unknown key. Redaction is unconditional in
  `pipeline.RegistrySink`, so a key that honoured only `true` would decide
  nothing, and a key whose sole permitted value is the behaviour you already get
  reads as evidence that the behaviour is optional. Having no key keeps the
  asymmetry and hardens it.

`TestRepoCannotSetExecutionSettings` guards the execution split and
`TestRepoCannotDisableRedaction` guards the redaction one; CI runs both as a
named security gate (`-run 'TestRepoCannot' ./internal/config/...`).

## Storage and connection policy

SQLite via `modernc.org/sqlite` — pure Go, no CGO. `internal/store` owns the
connection policy, and one detail there is worth calling out because getting it
wrong is a real data-integrity bug.

**SQLite pragmas are per-connection.** Running
`db.ExecContext(ctx, "PRAGMA foreign_keys = ON")` borrows one connection from a
pool of eight, configures it, and returns it. The other seven run with foreign
keys off, so `ON DELETE CASCADE` silently does not fire — and a verification
query that follows can reuse the connection just configured, so the check cannot
detect it. A 30-second concurrent soak in that setup produces 1,010 dangling
symbols and 505 orphaned FTS rows.

Pragmas therefore go in the DSN, where every connection the pool opens gets them:

```
foreign_keys(1), busy_timeout(5000), synchronous(NORMAL),
temp_store(MEMORY), cache_size(-64000)
[+ auto_vacuum(INCREMENTAL), journal_mode(WAL) when writable]
```

and `verifyPragmas` samples **32 goroutines concurrently** — more than the pool
holds — because a single-connection check is precisely what cannot catch it.

`auto_vacuum(INCREMENTAL)` comes before `journal_mode(WAL)` because switching a
new file to WAL writes its first page, after which the mode can no longer be
chosen; on an existing index it changes nothing. It lets `store.Compact` return
free pages to the file system after `index`, `resolve` and `watch` write, once
they reach 64 MiB and 20% of the file
([performance.md](performance.md#automatic-compaction)).

FTS5 is verified explicitly at `init` and by `doctor`. `modernc.org/sqlite`
enables it via a codegen flag but does not document it, and undocumented means
unpromised.

<a id="what-is-not-built-yet"></a>

## What is built, and what is not

Stated plainly, because documentation describing features that do not exist is
worse than no documentation — and a "not built yet" list that outlives the work
is the same failure with the sign flipped. Every row below was checked by running
the command named in it against a copy of this repository.

| Layer | State |
|---|---|
| `acquire`, `parse`, `analyze`, `pipeline`, `registry`, `store` | shipped |
| `resolve/infer` — heuristic reference resolution | shipped (`graphlume resolve`) |
| `resolve/scip`, `resolve/scipimport` — compiler-backed resolution | shipped (`graphlume scip import`) |
| `indexer` — detecting, provisioning and running external SCIP indexers | shipped (`graphlume indexers list`, `graphlume indexers install`) |
| `graph` — traversal, neighbours, paths | shipped; consumed by `mcp`, `context` and `capability` |
| `search` — identifier tokenisation over FTS5 | shipped (`graphlume search`, and the `search` MCP tool) |
| `context` — the context engine | shipped (`graphlume context`, and `context` over MCP) |
| `capability/impact`, `capability/tests` | shipped; exposed as MCP tools |
| `capability/architecture` — governance against declared components | shipped (`graphlume arch`) |
| `capability/runtime` — trace correlation | shipped (`graphlume trace`) |
| `analyze/api`, `analyze/db` | shipped (`graphlume api`, `graphlume db`) |
| `analyze/config`, `analyze/deps` | shipped (`graphlume config`, `graphlume deps`) |
| `compile` — task contracts | shipped (`graphlume compile`) |
| `bench` — retrieval-quality measurement | shipped (`graphlume bench`) |
| `security` — secret redaction and path containment | shipped; applied during indexing |
| `watch` — incremental indexing on filesystem events | shipped (`graphlume watch`) |
| `mcp` — the MCP server | shipped (`graphlume serve`) |

None of the packages this table names is a placeholder.

### The limits that remain

**`accuracy.mode` selects indexers, but only under `graphlume indexers`.** `indexer.NewPlan`
and `indexer.Execute` branch on it in three places: `fast` returns before
detection and runs nothing (`plan.Note` says so), `auto` runs every step whose
toolchain is present and reports the rest as skips, and `exact` refuses to start
if any detected language is blocked — `Plan.Err` names the language and the
missing tool rather than publishing a run marked exact with guessed edges.
Pinned by `TestAccuracyModeSelectsWhatRuns` and
`TestExactModeFailsRatherThanDegrading` in `internal/config`, alongside
`internal/indexer`'s own tests.

What is still true is that **`graphlume index` does not consult the mode**. It
reads `cfg.Accuracy`, records the string on the run via `reg.BeginRun`, and never
calls the orchestrator; the `.scip` files `graphlume indexers install` produces
are printed for you to feed to `graphlume scip import` yourself. So `accuracy.mode
= exact` does not make `graphlume index` fail on a repository it cannot prove — it
only makes `graphlume indexers install` fail. Closing that means `internal/cli`'s
index path calling `indexer.NewPlan`/`Execute` and importing the results before
the run commits.

**The `minimal` MCP profile exposes 8 tools, which is not the whole capability
set.** `graphlume serve --list-tools` is the authority on which. Every exposed
tool's schema enters the agent's context on every request, so the default is
small deliberately — with the consequence that several shipped capabilities are
reachable from the CLI while an agent on the default profile cannot see them.

**There is no `security.redact_secrets`, because redaction is not
configurable.** Redaction is applied unconditionally in `pipeline.RegistrySink`:
`security.RedactFile` classifies by path first, so an entire `.env` or private
key is suppressed wholesale rather than pattern-matched line by line. Verified by
indexing a repository containing an AWS-shaped key in a `.go` file and a `.env`
holding credentials — neither value is findable through `graphlume search`, with
no configuration set (`tests/integration/secret_leak_test.go`). A key that could
only ever mean "on" would be read by nothing, so neither `RepoConfig` nor the
resolved `Config` has one, and `security:` in `.graphlume.yml` is a parse error
rather than a key that is accepted and ignored.

**The `context.*` limits are consulted on the CLI path and ignored on the MCP
path.** `context.BudgetFrom` is the consumer — all four of `max_files`, `max_symbols`,
`max_lines` and `max_graph_depth` become a `context.Budget`, and `graphlume
query`, `graphlume compile` and `graphlume bench` derive theirs through it
(`TestContextLimitsReachTheAssemblerBudget`). What does not is `internal/mcp`:
`getTaskContext` builds a bare `devctx.Request`, so `Budget.withDefaults` fills
it from `config.Defaults()` and a repository's configured limits do not reach an
agent. The fix is one line there — seed the request with
`devctx.BudgetFrom(s.deps.Config)` before clamping the agent's `max_files`
against `s.limits.MaxItems`, keeping the clamp, since the agent's argument is
untrusted in a way the repository's config is not.
