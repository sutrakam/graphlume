-- 0001_schema: the GraphLume index schema.
--
-- Load-bearing design rules, which every table below follows:
--
--   * Every edge carries confidence, evidence and index_run_id. Readers and
--     writers are built around them, so they are part of the schema from the
--     start rather than something to retrofit.
--
--   * index_runs.committed gates visibility. Readers only ever see rows from a
--     committed run, which is what makes a crash mid-index harmless. Every
--     run-scoped table cascades from index_runs, so a discarded or retired run
--     takes all of its rows with it.
--
--   * A recorded outcome is a fact, not an absence. A reference that resolved
--     to nothing, a table access that bound to no declared table and a manifest
--     that could not be parsed are all stored, because "we looked and found
--     nothing" and "we never looked" have opposite consequences.
--
--   * Defaults say "not recorded" and never claim something. A producer that
--     does not fill a column leaves it '' or 0, and every column's encoding is
--     chosen so that its default reads as "unknown" rather than as a value.
--
--   * No credential or configuration VALUE has anywhere to go. config_usages
--     and the infra_* tables store key names, identifiers, stable labels and
--     integers only. A schema with nowhere to put a value cannot leak one,
--     which is a stronger guarantee than any redaction list. Do not add a
--     value, command-line or free-form message column to any of them.
--
--   * SQLite does not index a foreign key. Every column that references
--     symbols(id) has an index whose leading column is that id, so deleting a
--     file's symbols (every incremental update) does not full-scan each child
--     table.
--
-- Tables come first, in foreign-key order, then indexes, then triggers.

-- ============================================================================
-- RUNS, FILES, SYMBOLS, EDGES
-- ============================================================================

-- One indexing run. A run moves indexing -> ready | degraded | failed and is
-- published by setting committed = 1; exactly one run is committed at a time
-- (idx_index_runs_current).
CREATE TABLE index_runs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    status          TEXT    NOT NULL CHECK (status IN ('indexing','ready','degraded','failed')),
    committed       INTEGER NOT NULL DEFAULT 0 CHECK (committed IN (0,1)),
    started_unix    INTEGER NOT NULL,
    finished_unix   INTEGER,
    git_revision    TEXT,
    accuracy_mode   TEXT    NOT NULL DEFAULT 'auto' CHECK (accuracy_mode IN ('fast','auto','exact')),
    files_indexed   INTEGER NOT NULL DEFAULT 0,
    symbols_found   INTEGER NOT NULL DEFAULT 0,
    edges_found     INTEGER NOT NULL DEFAULT 0,
    parse_failures  INTEGER NOT NULL DEFAULT 0,
    registry_digest TEXT,
    error           TEXT,

    -- Completeness: how much of what the run discovered it actually covered.
    -- The two-phase swap proves a run was published all-or-nothing; it cannot
    -- prove the published run covers every file the walker found. See
    -- internal/pipeline/reconcile.go for the invariant
    --
    --     files_indexed + files_skipped + parse_failures == files_discovered
    --
    -- index_state is separate from status because completeness is orthogonal
    -- to lifecycle: a run can be `degraded` (files failed to parse) and
    -- `partial` (files vanished without reaching any terminal outcome) at once.
    -- 'complete' with files_discovered = 0 means the run made no measurement.
    files_discovered  INTEGER NOT NULL DEFAULT 0,
    index_state       TEXT    NOT NULL DEFAULT 'complete'
                      CHECK (index_state IN ('complete','partial')),
    files_unaccounted INTEGER NOT NULL DEFAULT 0,

    -- The build that produced the run. An index is data plus the resolution
    -- rules that produced it, and a stale index does not announce itself:
    -- every query against it succeeds. The build identity is derived rather
    -- than hand-maintained, so it cannot be forgotten; it over-reports (two
    -- builds can be resolution-identical), which is the safe direction. ''
    -- means unknown provenance, which reads differently from a known-stale
    -- version and warrants different advice.
    producer_version  TEXT    NOT NULL DEFAULT '',

    -- Monotonic counter bumped on every mutation of the run, so caches keyed
    -- on it (the context engine's catalogue cache) invalidate exactly, in O(1).
    mutation_seq      INTEGER NOT NULL DEFAULT 0,

    -- Parser panics recovered during the run, persisted so `status` and
    -- `doctor` report them from any process.
    recovered_panics  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE files (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    index_run_id   INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    path           TEXT    NOT NULL,
    language       TEXT    NOT NULL DEFAULT '',
    content_hash   TEXT    NOT NULL,
    size_bytes     INTEGER NOT NULL DEFAULT 0,
    mod_time_unix  INTEGER NOT NULL DEFAULT 0,
    parse_status   TEXT    NOT NULL DEFAULT 'ok'
                   CHECK (parse_status IN ('ok','partial','failed','skipped')),
    parse_error    TEXT,

    -- HOW `language` was decided, as a langdetect.Method:
    --
    --   override             an index.language_overrides entry in .graphlume.yml
    --   filename             an exact name or compound suffix (Makefile, .blade.php)
    --   extension            an extension one language owns, or whose plain reading is safe
    --   shebang              the `#!` line
    --   content              an ambiguous extension settled by a content rule
    --   neighbours           a `.h` placed by the source files beside it
    --   extension_ambiguous  an ambiguous extension no rule settled: a guess
    --   ''                   nothing detected
    --
    -- A file whose language is a guess and whose parse yields nothing is more
    -- likely misrouted than empty, and this column is what lets it be listed.
    -- It is deliberately NOT part of the run digest (registry/digest.go):
    -- `language` already carries the outcome, and how it was reached does not
    -- change a symbol or an edge.
    detected_by    TEXT    NOT NULL DEFAULT '',

    UNIQUE (index_run_id, path)
);

CREATE TABLE symbols (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    index_run_id  INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    file_id       INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    kind          TEXT    NOT NULL,
    name          TEXT    NOT NULL,
    fqn           TEXT    NOT NULL,
    start_line    INTEGER NOT NULL DEFAULT 0,
    start_col     INTEGER NOT NULL DEFAULT 0,
    end_line      INTEGER NOT NULL DEFAULT 0,
    end_col       INTEGER NOT NULL DEFAULT 0,
    -- Not filled for code symbols by internal/parse; see return_type.
    signature     TEXT,
    doc           TEXT,
    exported      INTEGER NOT NULL DEFAULT 0 CHECK (exported IN (0,1)),
    confidence    TEXT    NOT NULL CHECK (confidence IN ('exact','high','medium','low')),
    source        TEXT    NOT NULL,

    -- How many parameters the definition declares, OFFSET BY ONE: 0 means
    -- UNKNOWN and a count of n is stored as n+1. A plain count defaulting to 0
    -- could not tell "takes no parameters" from "nobody counted", and those are
    -- opposite facts: the first narrows candidates, the second is the absence
    -- of evidence. With the offset, the SQL default and the Go zero value both
    -- say "unknown", so a producer not taught about arity is silent rather than
    -- wrong. pkg/model.Arity is the Go side of the encoding. Arity is an input
    -- to ranking, never a filter (internal/resolve/infer/resolver.go).
    param_count   INTEGER NOT NULL DEFAULT 0,

    -- The plain type name a function or method declares it returns (`Order`),
    -- as internal/parse reads it. It lets the resolver type a local initialised
    -- from a call (`order = repo.find(id); order.save()`). '' means no plain
    -- type was read: none declared, a primitive, or a type the parser
    -- deliberately does not unwrap (`Optional<Order>`, `Promise<Order>`, a
    -- type parameter). Kept out of `signature` so readers of that column never
    -- print a bare type as if it were a whole signature.
    return_type   TEXT    NOT NULL DEFAULT '',

    -- Bit set of facts the language itself writes, 0 when not read, so "not
    -- read" never claims a symbol is public:
    --   1  private: the language's own marker (`private` on a Java, C#, Kotlin
    --      or PHP member, `defn-` or `^:private` in Clojure, `static` on a C
    --      function). Private members are not inherited and not reachable
    --      from other units, which changes which candidate a call binds to.
    --   2  trait: a PHP `trait`, indexed as kind `interface`. A trait's method
    --      overrides an inherited one; an interface's does not.
    -- Separate from `exported`, which orders search results and tiers
    -- bindings and follows Go's upper-case rule (capability.ExportedKnown).
    flags         INTEGER NOT NULL DEFAULT 0,

    UNIQUE (index_run_id, fqn, file_id, start_line)
);

CREATE TABLE edges (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    index_run_id   INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    src_id         INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    dst_id         INTEGER NOT NULL REFERENCES symbols(id) ON DELETE CASCADE,
    kind           TEXT    NOT NULL,
    confidence     TEXT    NOT NULL CHECK (confidence IN ('exact','high','medium','low')),
    evidence_file  TEXT    NOT NULL DEFAULT '',
    evidence_line  INTEGER NOT NULL DEFAULT 0,
    evidence_col   INTEGER NOT NULL DEFAULT 0,
    source         TEXT    NOT NULL,
    detail         TEXT,
    -- The resolver's own claim, kept when a compiler upgrades the row.
    -- UpgradeEdges replaces a weaker edge IN PLACE (the UNIQUE key does not
    -- span confidence), which is right for the graph: one relationship, one
    -- row, at the best evidence available. `graphlume accuracy` scores the
    -- resolver, though, and without these columns every call the resolver got
    -- right would be rewritten to source = 'compiler' and vanish from its
    -- report while every wrong one stayed. These hold the (confidence, source)
    -- pair the row carried immediately before the first upgrade; '' means no
    -- upgrade ever touched the row. Both are stored because a tier alone is
    -- ambiguous across producers. History lives in columns rather than in a
    -- second row so `callers` never shows one call twice.
    prior_confidence TEXT NOT NULL DEFAULT '',
    prior_source     TEXT NOT NULL DEFAULT '',

    -- An 'exact' edge must come from a compiler. Enforced in SQL as well as in
    -- Go so a bad writer cannot corrupt the store's central promise.
    CHECK (confidence <> 'exact' OR source = 'compiler'),
    UNIQUE (index_run_id, src_id, dst_id, kind, evidence_file, evidence_line)
);

-- ============================================================================
-- FULL-TEXT SEARCH
-- ============================================================================

-- Whole-file text search. Not external-content: the `expanded` column has no
-- backing table column, so FTS5 owns this content outright. `expanded` holds
-- identifier-split text (handleCallback -> "handle callback"): the unicode61
-- tokenizer treats an identifier as one opaque token, so without it a search
-- for "callback" cannot find handleCallback (tests/integration/
-- fts5_guarantee_test.go).
CREATE VIRTUAL TABLE files_fts USING fts5(
    path,
    content,
    expanded,
    tokenize = 'porter unicode61'
);

-- Maps an fts rowid back to the real file row.
CREATE TABLE files_fts_map (
    fts_rowid INTEGER PRIMARY KEY,
    file_id   INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE
);

-- One row per function or method, keyed by the symbol's id: the identifier
-- parts of its name and body, de-duplicated (search.ExpandIdentifiers), so a
-- definition's length is its vocabulary and not its line count. files_fts
-- ranks whole files and bm25 normalises by length, so a long file holding the
-- one function a task is about ranks below many short files; these rows let a
-- context pack find, and tell apart, the definitions inside it.
--
-- CONTENTLESS, with contentless_delete: the text derives from files_fts, which
-- already stores the source, and a second copy would double the largest part
-- of the index. Rows are deleted by rowid (the symbol id) before a file's or a
-- run's symbols cascade away, as files_fts rows are. An index without rows here
-- is filled from the stored file text by registry.BackfillDefinitionText.
CREATE VIRTUAL TABLE defs_fts USING fts5(
    expanded,
    content = '',
    contentless_delete = 1,
    tokenize = 'porter unicode61'
);

-- ============================================================================
-- STAGED FACTS, BOUND IN A SECOND PASS
-- ============================================================================
--
-- References, imports, table accesses, configuration reads and client calls
-- are written while files are parsed and bound to definitions afterwards.
-- Binding needs the COMPLETE symbol table, which does not exist until every
-- file is parsed, and on an incremental update the target may not be reparsed
-- at all. The rows stay after binding, so re-binding (after a compiler index
-- arrives, a schema file appears or the resolver improves) needs no reparse.

CREATE TABLE symbol_references (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    index_run_id  INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    file_id       INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,

    -- The identifier as written, e.g. "updateStatus".
    name          TEXT    NOT NULL,
    -- Tree-sitter tag kind, e.g. "reference.call".
    kind          TEXT    NOT NULL,
    -- Qualifying expression where the syntax exposes one ("svc" in
    -- svc.updateStatus()). The resolver's strongest disambiguation signal
    -- after imports. Its shape is in receiver_kind.
    receiver      TEXT    NOT NULL DEFAULT '',

    start_line    INTEGER NOT NULL DEFAULT 0,
    start_col     INTEGER NOT NULL DEFAULT 0,
    end_line      INTEGER NOT NULL DEFAULT 0,
    end_col       INTEGER NOT NULL DEFAULT 0,

    -- Resolution outcome, so an unresolved reference is a recorded fact rather
    -- than an absence. "How much of this repository can we actually resolve?"
    -- is a question the product must answer honestly, and it is the number
    -- published as resolution precision.
    resolved_symbol_id INTEGER REFERENCES symbols(id) ON DELETE SET NULL,
    resolution         TEXT NOT NULL DEFAULT 'pending'
                       CHECK (resolution IN ('pending','resolved','ambiguous','unresolved','ignored')),

    -- How many arguments the call site passes, offset by one exactly as
    -- symbols.param_count is: 0 means unknown, n arguments are stored as n+1.
    arg_count     INTEGER NOT NULL DEFAULT 0,

    -- Who decided the CURRENT resolution: '' for the heuristic resolver (or
    -- not yet decided), 'compiler' for a compiler index import. Stored, not
    -- inferred, so provenance stays true however many producers exist.
    -- "Bound by a typechecker" and "bound by name matching" are different
    -- claims, and answers must not present the second as the first.
    resolution_source TEXT NOT NULL DEFAULT '',
    -- What the resolver concluded, preserved verbatim the first time another
    -- producer overwrote `resolution`. '' means nothing did, so `resolution` IS
    -- the resolver's answer. `graphlume accuracy` explains misses from it.
    prior_resolution  TEXT NOT NULL DEFAULT '',

    -- The shape of what a call was made on, a closed vocabulary written by
    -- internal/parse and read by internal/resolve/infer. A separate column
    -- because the shape is a separate fact from the text: capitalisation and
    -- in-band markers mean different things in different languages.
    --
    --   ''       not recorded, or a reference that is not a call (a supertype
    --            has no receiver); the resolver binds such a call at `low` at most
    --   'bare'   `f()`: the source wrote no receiver
    --   'name'   `x.f()`: one identifier, held in `receiver` as written
    --   'path'   `a.b.f()`: identifiers joined by the member operator, held in
    --            `receiver` as written
    --   'scope'  `a::f()`, `a::b::f()`: qualified through the language's scope
    --            operator, which names a module or a type and never a value
    --   'self'   `self.f()`, `this.f()`: the instance or type the call is in
    --   'super'  `super.f()`, `super().f()`, `base.f()`: the superclass
    --   'type'   `x.f()` where the source states x's type; `receiver` holds the TYPE
    --   'value'  `x.f()` where x is a local or parameter declared with no stated
    --            type: a value, so never an import, a module or a type
    --   'expr'   a call on an expression with no name (`g().f()`, `a[i].f()`);
    --            `receiver` holds '?'
    receiver_kind TEXT NOT NULL DEFAULT '',

    -- 1 on a call that binds to nothing BECAUSE its qualifier names something
    -- outside the repository (a standard-library or third-party import, or a
    -- type imported from one). Set only on positive evidence. `resolution`
    -- stays 'unresolved' for these rows, so counts of unresolved references
    -- still include them, while a reader can separate "left the repository"
    -- from "found nothing".
    external      INTEGER NOT NULL DEFAULT 0 CHECK (external IN (0,1)),

    -- On a call made on a local variable whose type the source does not state:
    -- the position of the call whose result initialised that variable, i.e.
    -- the start_line and start_col of that call's own row, in the same file.
    -- 0 means no such call was read. The resolver binds the producing call
    -- first and reads the return type of what it bound
    -- (internal/resolve/infer/returned.go).
    value_line    INTEGER NOT NULL DEFAULT 0,
    value_col     INTEGER NOT NULL DEFAULT 0,

    -- 1 on a call on a local value that is the whole right side of
    -- `x = x.f(...)`, 0 everywhere else. A reassignment normally withdraws the
    -- type a local got from its initialiser; this one keeps it when `f` returns
    -- the type `x` already has. The parser sees the shape, the resolver knows
    -- the return type, and this column carries the shape between them.
    reassigns     INTEGER NOT NULL DEFAULT 0,

    -- Bit set of facts about how the call is written, 0 when not read, so
    -- "not read" is never "no block":
    --   1  block: the call passes a block (`do ... end` or `{ ... }`). Read for
    --      Ruby only, where a bare call with a block is usually a DSL method of
    --      the object the enclosing block runs against, not a same-file helper.
    flags         INTEGER NOT NULL DEFAULT 0
);

-- Imports per file: the resolver's primary disambiguation signal. Knowing a
-- file imports com.pay.repository narrows `repository.save(...)` to one
-- package instead of every `save` in the repository; without imports,
-- resolution degrades to name matching, which the design rejects.
CREATE TABLE file_imports (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    index_run_id INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    file_id      INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    path         TEXT    NOT NULL,
    alias        TEXT    NOT NULL DEFAULT '',
    start_line   INTEGER NOT NULL DEFAULT 0,
    UNIQUE (file_id, path, alias)
);

-- SQL statements' table accesses, bound to table symbols in a second pass:
-- `SELECT ... FROM transactions` may sit in a file parsed before the migration
-- that declares `transactions`. Once bound they produce READS/WRITES/DDL edges.
--
-- The access mode lives here and not only on the edge: an access that binds to
-- no table (a table declared outside this repository, the normal case for a
-- shared database) still says what the code does, and dropping it would make
-- "this service writes a table we do not own" unanswerable.
CREATE TABLE table_accesses (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    index_run_id  INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    file_id       INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,

    -- The table as written, lowercased and unqualified. A schema prefix is
    -- stripped at extraction: `public.transactions` and `transactions` are one
    -- table, and keeping both would split every question asked about it.
    table_name    TEXT    NOT NULL,

    -- READ | WRITE | READ_WRITE | DDL | UNKNOWN. Not a CHECK constraint with a
    -- fixed list: the analyzer owns this vocabulary, and a schema that has to
    -- be migrated to add a mode would push analyzer design into the store.
    access        TEXT    NOT NULL,

    -- The SQL verb, kept for diagnosis. "Why is this a WRITE?" is answered by
    -- "because the statement is an UPDATE" far more usefully than by a mode
    -- alone.
    verb          TEXT    NOT NULL DEFAULT '',

    -- The symbol whose body contained the statement, resolved at extraction
    -- time by line containment. NULL when the statement sits outside every
    -- definition — a migration file's top level, or a package-level constant —
    -- which is a real and common case rather than a failure.
    src_symbol_id INTEGER REFERENCES symbols(id) ON DELETE CASCADE,

    start_line    INTEGER NOT NULL DEFAULT 0,

    -- False when the statement looked like a run-time fragment. Recorded rather
    -- than used to discard, so a dynamically-assembled query still reports the
    -- table it names, with the confidence that deserves.
    certain       INTEGER NOT NULL DEFAULT 1,

    -- Binding outcome. An access that matched no declared table is a recorded
    -- fact, not an absence: it usually means the table is owned by another
    -- service, and that is worth reporting rather than losing.
    bound_symbol_id INTEGER REFERENCES symbols(id) ON DELETE SET NULL,
    binding         TEXT NOT NULL DEFAULT 'pending'
                    CHECK (binding IN ('pending','bound','unbound'))
);

-- Configuration reads, bound to declared keys in a second pass: the key
-- `os.Getenv("DATABASE_URL")` reads may be declared by a .env or
-- application.yml parsed after the code that reads it.
--
-- NO VALUE COLUMN, AND THERE MUST NEVER BE ONE. Secret values are never
-- exposed, and internal/analyze/config implements that as an absence rather
-- than a filter: nothing upstream reads a value, so nothing can be written
-- here. A redaction list can be wrong about a key nobody anticipated; a schema
-- with nowhere to put a value cannot.
CREATE TABLE config_usages (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    index_run_id  INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    file_id       INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,

    -- The key as written at the call site.
    key_name      TEXT    NOT NULL,
    -- How it was read: env | spring-value | viper | config. Distinguishes a
    -- process environment variable from an application setting, which matters
    -- when deciding where a missing key has to be added.
    mechanism     TEXT    NOT NULL DEFAULT '',

    -- The definition whose body performed the read, by line containment. NULL
    -- for a read at file scope, which is ordinary in scripts and module-level
    -- configuration.
    src_symbol_id INTEGER REFERENCES symbols(id) ON DELETE CASCADE,
    start_line    INTEGER NOT NULL DEFAULT 0,

    bound_symbol_id INTEGER REFERENCES symbols(id) ON DELETE SET NULL,
    binding         TEXT NOT NULL DEFAULT 'pending'
                    CHECK (binding IN ('pending','bound','unbound'))
);

-- Outbound HTTP calls, bound to the endpoints they consume in a second pass.
-- An EXPOSURE (a route and its handler) is intra-file and resolves during
-- indexing; a CALL is not: `fetch("/api/orders/42")` lives in a component and
-- the route that answers it may be parsed later, or not at all on an
-- incremental update. Once bound a call produces a CONSUMES edge from the
-- calling declaration to the endpoint node.
--
-- An unbound call is kept: a call no route here exposes is usually a call into
-- ANOTHER service, which is worth answering, and it is the denominator of "how
-- many of this component's calls did we resolve".
--
-- `path` is stored as written. The matching key (scheme and host stripped,
-- query removed, parameters folded to a wildcard, lowercased) is derived at
-- binding time and never persisted in its place: a canonicalised path is a
-- path the developer cannot find with grep.
CREATE TABLE client_calls (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    index_run_id  INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    file_id       INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,

    -- The verb as the syntax states it, uppercased. Empty means the call states
    -- none this scan could read; it is treated as ANY at binding time and never
    -- guessed at.
    method        TEXT    NOT NULL DEFAULT '',

    -- The LITERAL part of the target, exactly as written. For a dynamic target
    -- this is the constant prefix and may be empty.
    path          TEXT    NOT NULL DEFAULT '',

    -- Which calling convention matched (fetch | axios | go-net-http), kept so a
    -- report can say how a call was found rather than presenting all calls as
    -- equally certain.
    client        TEXT    NOT NULL DEFAULT '',

    -- The declaration whose body contains the call, resolved at extraction time.
    -- NULL when the call sits outside every declaration — a module-level
    -- constant holding a prefetch — which is real and common rather than a
    -- failure.
    src_symbol_id INTEGER REFERENCES symbols(id) ON DELETE CASCADE,

    start_line    INTEGER NOT NULL DEFAULT 0,

    -- True when the target was assembled at run time rather than written out.
    -- Recorded rather than used to discard: a dynamic call is a real request
    -- whose target is unknown, and it still counts in coverage.
    dynamic       INTEGER NOT NULL DEFAULT 0,

    -- The extractor's confidence in the CALL, before any binding evidence is
    -- weighed. The bound edge's confidence descends from this.
    confidence    TEXT    NOT NULL DEFAULT 'low',

    -- Binding outcome. `unbound` is a recorded fact, not an absence.
    bound_symbol_id INTEGER REFERENCES symbols(id) ON DELETE SET NULL,
    binding         TEXT NOT NULL DEFAULT 'pending'
                    CHECK (binding IN ('pending','bound','unbound'))
);

-- ============================================================================
-- DEPLOYMENT FACTS
-- ============================================================================
--
-- What internal/analyze/infra reads from Dockerfiles, compose files,
-- Kubernetes manifests, Helm charts and Terraform. Source code cannot answer
-- "which service does this code run in": that fact exists only in a compose
-- file's build context and a Deployment's image.
--
-- THERE IS NO VALUE COLUMN IN THESE TABLES, AND THERE MUST NEVER BE ONE.
--
-- These files hold credentials. What is stored is the LEFT-HAND SIDE:
-- infra_keys.name is a key name, infra_keys.ref names the Secret or ConfigMap a
-- value comes from, and there is no third column. The extractor's types
-- (infra.EnvKey, infra.KeySet, infra.Unit) have no field a value fits in, so
-- nothing could be inserted here even by mistake. Also deliberately absent:
--
--   * The container command line. It is assembled from arbitrary manifest text
--     (`/app --token=…` shaped); redaction is a good second line of defence and
--     a poor first one. `graphlume infra --disk` prints it and forgets.
--
--   * Terraform attribute values, which the extractor never reads: it scans
--     block HEADERS only.
--
--   * A parser's error message. A parser error QUOTES THE TEXT IT CHOKED ON,
--     and redaction matches credential shapes, not arbitrary strings, so a
--     message can carry somebody's password. infra_problems stores a stable
--     reason label plus file and line instead.
--
-- Every column therefore holds a key name, an identifier, a stable label or an
-- integer. There is no free-form file text anywhere in them.
--
-- UNITS ARE SYMBOLS. Edges reference symbols, so a deployment unit that is
-- only a row could never be joined to the handler that runs inside it or the
-- configuration key that restarts it. The side tables hold what a symbol row
-- cannot (ports, limits, env sources). Env key names become symbols of kind
-- `configuration`, the same kind internal/analyze/config produces for a .env
-- or properties key, so registry.BindConfigUsages binds `os.Getenv("X")` in
-- source to the manifest's key and "what does changing this key restart" is a
-- two-hop query.
--
-- DEFAULTS. Every TEXT column defaults to '' and every INTEGER to 0, and none
-- is nullable except the foreign keys into symbols and infra_units. '' and 0
-- mean "the manifest stated nothing here", a fact about the manifest (a port
-- with published_port = 0 is one the host cannot reach). NULL would force
-- every reader through COALESCE and split identical content across two sort
-- positions in the run digest. symbol_id and unit_id are nullable because they
-- record a relationship that may not exist, and ON DELETE SET NULL turns a
-- deleted node into a visible gap rather than a dangling id.

CREATE TABLE infra_units (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    index_run_id  INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    file_id       INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,

    -- The graph node for this unit. NULL only when the symbol could not be
    -- written, which is a defect worth seeing rather than papering over.
    symbol_id     INTEGER REFERENCES symbols(id) ON DELETE SET NULL,

    -- As declared: a compose service name, `deployment/container`, a build
    -- stage name, or `resource "aws_db_instance" "main"`.
    name          TEXT    NOT NULL,

    -- What kind of thing it is — "service", "Deployment", "stage",
    -- "resource:aws_db_instance". One column rather than four tables, for the
    -- reason infra.Unit is one type rather than four structs.
    unit_type     TEXT    NOT NULL DEFAULT '',

    -- Which manifest family declared it: dockerfile | compose | kubernetes |
    -- helm | terraform. Stored rather than derived from the path, because
    -- classification is the extractor's judgement and a reader comparing two
    -- runs must see the judgement that was actually made.
    format        TEXT    NOT NULL DEFAULT '',

    image         TEXT    NOT NULL DEFAULT '',

    -- The SOURCE DIRECTORY this unit is built from, and the Dockerfile within
    -- it. This is the one field in the entire schema that ties a running
    -- service back to code, and it is what BindInfraUnits joins on.
    build_context TEXT    NOT NULL DEFAULT '',
    dockerfile    TEXT    NOT NULL DEFAULT '',

    -- The last stage of a multi-stage build: the one that becomes the image.
    -- Earlier stages are toolchains, and reporting them as things that run
    -- would be wrong.
    final         INTEGER NOT NULL DEFAULT 0 CHECK (final IN (0,1)),

    start_line    INTEGER NOT NULL DEFAULT 0,

    -- One unit per (run, file, name, line). A compose file cannot declare the
    -- same service twice, and this makes re-staging a file idempotent instead
    -- of doubling its units.
    UNIQUE (index_run_id, file_id, name, start_line)
);

CREATE TABLE infra_ports (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    index_run_id  INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    unit_id       INTEGER NOT NULL REFERENCES infra_units(id) ON DELETE CASCADE,

    -- Inside the container: EXPOSE, containerPort, the right-hand side of a
    -- compose mapping.
    container_port  INTEGER NOT NULL DEFAULT 0,
    -- Reachable from outside: a compose host port, hostPort, Service port or
    -- nodePort. Zero is the answer to "is this reachable", not a missing value.
    published_port  INTEGER NOT NULL DEFAULT 0,
    protocol        TEXT    NOT NULL DEFAULT '',
    name            TEXT    NOT NULL DEFAULT '',

    -- A declaration no number could be read from: "${PORT}:80", a range, a
    -- named targetPort. Kept because a port that resolves at deploy time is
    -- still a port, and dropping it would under-report what a service exposes.
    raw             TEXT    NOT NULL DEFAULT '',

    start_line      INTEGER NOT NULL DEFAULT 0
);

-- Configuration KEY NAMES. Read the DEPLOYMENT FACTS header before adding a
-- column.
CREATE TABLE infra_keys (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    index_run_id  INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    file_id       INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,

    -- The unit that sets this key, or NULL when the key belongs to a ConfigMap,
    -- a Secret, a Helm values file or a Terraform variable block — a key set
    -- that exists independently of any one workload.
    unit_id       INTEGER REFERENCES infra_units(id) ON DELETE CASCADE,

    -- The key set's name and type when unit_id is NULL. Denormalised rather
    -- than given a table of its own: a key set has a name, a type and a line
    -- and nothing else, and a two-column table joined on every read would buy
    -- nothing.
    key_set       TEXT    NOT NULL DEFAULT '',
    key_set_type  TEXT    NOT NULL DEFAULT '',

    -- The `configuration` symbol this key name became.
    symbol_id     INTEGER REFERENCES symbols(id) ON DELETE SET NULL,

    name          TEXT    NOT NULL,

    -- Where the value comes from: literal | secret-ref | configmap-ref |
    -- downward-api | host | build-arg | image. The value itself is not read;
    -- where it comes from is often the more useful half, because "this key is
    -- supplied by Secret db-creds" says what rotating that Secret restarts.
    from_source   TEXT    NOT NULL DEFAULT '',

    -- The Secret or ConfigMap supplying it, as `name` or `name:key`. A NAME.
    ref           TEXT    NOT NULL DEFAULT '',

    -- Set when the key comes out of a Secret, or when its NAME says credential.
    -- Classifying by name requires reading nothing.
    secret        INTEGER NOT NULL DEFAULT 0 CHECK (secret IN (0,1)),

    start_line    INTEGER NOT NULL DEFAULT 0
);

-- A whole Secret or ConfigMap injected at once: Kubernetes `envFrom`.
--
-- Deliberately NOT expanded into infra_keys rows. The keys live in the
-- referenced object, which may be in another file or in no file in this
-- repository, and inventing key names for it would be fabrication. An
-- unresolved reference is a real finding — a deployment that depends on
-- configuration this repository does not contain — so it is stored as what it
-- is rather than dropped or guessed at.
CREATE TABLE infra_env_from (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    index_run_id  INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    unit_id       INTEGER NOT NULL REFERENCES infra_units(id) ON DELETE CASCADE,
    kind          TEXT    NOT NULL DEFAULT '',   -- Secret | ConfigMap
    name          TEXT    NOT NULL DEFAULT '',
    prefix        TEXT    NOT NULL DEFAULT '',
    optional      INTEGER NOT NULL DEFAULT 0 CHECK (optional IN (0,1)),
    start_line    INTEGER NOT NULL DEFAULT 0
);

-- Declared resource bounds. Amounts ("512Mi", "250m") are quantities, not
-- configuration values: they are the units of a capacity question, they carry
-- no credential, and a blast-radius report that cannot say a workload is capped
-- at 512Mi cannot say why raising a cache size will kill it.
CREATE TABLE infra_limits (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    index_run_id  INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    unit_id       INTEGER NOT NULL REFERENCES infra_units(id) ON DELETE CASCADE,
    resource      TEXT    NOT NULL,
    request_qty   TEXT    NOT NULL DEFAULT '',
    limit_qty     TEXT    NOT NULL DEFAULT ''
);

-- A manifest we recognised and could not read.
--
-- This is the table that keeps silence honest. Without it, a repository whose
-- Kubernetes manifests all fail to parse is indistinguishable from one that has
-- none, and every consumer downstream — MCP, context, blast radius — reports
-- confidently on a deployment it never saw. It has no symbol_id: a file we
-- could not read declares nothing, so there is no node to point at.
CREATE TABLE infra_problems (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    index_run_id  INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    file_id       INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    format        TEXT    NOT NULL DEFAULT '',

    -- A stable label, not prose: too_large | malformed_yaml |
    -- templated_not_rendered | dockerfile_without_from | limit_reached |
    -- parser_panic, or a walker skip reason.
    reason        TEXT    NOT NULL,

    -- There is deliberately no `detail`. See the DEPLOYMENT FACTS header: a
    -- parser's message quotes the text it failed on, and that text can be a
    -- credential.

    start_line    INTEGER NOT NULL DEFAULT 0
);

-- ============================================================================
-- PER-RUN STATISTICS
-- ============================================================================

-- Per-run binding statistics, computed when the index is written. Every
-- relationship answer opens with a coverage statement ("only 59% of the
-- TypeScript references that name something this index defines are bound");
-- its figures depend on the run and nothing the caller asked, and computing
-- them reads every reference row and every edge.
--
-- run_language_stats holds one row per language of the run: what registry's
-- LanguageBindings returns, so the run-wide totals (ResolutionStats) are its
-- sums. run_stats says whether those rows still describe the run.
--
-- VALIDITY. The rows are a cache of the tables they summarise, and a stale
-- cache would state a coverage the index no longer has. So every write that
-- could change a figure clears run_stats.valid (the run_stats_* triggers), and
-- a reader trusts the rows only while valid = 1. RefreshRunStats rewrites the
-- rows and sets it again; `resolve`, `scip import` and the incremental update
-- call it when they finish. A run with no run_stats row is "not computed", and
-- readers fall back to the live query.
CREATE TABLE run_stats (
    index_run_id  INTEGER PRIMARY KEY REFERENCES index_runs(id) ON DELETE CASCADE,
    valid         INTEGER NOT NULL DEFAULT 0 CHECK (valid IN (0,1)),
    by_compiler   INTEGER NOT NULL DEFAULT 0,
    computed_unix INTEGER NOT NULL DEFAULT 0,
    -- How many definitions the dead-code scan would examine (-1: not
    -- computed). It lets a scan limited to one directory read only that
    -- directory's definitions and still report the number the whole scan would
    -- have examined.
    unreferenced  INTEGER NOT NULL DEFAULT -1
);

CREATE TABLE run_language_stats (
    index_run_id  INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    language      TEXT    NOT NULL,
    files         INTEGER NOT NULL DEFAULT 0,
    definitions   INTEGER NOT NULL DEFAULT 0,
    relationships INTEGER NOT NULL DEFAULT 0,
    hierarchy     INTEGER NOT NULL DEFAULT 0,
    total         INTEGER NOT NULL DEFAULT 0,
    resolved      INTEGER NOT NULL DEFAULT 0,
    ambiguous     INTEGER NOT NULL DEFAULT 0,
    unresolved    INTEGER NOT NULL DEFAULT 0,
    ignored       INTEGER NOT NULL DEFAULT 0,
    pending       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (index_run_id, language)
) WITHOUT ROWID;

-- ============================================================================
-- BULK-LOAD BOOKKEEPING
-- ============================================================================

-- Schema objects set aside while an index is first loaded.
--
-- A first index writes every symbol and reference row once, in file order,
-- into empty tables. Keeping the secondary indexes and the run_stats triggers
-- current row by row costs more than the rest of the sink put together;
-- building the same indexes from the finished table is one sort each. So
-- `graphlume index` drops those objects before the first load of an empty
-- index and creates them again before it publishes the run.
--
-- Each dropped object's CREATE statement is stored here in the same
-- transaction as the drop, so the two can never disagree: a row in this table
-- IS an object the schema is missing. The text is copied from sqlite_master,
-- so objects come back exactly as this schema defines them. An interrupted run
-- leaves its rows behind; the next `graphlume index` restores them before
-- doing anything else, and `status` and `doctor` report them until it has.
CREATE TABLE deferred_schema (
    name          TEXT PRIMARY KEY,
    kind          TEXT NOT NULL CHECK (kind IN ('index','trigger')),
    sql           TEXT NOT NULL,
    deferred_unix INTEGER NOT NULL DEFAULT 0
);

-- ============================================================================
-- FILE ROLLUPS
-- ============================================================================

-- The edge graph summed to files. A picture of how directories depend on each
-- other groups every edge by the file at each end, and on a monorepo of 27,000
-- files and 740,000 edges that grouping, done live, takes longer than a
-- read-only graph query is allowed. These rows are the grouping done once:
-- one per (source file, target file, kind, confidence) with the number of
-- edges it stands for, so the same picture is a read of a few thousand rows.
--
-- Edges between two definitions of the same file are not summed: a rollup
-- between files has nothing to say about them.
--
-- MAINTENANCE. The rows are a cache of `edges`, rewritten from it in full when
-- a run is published (CommitRun), when `resolve` and `scip import` finish,
-- and when more than a quarter of the files changed. A smaller change is
-- applied file by file: the file_rollup_* triggers record, in
-- file_rollup_dirty, every file of the committed run whose symbols or edges a
-- write touched, and the incremental update rewrites the rows of exactly
-- those files (registry.UpdateFileRollup). A replaced or deleted file takes
-- its rows with it (ON DELETE CASCADE on both file ids). `graphlume doctor
-- --verify-index` compares the rows with the live grouping.
CREATE TABLE file_edges (
    index_run_id INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    src_file_id  INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    dst_file_id  INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    kind         TEXT    NOT NULL,
    confidence   TEXT    NOT NULL CHECK (confidence IN ('exact','high','medium','low')),
    n            INTEGER NOT NULL CHECK (n > 0),
    CHECK (src_file_id <> dst_file_id),
    PRIMARY KEY (index_run_id, src_file_id, dst_file_id, kind, confidence)
) WITHOUT ROWID;

-- How many symbols each file defines, kept beside file_edges for the same
-- reason: sizing every directory of a large tree by its definitions is a join
-- of every file with every symbol. A file that defines nothing has no row.
CREATE TABLE file_symbols (
    file_id      INTEGER PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
    index_run_id INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    n            INTEGER NOT NULL CHECK (n > 0)
) WITHOUT ROWID;

-- Files of the committed run whose rollup rows a write has made stale, filled
-- by the file_rollup_* triggers and emptied by the next rollup update. A file
-- id here may name a file that has since been deleted; the update skips it.
-- Not a foreign key for that reason. File id 0, which no file has, means the
-- whole run will be summed again: `resolve` and `scip import` set it before
-- they rewrite most of the edges, and while it is set the triggers record
-- nothing, so those writes do not pay a row each for a list nobody will read.
CREATE TABLE file_rollup_dirty (
    index_run_id INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    file_id      INTEGER NOT NULL,
    PRIMARY KEY (index_run_id, file_id)
) WITHOUT ROWID;

-- ============================================================================
-- COMPILER AND TEST COVERAGE
-- ============================================================================

-- Which files a compiler indexer READ. An answer about who calls a definition
-- counts the unbound references that name it; a reference in a file a compiler
-- read, spelled like a name the compiler resolved to something else, is not a
-- candidate. The edge table cannot say a file was read: a file that only calls
-- code outside the repository carries no compiler edge, and neither does a file
-- the indexer skipped, and treating a skipped file as read is how an answer
-- comes to say "complete" about a file nobody looked at.
--
-- One row per file of the run that appeared as a document of an index imported
-- from a compiler indexer this tool ran itself. A re-indexed file gets a new
-- id, so a changed file drops its row (ON DELETE CASCADE) until the indexer has
-- read it again.
CREATE TABLE compiler_coverage (
    index_run_id INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    file_id      INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    tool         TEXT    NOT NULL,
    PRIMARY KEY (index_run_id, file_id)
) WITHOUT ROWID;

-- Test coverage: which lines of each definition a test run executed, so
-- analyze_impact can say whether any test ever ran a function and find_tests
-- can say a test it lists never executed it.
--
-- It is a PROFILE, not per-test data: Go coverprofile, LCOV, Cobertura and
-- JaCoCo record how often a line ran, summed over the run, never which test
-- ran it. So nothing here can rank tests; it says "executed" or "never
-- executed in that run". One imported profile per run; a new import replaces
-- the old one. Every table is run-scoped with ON DELETE CASCADE.

-- What was imported and when. content_hash names the profile bytes (the same
-- file imported twice is the same profile); git_revision is the commit HEAD
-- named at import, which is how an answer says WHICH code the numbers
-- describe.
CREATE TABLE coverage_imports (
    index_run_id     INTEGER PRIMARY KEY REFERENCES index_runs(id) ON DELETE CASCADE,
    content_hash     TEXT    NOT NULL,
    format           TEXT    NOT NULL,
    git_revision     TEXT    NOT NULL DEFAULT '',
    imported_unix    INTEGER NOT NULL,
    files_matched    INTEGER NOT NULL DEFAULT 0,
    files_unmatched  INTEGER NOT NULL DEFAULT 0
);

-- The content hash each matched file had when the profile was imported. Keyed
-- by PATH, not file id, on purpose: a re-indexed file gets a new id, so a row
-- keyed by id would vanish exactly when it is needed. Comparing this hash with
-- files.content_hash lets a reader say "stale" instead of showing numbers for
-- code that no longer exists.
CREATE TABLE coverage_files (
    index_run_id INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    path         TEXT    NOT NULL,
    content_hash TEXT    NOT NULL,
    PRIMARY KEY (index_run_id, path)
) WITHOUT ROWID;

-- Per definition: how many of its instrumented lines ran, how many it has,
-- and the highest hit count of any line. Keyed by symbol id with ON DELETE
-- CASCADE, so a changed or deleted file drops its definitions' rows by itself.
-- A definition with no instrumented lines gets no row: zero of zero is not a
-- measurement.
CREATE TABLE symbol_coverage (
    symbol_id     INTEGER PRIMARY KEY REFERENCES symbols(id) ON DELETE CASCADE,
    index_run_id  INTEGER NOT NULL REFERENCES index_runs(id) ON DELETE CASCADE,
    covered_lines INTEGER NOT NULL,
    total_lines   INTEGER NOT NULL,
    max_hits      INTEGER NOT NULL DEFAULT 0
);

-- ============================================================================
-- INDEXES
-- ============================================================================

-- Exactly one run may be the current committed one.
CREATE UNIQUE INDEX idx_index_runs_current
    ON index_runs (committed) WHERE committed = 1;

CREATE INDEX idx_files_hash ON files (content_hash);
CREATE INDEX idx_files_lang ON files (index_run_id, language);

CREATE INDEX idx_symbols_name ON symbols (index_run_id, name);
CREATE INDEX idx_symbols_fqn  ON symbols (index_run_id, fqn);
CREATE INDEX idx_symbols_file ON symbols (file_id);
-- Guided candidate lookup, "does THIS file define NAME": when an import, the
-- same file or the receiver's type says where a definition is, the question is
-- a point probe per file rather than a scan of every symbol of the name.
CREATE INDEX idx_symbols_file_name ON symbols (file_id, name);

CREATE INDEX idx_edges_src  ON edges (index_run_id, src_id, kind);
CREATE INDEX idx_edges_dst  ON edges (index_run_id, dst_id, kind);
CREATE INDEX idx_edges_conf ON edges (index_run_id, confidence);
-- Foreign-key indexes. idx_edges_src/dst lead with index_run_id, so they cannot
-- serve a lookup that knows only the symbol id, which is all a foreign-key
-- check has. Single-column on purpose, as are all *_fk indexes below.
CREATE INDEX idx_edges_src_fk ON edges (src_id);
CREATE INDEX idx_edges_dst_fk ON edges (dst_id);
-- Edge by location: `graphlume accuracy` adjudicates each call site against
-- the compiler's edge at the same file and line.
CREATE INDEX idx_edges_evidence
    ON edges (index_run_id, evidence_file, evidence_line);

CREATE INDEX idx_files_fts_map_file ON files_fts_map (file_id);

-- Rollup rows by the file they point at, which is also the foreign-key index
-- of dst_file_id; src_file_id gets its own, since the primary key leads with
-- the run.
CREATE INDEX idx_file_edges_dst    ON file_edges (dst_file_id, kind, confidence);
CREATE INDEX idx_file_edges_src_fk ON file_edges (src_file_id);
CREATE INDEX idx_file_symbols_run  ON file_symbols (index_run_id);

CREATE INDEX idx_refs_run_name ON symbol_references (index_run_id, name);
CREATE INDEX idx_refs_file     ON symbol_references (file_id);
CREATE INDEX idx_refs_status   ON symbol_references (index_run_id, resolution);
CREATE INDEX idx_refs_resolved_fk ON symbol_references (resolved_symbol_id);
-- Reference by location, for the same accuracy adjudication as
-- idx_edges_evidence; idx_refs_file alone narrows to a file and then scans it.
CREATE INDEX idx_refs_file_line
    ON symbol_references (file_id, start_line);
-- Covering indexes for the per-symbol reads of a relationship answer, so none
-- of them touches the table:
--   NameBinding  "how were the references spelled like this name resolved"
--                (a hub name has tens of thousands), and the dead-code probe
--                "is a reference spelled like this still unbound";
--   BoundSites   "which references are bound to this symbol, and from where".
CREATE INDEX idx_refs_name_binding
    ON symbol_references (index_run_id, name, resolution, resolved_symbol_id);
-- Partial: only a bound reference is ever asked about by the symbol it is bound
-- to.
CREATE INDEX idx_refs_bound_sites
    ON symbol_references (resolved_symbol_id, file_id, start_line, receiver, receiver_kind)
    WHERE resolution = 'resolved';
-- References by the receiver they are written on (`Repo.find()`, `mypkg::h()`).
-- An incremental update re-resolves every reference a changed definition can
-- reach, found by name and by receiver (internal/pipeline/reresolve.go).
-- Partial: a bare call has no receiver and is never asked about by one.
CREATE INDEX idx_refs_run_receiver ON symbol_references (index_run_id, receiver) WHERE receiver <> '';

CREATE INDEX idx_imports_run  ON file_imports (index_run_id);
CREATE INDEX idx_imports_path ON file_imports (index_run_id, path);

CREATE INDEX idx_access_run_table ON table_accesses (index_run_id, table_name);
CREATE INDEX idx_access_file      ON table_accesses (file_id);
CREATE INDEX idx_access_src       ON table_accesses (index_run_id, src_symbol_id);
CREATE INDEX idx_access_binding   ON table_accesses (index_run_id, binding);
CREATE INDEX idx_access_src_fk    ON table_accesses (src_symbol_id);
CREATE INDEX idx_access_bound_fk  ON table_accesses (bound_symbol_id);

CREATE INDEX idx_cfguse_run_key  ON config_usages (index_run_id, key_name);
CREATE INDEX idx_cfguse_file     ON config_usages (file_id);
CREATE INDEX idx_cfguse_binding  ON config_usages (index_run_id, binding);
CREATE INDEX idx_cfguse_src_fk   ON config_usages (src_symbol_id);
CREATE INDEX idx_cfguse_bound_fk ON config_usages (bound_symbol_id);

CREATE INDEX idx_calls_run_path ON client_calls (index_run_id, path);
CREATE INDEX idx_calls_file     ON client_calls (file_id);
CREATE INDEX idx_calls_binding  ON client_calls (index_run_id, binding);
CREATE INDEX idx_calls_src_fk   ON client_calls (src_symbol_id);
CREATE INDEX idx_calls_bound_fk ON client_calls (bound_symbol_id);

CREATE INDEX idx_infra_units_run     ON infra_units (index_run_id, name);
CREATE INDEX idx_infra_units_file    ON infra_units (file_id);
CREATE INDEX idx_infra_units_symbol  ON infra_units (symbol_id);
-- Partial: only units that name a build context take part in the join that
-- answers "which service does this code run in", a small minority of rows.
CREATE INDEX idx_infra_units_context ON infra_units (index_run_id, build_context)
    WHERE build_context <> '';

CREATE INDEX idx_infra_ports_unit ON infra_ports (unit_id);
CREATE INDEX idx_infra_ports_run  ON infra_ports (index_run_id, container_port);

CREATE INDEX idx_infra_keys_run    ON infra_keys (index_run_id, name);
CREATE INDEX idx_infra_keys_unit   ON infra_keys (unit_id);
CREATE INDEX idx_infra_keys_file   ON infra_keys (file_id);
CREATE INDEX idx_infra_keys_symbol ON infra_keys (symbol_id);

CREATE INDEX idx_infra_env_from_unit ON infra_env_from (unit_id);
CREATE INDEX idx_infra_env_from_run  ON infra_env_from (index_run_id, name);

CREATE INDEX idx_infra_limits_unit ON infra_limits (unit_id);

CREATE INDEX idx_infra_problems_file ON infra_problems (file_id);
CREATE INDEX idx_infra_problems_run  ON infra_problems (index_run_id, reason);

CREATE INDEX idx_symbol_coverage_run ON symbol_coverage (index_run_id);

-- ============================================================================
-- TRIGGERS
-- ============================================================================

-- Invalidate run_stats on every write that could change a figure. Each is one
-- primary-key probe that matches no row once the flag is already clear, so a
-- bulk write pays for it once per row and not once per figure.
CREATE TRIGGER run_stats_files_ins AFTER INSERT ON files
BEGIN UPDATE run_stats SET valid = 0 WHERE index_run_id = NEW.index_run_id AND valid = 1; END;
CREATE TRIGGER run_stats_files_del AFTER DELETE ON files
BEGIN UPDATE run_stats SET valid = 0 WHERE index_run_id = OLD.index_run_id AND valid = 1; END;
CREATE TRIGGER run_stats_files_upd AFTER UPDATE OF language ON files
BEGIN UPDATE run_stats SET valid = 0 WHERE index_run_id = NEW.index_run_id AND valid = 1; END;

CREATE TRIGGER run_stats_symbols_ins AFTER INSERT ON symbols
BEGIN UPDATE run_stats SET valid = 0 WHERE index_run_id = NEW.index_run_id AND valid = 1; END;
CREATE TRIGGER run_stats_symbols_del AFTER DELETE ON symbols
BEGIN UPDATE run_stats SET valid = 0 WHERE index_run_id = OLD.index_run_id AND valid = 1; END;

CREATE TRIGGER run_stats_refs_ins AFTER INSERT ON symbol_references
BEGIN UPDATE run_stats SET valid = 0 WHERE index_run_id = NEW.index_run_id AND valid = 1; END;
CREATE TRIGGER run_stats_refs_del AFTER DELETE ON symbol_references
BEGIN UPDATE run_stats SET valid = 0 WHERE index_run_id = OLD.index_run_id AND valid = 1; END;
CREATE TRIGGER run_stats_refs_upd AFTER UPDATE OF resolution, resolution_source ON symbol_references
BEGIN UPDATE run_stats SET valid = 0 WHERE index_run_id = NEW.index_run_id AND valid = 1; END;

CREATE TRIGGER run_stats_edges_ins AFTER INSERT ON edges
BEGIN UPDATE run_stats SET valid = 0 WHERE index_run_id = NEW.index_run_id AND valid = 1; END;
CREATE TRIGGER run_stats_edges_del AFTER DELETE ON edges
BEGIN UPDATE run_stats SET valid = 0 WHERE index_run_id = OLD.index_run_id AND valid = 1; END;

-- Record the files whose rollup rows (file_edges, file_symbols) a write to the
-- committed run made stale. A staging run is summed whole when it is
-- published, so for its rows each trigger is one primary-key probe that finds
-- the run uncommitted. A symbol deleted by a cascade is already gone when its
-- edges' triggers run, so the lookup finds only the end that survives; the
-- deleted file's rows go with it.
CREATE TRIGGER file_rollup_symbols_ins AFTER INSERT ON symbols
WHEN EXISTS (SELECT 1 FROM index_runs WHERE id = NEW.index_run_id AND committed = 1)
 AND NOT EXISTS (SELECT 1 FROM file_rollup_dirty WHERE index_run_id = NEW.index_run_id AND file_id = 0)
BEGIN INSERT OR IGNORE INTO file_rollup_dirty (index_run_id, file_id) VALUES (NEW.index_run_id, NEW.file_id); END;
CREATE TRIGGER file_rollup_symbols_del AFTER DELETE ON symbols
WHEN EXISTS (SELECT 1 FROM index_runs WHERE id = OLD.index_run_id AND committed = 1)
 AND NOT EXISTS (SELECT 1 FROM file_rollup_dirty WHERE index_run_id = OLD.index_run_id AND file_id = 0)
BEGIN INSERT OR IGNORE INTO file_rollup_dirty (index_run_id, file_id) VALUES (OLD.index_run_id, OLD.file_id); END;

CREATE TRIGGER file_rollup_edges_ins AFTER INSERT ON edges
WHEN EXISTS (SELECT 1 FROM index_runs WHERE id = NEW.index_run_id AND committed = 1)
 AND NOT EXISTS (SELECT 1 FROM file_rollup_dirty WHERE index_run_id = NEW.index_run_id AND file_id = 0)
BEGIN INSERT OR IGNORE INTO file_rollup_dirty (index_run_id, file_id)
      SELECT NEW.index_run_id, file_id FROM symbols WHERE id IN (NEW.src_id, NEW.dst_id); END;
CREATE TRIGGER file_rollup_edges_del AFTER DELETE ON edges
WHEN EXISTS (SELECT 1 FROM index_runs WHERE id = OLD.index_run_id AND committed = 1)
 AND NOT EXISTS (SELECT 1 FROM file_rollup_dirty WHERE index_run_id = OLD.index_run_id AND file_id = 0)
BEGIN INSERT OR IGNORE INTO file_rollup_dirty (index_run_id, file_id)
      SELECT OLD.index_run_id, file_id FROM symbols WHERE id IN (OLD.src_id, OLD.dst_id); END;
CREATE TRIGGER file_rollup_edges_upd AFTER UPDATE OF src_id, dst_id, kind, confidence ON edges
WHEN EXISTS (SELECT 1 FROM index_runs WHERE id = NEW.index_run_id AND committed = 1)
 AND NOT EXISTS (SELECT 1 FROM file_rollup_dirty WHERE index_run_id = NEW.index_run_id AND file_id = 0)
BEGIN INSERT OR IGNORE INTO file_rollup_dirty (index_run_id, file_id)
      SELECT NEW.index_run_id, file_id FROM symbols WHERE id IN (OLD.src_id, OLD.dst_id, NEW.src_id, NEW.dst_id); END;
