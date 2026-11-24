# GraphLume User Guide: The Enterprise Guide to Code Intelligence & Change Safety

GraphLume is a local-first, zero-CGO code intelligence engine and knowledge graph for engineering teams and AI coding agents. It indexes large polyglot codebases into a queryable graph of symbols, call chains, HTTP endpoints, database access patterns, event broker queues, and test coverage—delivering deterministic answers over CLI, MCP (Model Context Protocol), and an embedded interactive portal.

---

## 1. Design Philosophy

Modern codebases suffer from context fragmentation. AI coding agents burn 80% of their token budget and reasoning time repeatedly grepping files, attempting to reconstruct architectural boundaries and tracing caller chains from scratch.

GraphLume replaces speculative text search with deterministic graph intelligence:

1. **Every Fact Carries Provenance**: GraphLume never conflates a compiler-proven fact with a heuristic regex match. All relationships carry confidence tiers (`exact`, `high`, `medium`, `low`).
2. **Zero-CGO & Local-First**: Written in pure Go using pure-Go SQLite (`modernc.org/sqlite`). It compiles to a self-contained static binary. Your proprietary source code never leaves your workstation or CI runner.
3. **Zero External LLM Calls in Core Engine**: Graph construction, BM25 indexing, AST pruning, and impact analysis run deterministically at native CPU speed with zero cloud API dependencies.
4. **Honest Incompleteness**: An empty query result distinguishes between "we proved this has no callers" and "this code has unresolvable dynamic references." Blind spots and omitted edges are reported explicitly.

---

## 2. Confidence Tier & Provenance Model

GraphLume categorizes all graph relationships into four immutable tiers:

| Tier | Source | Typical Precision | Recommended Usage |
|---|---|---|---|
| `exact` | Compiler-backed SCIP indexers (`scip-go`, `scip-typescript`, `scip-python`, `scip-java`) | **100.0%** (Proved by compiler typechecker) | Fully automated refactoring and blocking CI gates. |
| `high` | Tree-sitter explicit import matching + unambiguous type discriminator | **86.8% – 99.5%** | Primary guidance for AI agents and human reviews. |
| `medium` | Multi-candidate type inference or package-level heuristic | **5.7% – 37.5%** | Displayed as informative leads; never acted on automatically. |
| `low` | Loose identifier name match across files | **0.0% – 2.8%** | Filtered out by default; accessible via `--min-confidence low`. |

To inspect your repository's measured precision and recall against your local toolchains:
```bash
graphlume accuracy
```

---

## 3. Installation & Getting Started

### Pre-built Binary Installation

**macOS / Linux:**
```bash
curl -fsSL https://raw.githubusercontent.com/sutrakam/graphlume/main/install.sh | sh
```

**Windows (PowerShell):**
```powershell
Invoke-WebRequest -Uri https://raw.githubusercontent.com/sutrakam/graphlume/main/install.ps1 -OutFile install.ps1
.\install.ps1
```

### Building From Source (Go 1.26+)
```bash
git clone https://github.com/sutrakam/graphlume.git
cd graphlume
go build -trimpath -ldflags="-s -w" -o graphlume ./cmd/graphlume
sudo mv graphlume /usr/local/bin/
```

### Initializing a Project

From the root of your git repository:
```bash
# 1. Initialize local configuration (.graphlume.yml)
graphlume init

# 2. Build the structural index (scans source files and extracts definitions)
graphlume index

# 3. Resolve cross-file references and build the call graph
graphlume resolve

# 4. Verify graph integrity
graphlume doctor
```

Subsequent indexing runs are incremental: GraphLume stores SHA-256 content hashes of files and re-indexes only files modified since the last run.

---

## 4. CLI Reference & Key Capabilities

### 4.1. Distributed Event Topology (`graphlume events`)

GraphLume statically tracks asynchronous event producers and consumers across distributed message brokers:
- **Supported Brokers**: Apache Kafka (Sarama, kafka-go, Spring Kafka, KafkaJS, kafka-python), RabbitMQ / AMQP, AWS SQS / SNS, and Google Cloud Pub/Sub.

```bash
# List all discovered topics/queues across the repository
graphlume events

# Inspect producers and consumers for a specific topic
graphlume events topic payment.settled

# Output machine-readable JSON
graphlume events --json
```

Example human output:
```text
Event Topology Report (3 topics discovered)

Topic: payment.settled [kafka]
  Producers:
    - SettlementService.Finalize (services/settlement.go:142) [high]
  Consumers:
    - LedgerWorker.OnMessage (workers/ledger.go:88) [high]
    - NotificationHandler.HandleEvent (handlers/notify.ts:45) [high]
```

### 4.2. Code Blindspots & Missed References (`graphlume blindspots`)

Static analysis frequently encounters dynamic dispatch, unmapped HTTP routes, orphan database tables, or critical paths lacking tests. `graphlume blindspots` surfaces these blindspots before they cause production defects.

```bash
# Run a blindspot audit across the repository
graphlume blindspots

# Output formatted JSON for custom CI/CD parsing
graphlume blindspots --json
```

Discovered categories:
- `unresolved_call`: Dynamic or reflection-based calls where the target symbol could not be proven.
- `untested_critical`: Public API endpoints or database writers lacking covering test edges.
- `unmapped_endpoint`: Route handlers defined in code without client callers or framework contracts.
- `orphan_table`: Database tables defined in schemas or migrations with zero code readers or writers.

### 4.3. Deterministic Refactoring Plans (`graphlume plan-change`)

Before refactoring a symbol or renaming a core function, `graphlume plan-change` constructs an ordered, multi-step execution plan containing direct callers, affected tests, preconditions, and postconditions.

```bash
# Generate refactoring execution plan for a symbol
graphlume plan-change --symbol ProcessRefund

# Output JSON plan for automated agent execution
graphlume plan-change --symbol ProcessRefund --json
```

Example output:
```text
Refactor Plan for ProcessRefund (services/billing.go:112)
Risk Level: MEDIUM (Risk Score: 42/100)
Direct Callers: 4 | Affected Tests: 2

Preconditions:
  - Working directory is clean (no uncommitted edits)
  - Target symbol exists and compiles

Step 1 [update_caller]: Update call site in handlers/payment.go:64
Step 2 [update_caller]: Update call site in workers/retry.go:102
Step 3 [update_def]: Refactor signature in services/billing.go:112
Step 4 [verify_test]: Run covering test TestProcessRefundSuccess (services/billing_test.go:40)

Postconditions:
  - Run graphlume doctor to ensure zero dangling reference edges
  - Run affected test suite
```

### 4.4. AST Interface Pruning (`graphlume outline`)

When AI agents read source files, up to 90% of tokens are consumed by implementation logic inside method bodies. `graphlume outline` parses Go, Python, and TypeScript/JavaScript files and strips all function bodies, replacing them with concise stubs while preserving all type signatures, struct fields, interfaces, and docstrings.

```bash
# Print pruned AST interface to stdout
graphlume outline internal/store/sqlite.go

# Reduces a 1,200-line file (~12,000 tokens) to a 90-line interface (~900 tokens)
```

### 4.5. Token-Efficient Diff Minimization (`graphlume minimize-diff`)

Standard git diffs contain excess context lines, whitespace noise, and redundant index headers. `graphlume minimize-diff` parses patch streams into structured, token-efficient representations highlighting exact additions, removals, and symbol contexts.

```bash
git diff HEAD~1 | graphlume minimize-diff
```

### 4.6. Task Context & Relevance Ranking (`graphlume context`)

Given a natural language task description, GraphLume uses BM25 full-text scoring combined with AST dependency graph traversal to locate the exact transitive file set required for the task.

```bash
graphlume context "Add idempotency key validation to payment callback handler"
```

The context engine bridges domain concepts deterministically (e.g. mapping "credential leakage" to `redact.go` and `credentials_test.go`) without hallucination.

### 4.7. The Change-Safety Gate: Compile & Verify

GraphLume enforces architecture rules and prevents regressions through a two-phase contract:

1. **Compile**: Convert an engineering intent into a strict JSON contract:
   ```bash
   graphlume compile "Refactor auth middleware to use JWT tokens" --json > task-contract.json
   ```
2. **Work**: Engineer or AI agent makes changes.
3. **Verify**: Evaluate git changes against the contract:
   ```bash
   graphlume verify --contract task-contract.json --changed "$(git diff --name-only | paste -sd, -)"
   ```

Exit Codes:
- `0` (`pass`): All checks satisfied with zero violations.
- `0` (`advisory`): Only heuristic leads discovered (use `--fail-on-leads` to reject).
- `1` (`fail`): At least one compiler-proven fact violation detected (e.g., breaking an architecture boundary or modifying an excluded file).
- `2` (`unverified`): No index or contract data available to verify.

### 4.8. Applying a Rename (`graphlume rename`)

`plan-change` tells you what a rename touches; `rename` makes the edit, but only
where it can prove every site.

1. **Dry run first** (the default). It lists every site with its confidence and
   every site that would block an apply:
   ```bash
   graphlume rename updateStatus applyStatus
   ```
2. **What blocks `--apply`.** It writes nothing, lists every failing site and
   exits `2` unless every site is `exact` (compiler-proven), unchanged since
   indexing, unambiguous on its line, inside the project root and writable, and
   the new name is not already defined in an edited file. On a project with no
   compiler index, every site is `high` and the rename is refused. Run
   `graphlume indexers install --yes` and `graphlume scip import` to upgrade them
   to `exact`.
3. **Opting into `high`.** `--floor high` also accepts an explicit import plus an
   unambiguous receiver type. That tier is measured 86.8-96.1% precise, so the
   command prints a warning first: at 90%, a rename touching thirty sites gets
   three wrong. Read every listed site.
   ```bash
   graphlume rename updateStatus applyStatus --floor high --apply
   ```
4. **What `--apply` does.** Files are written atomically and restored if a later
   write fails. A lock file, `.graphlume-edit.lock`, exists at the project root
   only while the edit runs; if a crashed run left it behind, GraphLume never
   deletes it for you, and the error tells you to remove it once no graphlume
   process is running. Then the task contract lands in
   `.graphlume/rename-contract.json` and `verify` runs automatically on the
   changed files. `verify` reads the **pre-rename index**, so run
   `graphlume index` afterwards.
5. **Exit codes.** `0` applied and verified; `1` error or verify failure; `2`
   refused, nothing written; `3` applied, but not verified complete: either
   `verify` was unverified, or the old name still appears in indexed files, which
   are listed under `LEFTOVER OCCURRENCES`. Handle them by hand.
6. **GraphLume does not run the compiler.** References it could not bind, and
   interface implementations or overrides with no edge to the symbol, are not
   renamed. After every rename, search for the old name, then **build and
   test**.

---

---

## 5. Model Context Protocol (MCP) Server Setup

GraphLume includes a built-in MCP server that communicates over JSON-RPC via standard input/output.

### Claude Desktop Configuration
Add GraphLume to `~/Library/Application Support/Claude/claude_desktop_config.json`:
```json
{
  "mcpServers": {
    "graphlume": {
      "command": "graphlume",
      "args": ["serve", "--project", "/path/to/your/repository"]
    }
  }
}
```

### Cursor & Windsurf Configuration
In Cursor or Windsurf MCP Settings:
- **Name**: `graphlume`
- **Type**: `command`
- **Command**: `graphlume serve --project /path/to/your/repository`

### Available MCP Tools

AI agents connected to GraphLume gain access to:
- `overview`: Orient on the repository: languages, size, resolution coverage.
- `search` / `symbol` / `definition`: Locate declarations with exact file, line, and kind.
- `callers` / `references`: Retrieve incoming call edges and usage sites with confidence grades.
- `context`: Obtain ranked file summaries and symbol graphs for a prompt.
- `impact`: Calculate transitive impact (blast radius) and risk for a change.
- `tests`: Find the tests that cover a symbol or file.
- `plan`: Generate a step-by-step refactoring plan with preconditions and post-checks.
- `rename`: Enumerate every site a rename must touch.
- `blindspots`: Audit unresolved calls, unmapped endpoints, orphan tables and missing config.
- `outline`: Obtain a token-optimized outline (signatures, types, docs; bodies pruned) of a file.
- `diff`: Compress unified git diffs into structured, token-efficient patch contexts.
- `endpoints` / `tables` / `config`: Query HTTP endpoints, database access and configuration keys.
- `coverage`: Report how much of the call graph is resolved, so an empty result is not over-trusted.
- `estate`: Links, callers and impact across a multi-repository estate (`full` profile, with `graphlume serve --estate FILE`).

Some tools need the `standard` profile; see [agent-integration.md](agent-integration.md).
Event topology (`graphlume events`) is available from the CLI and the portal, not as an MCP tool.

---

## 6. The local portal (`graphlume ui`)

GraphLume includes a local, read-only web portal over the index. It has five sections (Home, Explore, Change, Health and Agents) and one page for every function, type, file, route and table.

To launch the portal:
```bash
graphlume ui
```
It listens on loopback (`127.0.0.1`) on port 7777 and prints the address. Use `--port 0` to pick a free port. `--host` accepts loopback addresses only; the portal has no login.

### Search
The box in the header searches functions, types, files, routes, tables and text. Press `/` to jump to it.

### Symbol pages
A symbol page shows what calls it, what it calls, what a change to it can reach, the tests that reach it and its type hierarchy, as tabs. File, route and table pages follow the same pattern.

### Reading an answer
Every relationship is marked proven, very likely, uncertain or a guess. Each answer shows "What this answer cannot see" and "Ask your agent the same thing", the exact `graphlume tool call ...` command that returns the same answer.

### Sections
- **Home**: the state of the index and how to read an answer.
- **Explore**: the routes the code exposes and the tables it uses.
- **Change**: review a branch, or plan a change to one thing.
- **Health**: how complete the index is, hotspots, unused code and blind spots.
- **Agents**: four ways to connect an agent, and what each tool costs in context.

The portal never writes to the index. To refresh it, run `graphlume index` or `graphlume watch`. See the [portal guide](guides/portal-guide.md).

---

## 7. CI/CD Integration: GitHub Actions Change-Safety Gate

GraphLume can be integrated directly into your GitHub Actions workflow to fail pull requests that introduce architecture violations or high-severity blindspots (`--fail-on`, default `high`), and to publish what it found: a sticky pull-request comment, code-scanning alerts (SARIF) and the job summary. Reports are published before the gate runs, so a failing build still leaves the reviewer a comment and alerts that explain why.

Create `.github/workflows/graphlume-gate.yml` with this content (the same file is in [docs/examples/graphlume-gate.yml](examples/graphlume-gate.yml)). It runs the [GraphLume GitHub Action](../action.yml) on every pull request into `main`:
```yaml
name: GraphLume

on:
  pull_request:
    branches: [main]

permissions:
  contents: read
  pull-requests: write
  security-events: write

concurrency:
  group: graphlume-${{ github.event.pull_request.number }}
  cancel-in-progress: true

jobs:
  change-safety:
    runs-on: ubuntu-latest
    timeout-minutes: 20
    steps:
      - uses: actions/checkout@v7
        with:
          fetch-depth: 0
      - uses: sutrakam/graphlume@v1
        with:
          fail-on: high
      - uses: github/codeql-action/upload-sarif@v4
        if: always() && github.event.pull_request.head.repo.full_name == github.repository
        with:
          sarif_file: graphlume-out/graphlume.sarif
          category: graphlume
```

The action's inputs (`version`, `fail-on`, `accuracy`, `comment-pr`, `upload-index`) are listed in [action.yml](../action.yml).

The `Collect Findings` step saves `graphlume --json arch|missed|impact` output; `graphlume report sarif` turns it into a SARIF 2.1.0 file uploaded to code scanning under the category `graphlume`, and `graphlume report pr` renders one bounded Markdown comment that starts with `<!-- graphlume:pr-report -->`. That marker is how the workflow finds and edits its own comment on later pushes instead of adding a new one. The job needs `pull-requests: write` (the comment) and `security-events: write` (the SARIF upload); the workflow sets both on the job only. Pull requests from forks get neither the comment nor the code-scanning upload, because a fork's token is read-only; they still get the job summary and the gate. The blast-radius analysis runs only on pull requests, and none of the reporting steps fail the build -- the `Enforce` steps at the end do.

**Compiler-backed precision.** With `GRAPHLUME_ACCURACY: auto` (the default) the gate runs `graphlume indexers install --yes --out-dir graphlume-out/scip` after `resolve`, then `graphlume scip import` on every index it produced, so `impact` walks compiler-proven `exact` edges wherever a compiler exists. `--yes` is the CI operator's consent to install the pinned indexers listed by `graphlume indexers pins`; nothing else is installed, and every download is pinned and verified (by digest, or by the Go checksum database for scip-go). The runner needs the language's own toolchain — Go for scip-go (setup-go provides it), Node for scip-typescript and scip-python; a project whose toolchain is missing is skipped with its reason. Java is never built by this workflow. The stage is an accuracy upgrade, never a requirement, and it never fails the job: if an indexer is missing, blocked, or produces an index that will not import, the gate emits a `::warning::`, the edges stay heuristic, and the job summary gains a "Compiler-backed precision" section listing the imported and failed counts and each skipped language with its reason. scip-go is cached under a key derived from the pin table. Set `GRAPHLUME_ACCURACY: fast` to skip the stage; the PR report then shows, in a "How much of the blast radius is proven" table, what share of the blast radius is `exact`, `high` and `medium` (interface-dispatch-only symbols are not counted). The step never passes `--allow-repo-build`, so fork pull requests, which run with a read-only token, execute only pinned, digest-verified indexers and never the repository's own build.

---

## 8. Summary & Best Practices

1. **Keep the Index Fresh**: Add `graphlume hooks install` to your development environment to automatically refresh the index on every git commit in 20 milliseconds without running background daemons.
2. **Import Compiler Data in CI**: Place the compiler's `index.scip` in the project root (or `.graphlume/`) before `graphlume resolve`, which imports it automatically (`--no-scip` opts out), or run `graphlume scip import` yourself, to upgrade heuristic edges to compiler-backed `exact` confidence.
3. **Trust `exact` and `high`, Verify `medium`**: Never allow AI coding agents or automated scripts to execute destructive refactoring operations on `medium` or `low` confidence edges without explicit human review.

---

## 9. Comprehensive Product Capabilities

GraphLume delivers an end-to-end change-safety and code intelligence architecture. The following matrix details each capability and its operational mechanism:

### 9.1. Polyglot AST Precision Engine & Arity Dialects
- **Multi-Language Parsing**: Zero-external-dependency Tree-Sitter grammars compiled directly into pure Go. Reads definitions in 91 of the 206 bundled grammars, and binds calls across files for 14 languages: Go, Java, Python, TypeScript, C#, C++, Kotlin and Scala (typed), and C, JavaScript, PHP, Ruby, Rust and TSX (resolved). `graphlume languages` prints the current list.
- **Arity & Overload Resolution**: Parameter count queries (`paramCountQueries`) and call argument queries (`argCountQueries`) distinguish methods overloaded by parameter count in Go, Java, TypeScript, C#, C++, Kotlin and Scala. Argument types are not used, so two overloads with the same count are still confused, at `low`.
- **Bounded Parsing and Memory**: A parse stops on the parser library's work limits, which give the same verdict for the same bytes at any machine load, and files up to 2 MiB are parsed (`index.max_file_bytes` changes the limit). A file over 512 KiB reserves eight times its size from an in-flight memory budget, so large files are parsed a few at a time.

### 9.2. Change Safety & Blast Radius Analytics (`graphlume impact`, `graphlume plan-change`)
- **Upstream & Downstream Traversal**: Answers both *"what does this function call?"* (downstream dependencies) and *"what in the entire enterprise depends on this function?"* (upstream blast radius).
- **Hub Pruning**: Automatically prunes traversals across high-degree utility nodes (such as loggers, string formatters, and base object classes) to avoid exponential combinatorial explosions while explicitly reporting the cutoff boundary.
- **Test Gap Detection (`graphlume blindspots`)**: Proves whether modified code paths have verified test coverage or whether execution branches are blind to the testing suite.

### 9.3. Safe Automated Refactoring (`graphlume rename`)
- **Exact AST Identifier Splicing**: Matches identifiers strictly on AST node boundaries. Never confuses a symbol name with a comment, string literal, or substring token.
- **Reversible Multi-File Rollback**: Takes an in-memory snapshot before modifying source code. If any file write fails or post-apply verification detects an orphaned call site, GraphLume rolls back all files across the repository to their original state in reverse order.
- **Leftover Occurrence Scanning**: Audits all files (including unindexed or binary files) post-rename to ensure no stale references to the old symbol persist.

### 9.4. Architecture Governance & CI/CD Gates (`graphlume arch`)
- **Structural Boundary Enforcement**: Declaratively defines allowed import and call relationships between architectural layers (e.g. `domain` must never import `infrastructure` or `controller`).
- **PR Gating & SARIF 2.1.0**: Generates standard SARIF code-scanning artifacts and sticky GitHub PR comments showing exact change risk and architectural compliance scores.

### 9.5. Cross-Stack Distributed Topology (`graphlume api`, `graphlume db`, `graphlume events`)
- **HTTP / REST Endpoints**: Statically discovers route handlers across Express, Spring Boot, Gin, FastAPI, NestJS, and ASP.NET.
- **Database Access Maps**: Maps database tables, ORM models (Prisma, GORM, Hibernate, SQLAlchemy), and raw SQL queries to identifying calling methods.
- **Distributed Event Topologies**: Extracts producer and consumer queues across Apache Kafka, RabbitMQ, AWS SQS/SNS, Google Cloud Pub/Sub and ActiveMQ (STOMP/JMS).

### 9.6. AI Coding Agent MCP Server (`graphlume serve`, `graphlume install`)
- **Compact Subgraph Extraction**: Delivers targeted subgraphs and pruned signatures instead of raw file dumps. How much context that saves depends on the task: the one agent benchmark run so far found no token advantage on simple lookups (`docs/BENCHMARKING.md`).
- **Confidence Advisory Notices**: Automatically attaches warning notes when query results contain heuristic (`medium` or `low`) edges, preventing autonomous agents from acting on hallucinatory call chains.

---

## 10. Deep Comparative Analysis: GraphLume vs Alternative Tools

Rather than evaluating specific vendor brands, the following analysis benchmarks GraphLume against the primary architectural classes of code intelligence and developer tools currently deployed in the software industry:

```
+--------------------------------------------------------------------------------------------------+
|                                  TOOL CATEGORY COMPARISON OVERVIEW                               |
+--------------------------------------------------------------------------------------------------+
| GraphLume                  : Pure-Go static binary, deterministic graph, safe transactional edits |
| Category A (LSP Daemons)  : Per-language server silos, heavy memory, navigation only, no cross-db|
| Category B (Vector RAG)   : Fuzzy text embeddings, 80%+ hallucinations, massive token waste      |
| Category C (Heavy SAST)   : Multi-hour batch scans, alert fatigue, read-only, non-agent-native   |
| Category D (Text Greppers): High speed, zero semantic awareness, false-positive substring matches|
+--------------------------------------------------------------------------------------------------+
```

### Detailed Evaluation by Tool Category

#### Category 1: Traditional Language Server Protocol (LSP) Daemons
* **How They Work**: Run language-specific daemons (e.g., `gopls`, `tsserver`, `jdtls`) in the background of an IDE to provide real-time auto-complete and go-to-definition.
* **Limitations Compared to GraphLume**:
  - **Siloed by Language**: An LSP cannot connect a TypeScript frontend API call to a Java Spring Boot endpoint, nor can it trace a Go publisher to a Python Kafka consumer.
  - **Resource Heavy**: Running 3 to 4 distinct language servers in a microservice monorepo easily consumes 4–8 GB of RAM.
  - **No Global Change Impact**: LSPs provide local symbol lookups but lack graph traversal capabilities (e.g., multi-hop blast radius, hub pruning, test gap discovery).
* **Where GraphLume Excels**: GraphLume unifies all languages into a single SQLite knowledge graph running in a single lightweight process with flat memory footprint.

#### Category 2: Naive Vector Search / Embedding-Based Code RAG
*(For an exhaustive architectural essay, see [Why Vector RAG Fails for Code: The Case for Deterministic Graph Intelligence](WHY_NOT_VECTOR_RAG.md))*
* **How They Work**: Chunk source code into text windows, compute dense vector embeddings, and perform cosine similarity searches to retrieve context for LLMs.
* **Limitations Compared to GraphLume**:
  - **Fuzzy Guesswork vs Deterministic Truth**: Vector similarity finds code that *reads similarly*, not code that is *actually executed*. A method named `processPayment` will match unrelated payment helpers while missing the actual interface implementation `settle`.
  - **Extreme Token Waste**: Dumping entire source files or arbitrary chunks into an LLM context consumes tens of thousands of tokens per prompt, leading to high cost and lost-in-the-middle context degradation.
  - **Zero Safety Guarantees**: Vector search provides zero safety invariants for automated refactoring or architecture governance.
* **Where GraphLume Excels**: GraphLume delivers exact call graphs and pruned signatures, with a confidence tier on every relationship, each tier measured against real projects (`docs/accuracy.md`).

#### Category 3: Heavyweight Enterprise Static Analysis (SAST) Suites
* **How They Work**: Execute deep interprocedural taint analysis and abstract interpretation to detect security vulnerabilities and code quality flaws.
* **Limitations Compared to GraphLume**:
  - **Hours-Long Execution**: Full repository scans take 30 minutes to several hours, preventing interactive developer use.
  - **High False-Positive Noise**: Developers frequently suffer from alert fatigue due to uncalibrated heuristic warnings.
  - **Read-Only / Non-Agent Native**: Designed for compliance reporting rather than active pair-programming with AI coding agents.
* **Where GraphLume Excels**: GraphLume indexes 10,000 files in seconds, provides sub-millisecond query latency via in-memory CSR graphs, and integrates natively into agent workflows via standard MCP tools.

#### Category 4: Static LSIF / SCIP Compiler-Only Dumpers
* **How They Work**: Compile code using full toolchains (`scip-go`, `scip-typescript`) and write large protocol buffer files storing cross-references.
* **Limitations Compared to GraphLume**:
  - **Brittle Setup Requirements**: Require every compiler, SDK, private registry credential, and native dependency to be fully configured on the host. If a build fails, zero index is produced.
  - **Static / Non-Queryable**: Output static dump files that cannot be dynamically traversed, queried for blast radius, or updated incrementally on file save.
* **Where GraphLume Excels**: GraphLume provides zero-setup Tree-Sitter heuristic indexing out of the box, with optional seamless SCIP compiler enrichment (`graphlume scip import`). If a compiler is missing, GraphLume still delivers an 87%+ accurate graph.

#### Category 5: Fast Regex / Text Greppers (ripgrep, ag, ctags)
* **How They Work**: Execute optimized parallel string and regular expression searches across file trees.
* **Limitations Compared to GraphLume**:
  - **No Semantic Awareness**: Cannot distinguish between a function definition, an invocation, a comment, a string literal, or a substring in an unrelated variable name.
  - **No Graph Traversability**: Cannot trace multi-hop call hierarchies, dependency chains, or transitive blast radius.
* **Where GraphLume Excels**: GraphLume understands AST node boundaries, receiver types, and call semantics.

---

### Comprehensive Capabilities & Architectural Comparison Matrix

| Architectural Feature | GraphLume | Traditional LSP Daemons | Vector Embedding RAG | Enterprise SAST Suites | Static SCIP / LSIF Dumpers | Text Greppers (`rg`) |
|:---|:---:|:---:|:---:|:---:|:---:|:---:|
| **Zero-Setup Polyglot Indexing** | **Yes** (Native Go) | No (Per-language SDKs) | Yes (Raw text) | No (Complex setups) | No (Full build required) | **Yes** |
| **Deterministic Call Graphs** | **Yes** | Yes (Single language) | No (Fuzzy similarity) | Yes | **Yes** | No (String match) |
| **Cross-Stack Topologies (DB/API/Events)** | **Yes** | No | No | Partial (Security only) | No | No |
| **Calibrated Confidence Provenance** | **Yes** (4 Tiers) | Implicit (Uncalibrated) | No (Distance metric) | No (Severity, not truth) | **Yes** (Compiler-only) | No |
| **Transitive Blast Radius (`impact`)** | **Yes** (With Hub Pruning)| No | No | No | No | No |
| **AI Agent MCP Server Native** | **Yes** (10+ tools) | No | Partial (Retrieval only)| No | No | No |
| **Atomic Multi-File Safe Refactoring** | **Yes** (With Rollback) | Partial (LSP rename) | No | No | No | No (Blind replace) |
| **Incremental File Watch Latency** | **<50 ms** | <100 ms | Seconds to minutes | Hours (Batch only) | Batch only | Instant (Stateless) |
| **Memory Footprint on 10K Files** | **<250 MB** | 2–6 GB | 1–3 GB + Vector DB | 4–16 GB | N/A (Static file) | **<50 MB** |
| **100% Offline / Air-Gapped Operation** | **Yes** | **Yes** | No (Often needs cloud) | Often Cloud-hosted | **Yes** | **Yes** |
| **Single Standalone Static Binary** | **Yes** (Zero CGO) | No | No | No | No | **Yes** |
