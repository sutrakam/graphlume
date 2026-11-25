# GraphLume user guide

This guide takes you from installing GraphLume to using every command in day-to-day work. Follow it in order the first time; after that, use the cheat sheet and jump to the task you need.

Every example here was run against the same sample repository, a small payments platform with Go services, a TypeScript web app, SQL migrations and Kafka events. Long outputs are trimmed, and trims are marked with `…`.

> [!TIP]
> Each command also explains itself: run `graphlume <command> --help` for every flag.

## Contents

- [Cheat sheet](#cheat-sheet)
- [1. Install](#1-install)
- [2. Set up a project](#2-set-up-a-project)
- [3. Connect your coding agent](#3-connect-your-coding-agent)
- [4. Keep the index current](#4-keep-the-index-current)
- [5. Read an answer](#5-read-an-answer)
- [6. Understand the code](#6-understand-the-code)
- [7. Change it safely](#7-change-it-safely)
- [8. Check the repository's health](#8-check-the-repositorys-health)
- [9. Give an agent less to read](#9-give-an-agent-less-to-read)
- [10. Configure a project](#10-configure-a-project)
- [11. The web portal](#11-the-web-portal)
- [12. Share one index with a team](#12-share-one-index-with-a-team)
- [13. Gate pull requests in CI](#13-gate-pull-requests-in-ci)
- [14. Scripting: JSON, global flags and exit codes](#14-scripting-json-global-flags-and-exit-codes)
- [15. Troubleshooting](#15-troubleshooting)
- [16. Remove GraphLume](#16-remove-graphlume)
- [Further reading](#further-reading)

---

## Cheat sheet

| I want to… | Run |
|---|---|
| Set up a repository and connect my agents | `graphlume init --all` |
| Check that the index is healthy | `graphlume status` · `graphlume doctor` |
| Check that my agent is wired up | `graphlume agent --check` |
| Read one function, its callers and callees | `graphlume explore <name>` |
| Find who calls something | `graphlume callers <name>` |
| See how A reaches B | `graphlume path <from> <to>` |
| Get a reading order for a new codebase | `graphlume tour` |
| Find the tests to run for a change | `graphlume tests <name>` |
| Plan a change before making it | `graphlume plan-change <name>` |
| See what a branch puts at risk | `graphlume impact --base main` |
| Rename safely (dry run first) | `graphlume rename <old> <new>` |
| Find who to ask about some code | `graphlume owners <name>` |
| List routes, tables, events and config keys | `graphlume api` · `db` · `events` · `config` |
| Find unused code and risky files | `graphlume dead-code` · `hotspots` · `blindspots` |
| Give an agent exactly the context for a task | `graphlume context "<task>"` |
| Browse it all in a browser | `graphlume ui` |
| Remove it from this project | `graphlume clean --dry-run`, then `graphlume clean` |

Where a name is ambiguous, add `--path <part of the file path>`. Add `--json` to any command for machine-readable output.

---

## 1. Install

**Requirements:** macOS (Intel or Apple silicon), Linux (x86-64 or ARM64) or Windows (x86-64). Git, for the answers that read history. Go 1.26 or later only if you install with `go install`.

GraphLume is one self-contained binary. It needs no database server, no service and no account, and it makes no network calls while it indexes or answers.

### macOS and Linux

```bash
curl -fsSL https://raw.githubusercontent.com/sutrakam/graphlume/main/install.sh | sh
```

The script downloads the release for your platform, checks it against the published checksums and installs it in `~/.local/bin`. To choose the directory or pin a version:

```bash
curl -fsSL https://raw.githubusercontent.com/sutrakam/graphlume/main/install.sh | sh -s -- --dir /usr/local/bin --version v1.0.0
```

### Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/sutrakam/graphlume/main/install.ps1 | iex
```

### With the Go toolchain

```bash
go install github.com/sutrakam/graphlume/cmd/graphlume@latest
```

### In a container

```bash
docker run --rm -v "$PWD:/workspace" ghcr.io/sutrakam/graphlume index
```

### Check the installation

```bash
graphlume version
graphlume doctor
```

`doctor` checks the runtime, the bundled SQLite and its full-text search, and the config files it found. Every line should read `ok`.

To update, run the same install command again. To verify a release's signature and provenance by hand, see [Verifying a release](operations/verifying-a-release.md). For offline and pinned installs, see the [installation guide](getting-started/installation.md).

---

## 2. Set up a project

### The one-command setup

```bash
cd your-project
graphlume init --all
```

`init --all` does five things, and asks before anything that downloads or runs code:

1. Creates the index at `.graphlume/index.db`.
2. Offers the compiler indexers your languages can use, one at a time, saying what each download is and where it comes from. Nothing is installed without a yes.
3. Indexes the repository and binds each reference to its definition.
4. Registers the MCP server with every coding agent it detects.
5. Adds a short guidance block to `AGENTS.md`, so the agent reaches for GraphLume before it falls back to grep.

| Flag | What it does |
|---|---|
| `--local` | Write only project-scoped agent configs (`.mcp.json`, `.cursor/mcp.json`, …), nothing in your home directory |
| `--profile <name>` | The MCP profile registered with agents (default `compact`; see [Profiles](#choose-a-profile)) |
| `--allow-repo-build` | Allow indexers that run the repository's own build (Maven, Gradle, C/C++ compilers, cargo build scripts) |
| `--yes` | Without a terminal, accept only the small pinned, checksum-verified indexers; never a toolchain or a build |
| `--force` | Recreate the index if one exists |

### Step by step

The same setup, one step at a time:

```bash
graphlume init          # create .graphlume/ and the empty index
graphlume index         # parse every file and record what it defines and references
graphlume resolve       # bind each reference to its definition, using compilers where available
```

`index` is incremental: the next run reads only the files that changed. Use `index --full` to rebuild from scratch, then run `resolve` again.

### Read the status

```bash
graphlume status
```

```text
Project:           demo
Index:             READY
Files:             53
Symbols:           291
Relationships:     483 (exact compiler-proven: 439 [bit-exact 90.9%], heuristic: 44 [9.1%])
…
Go graph:          type-checked (go/types): 425 exact edge(s) across 34 of 34 Go file(s)
Typescript graph:  compiler-checked (scip-typescript): 14 exact edge(s) across 3 of 3 file(s)
Last indexed:      2026-11-25T00:57:15+05:30
Changed since:     1 file has changed since the last index; run 'graphlume index' to update it
Kept current:      by agents, which re-index edited files before answering; for the CLI and portal run 'graphlume hooks install' or 'graphlume watch'
Index size:        984.0 KiB
MCP profile:       minimal

Quality: 91% of relationships are compiler-proven; the rest are graded by how sure they are.
```

**Changed since** says whether the index still matches the files on disk, and **Kept current** says what updates it. The line to watch for accuracy is **Relationships**: the share that is compiler-proven. To raise it, install the compiler indexers your languages need:

```bash
graphlume indexers list       # what would run for this repository, and what is missing
graphlume indexers install    # install them, asking before each download
graphlume resolve
```

`graphlume languages` lists what GraphLume can do for each language it parses.

### What is written where

| Path | What it holds | Commit it? |
|---|---|---|
| `.graphlume/` | The index, logs and the audit log | No. It carries its own `.gitignore`, so git ignores it |
| `.graphlume.yml` | Indexing settings, architecture rules and policy, shared by the team | Yes |
| `AGENTS.md` (and `CLAUDE.md` when present) | The guidance block, marked `<!-- graphlume:guidance -->` | Yes |
| `.mcp.json`, `.cursor/mcp.json`, `.vscode/mcp.json` | Project-scoped agent registrations | Your choice |
| User config directory | Machine-wide settings (`graphlume doctor` prints the path) | Not in the repository |

---

## 3. Connect your coding agent

`init --all` registers GraphLume with the agents it finds. This section covers the other ways in, and how to check the result.

### Register with detected agents

```bash
graphlume install --list        # every supported agent, whether it is detected, and manual steps for the rest
graphlume install --dry-run     # what would be written
graphlume install               # write it
```

| Flag | What it does |
|---|---|
| `--only <client>` | Only these clients, for example `--only claude-code` or `--only cursor` |
| `--local` | Only project-scoped configs |
| `--links` | Print one-click install links for Cursor, VS Code, Windsurf and LM Studio |
| `--create` | Write every supported client's config, even where the client is not installed |
| `--profile <name>` | The MCP profile to register |
| `--no-rules` | Do not touch `AGENTS.md` or instruction files |

`graphlume setup` does the setup and the registration in one pass, with the `standard` profile by default. Add `--hooks` to also install a Claude Code SessionStart hook that shows the agent the guidance at every session start and after compaction.

### Several projects on one machine

Run `graphlume init --all` once in each project. Each project gets its own registration in its project files (`.mcp.json`, `.cursor/mcp.json`, `.vscode/mcp.json`). The user-level configs (`~/.claude.json`, `~/.cursor/mcp.json` and the like) hold one entry, `graphlume serve`, that names no project: the server finds the index from the folder the agent is working in, so setting up a second project never redirects the first.

An app with no project folder, such as Claude Desktop, has no folder to work from. Register it for one project by hand, with `--project <path>` before `serve`.

### Register by hand

**Claude Code**

```bash
claude mcp add graphlume -- graphlume serve
```

**Cursor, Windsurf, Gemini CLI and other clients that read `mcp.json`**

```json
{
  "mcpServers": {
    "graphlume": {
      "command": "graphlume",
      "args": ["serve"]
    }
  }
}
```

**OpenAI Codex** (`~/.codex/config.toml`)

```toml
[mcp_servers.graphlume]
command = "graphlume"
args = ["serve"]
```

To serve a project other than the agent's working directory, add `"--project", "/path/to/repo"` before `"serve"`.

### Choose a profile

A profile decides how many tools, and how much tool schema, enter the agent's context. There are 42 tools in all.

| Profile | Tools | Use it when |
|---|---:|---|
| `compact` | 1 | Context is tight. A single front-door tool of about 720 tokens routes every question |
| `minimal` | 8 | You want the core reads: `context`, `overview`, `search`, `symbol`, `definition`, `references`, `callers`, `explore` |
| `standard` | 19 | The agent also edits code: adds `impact`, `tests`, `plan`, `rename`, `blindspots` and the API, database and config tools |
| `full` | 42 | You want everything, including history, architecture, policy, dependencies and events (about 12,800 tokens of schema) |

`graphlume serve` uses `minimal` unless told otherwise. `init --all` registers `compact` and `setup` registers `standard`. To change it, pass `--profile` in the registration, or set it for the project:

```yaml
# .graphlume.yml
mcp:
  profile: standard
```

To see what a profile exposes, and what each tool costs in tokens:

```bash
graphlume serve --profile standard --list-tools
graphlume tool list
```

### Check the wiring

```bash
graphlume agent --check
```

```text
[ok  ]  index             revision 1 — 53 files, 291 symbols, 483 relationships
[ok  ]  resolution        98% of the 122 references that name something this index defines are bound
[ok  ]  mcp tools         profile "minimal" exposes 8: overview, search, symbol, definition, explore, references, callers, context
[warn]  mcp registration  not in this repository's .mcp.json
                            do:    graphlume agent, and run the 'claude mcp add' line it prints
[warn]  guidance          not found in CLAUDE.md or AGENTS.md
                            means: the agent has the tools but no rule telling it when to use them, so it will keep grepping
                            do:    graphlume agent --guidance >> CLAUDE.md
```

Each warning says what it means and what to run. The guidance matters as much as the registration: without it, an agent has the tools but keeps grepping.

```bash
graphlume agent --guidance >> AGENTS.md
```

For each agent's details, see [Coding agents](guides/coding-agents.md).

### Available MCP Tools

| Profile | Adds these tools |
|---|---|
| `minimal` | `context` · `overview` · `search` · `symbol` · `definition` · `references` · `callers` · `explore` |
| `standard` | `impact` · `tests` · `plan` · `rename` · `blindspots` · `endpoints` · `tables` · `config` · `outline` · `diff` · `coverage` |
| `full` | `callees` · `implementations` · `trace` · `changes` · `patch` · `owners` · `hotspots` · `deadcode` · `busfactor` · `communities` · `structure` · `tour` · `arch` · `adr` · `policy` · `deps` · `licenses` · `vulns` · `sbom` · `events` · `graph` · `lessons` · `stats` |

With `graphlume serve --estate FILE`, the `full` profile also exposes `estate`, which answers callers and impact across several repositories. Each tool's arguments: `graphlume tool describe <tool>`, or the [MCP tools reference](reference/mcp-tools.md).

---

## 4. Keep the index current

Pick one. All of them update only the files that changed.

| Method | Command | Best for |
|---|---|---|
| Let the server do it | nothing to run | Agents: `serve` re-indexes edited files before it answers (`--check-edits`, on by default) |
| Watch the tree | `graphlume watch` | Long sessions in the terminal or the portal |
| Git hooks | `graphlume hooks install` | Re-indexing after every commit, merge and checkout |
| By hand | `graphlume index && graphlume resolve` | Scripts and CI |

```bash
graphlume hooks install
graphlume hooks status
```

```text
post-commit:   installed (…/.git/hooks/post-commit)
post-merge:    installed (…/.git/hooks/post-merge)
post-checkout: installed (…/.git/hooks/post-checkout)
```

Each hook runs a background re-index that never fails a git operation and logs to `.graphlume/logs/auto-index.log`. GraphLume writes its own marked block into each hook and leaves the rest of the file alone; `graphlume hooks uninstall` removes only that block.

**Skip indexing on a large repository.** When CI builds the index (see [section 13](#13-gate-pull-requests-in-ci)), download it instead:

```bash
graphlume index pull --update
```

`index pull` uses your `gh` login, finds the `graphlume-index-<commit>` artifact for `HEAD` (or the nearest earlier commit that has one), installs it, and with `--update` catches up with your working tree. To share an index by hand, use `graphlume index export --out <dir>`.

---

## 5. Read an answer

Every relationship GraphLume reports carries a confidence grade and the source location that justifies it.

| Grade | Means | Act on it? |
|---|---|---|
| `exact` | Proven by a compiler or type checker | Yes |
| `high` | Strong structural evidence; measured 86.8 to 96.1% precise | Yes, and glance at the evidence |
| `medium` | Plausible; for example a call through an interface | Check before relying on it |
| `low` | A lead only; for example a name match | Treat as a hint |

Answers also say what they could not see. When a result is shorter than you expected, the note tells you why: an unresolved call, a dynamic dispatch, a depth limit. Most commands take `--min-confidence` to drop the weaker grades.

---

## 6. Understand the code

### Search and find

```bash
graphlume search idempotency -n 5     # full-text search over every indexed file
graphlume symbol Capture              # definitions with this exact name
graphlume symbol captre --fuzzy       # prefix, substring and typo matches
```

```text
services/checkout-api/internal/payments/idempotency.go
db/migrations/003_idempotency_keys.sql
services/checkout-api/internal/api/admin.go
apps/billing-web/src/clients/payments.ts
openapi/checkout-api.yaml

5 file(s) matching "idempotency".
```

### Explore one symbol

`explore` gives the definition, a source excerpt, its callers and callees, and its routes in one answer. It is the best first question about any function.

```bash
graphlume explore Capture --path payments/service.go
```

```text
Capture (method) at services/checkout-api/internal/payments/service.go:49-83
Confidence: high

--- Source Excerpt ---
   49 | func (s *PaymentService) Capture(ctx context.Context, id, key string) (core.Payment, error) {
   50 | 	if first, seen, err := s.idem.Check(ctx, key); err != nil {
   …
```

Add `--diagram mermaid` (or `dot`, `html`) to draw the callers and callees instead.

### Callers, references and callees

```bash
graphlume callers Post --path ledger/post.go
```

```text
2 caller(s) of Post (services/ledger/ledger/post.go:27) are recorded.

KIND   SYMBOL                    EVIDENCE                                               CONFIDENCE
CALLS  PaymentService.Capture    services/checkout-api/internal/payments/service.go:70  exact (compiler)
CALLS  TestPost_WritesBothSides  services/ledger/ledger/post_test.go:26                 exact (compiler)
```

| Command | Answers |
|---|---|
| `callers <name>` | What calls it |
| `references <name>` | Every use: calls, type uses, imports, reads and writes |
| `callees <name>` | What it calls |
| `implementations <type>` | What implements or extends a type, and what it implements |
| `mentions <name>` | Which documents name it |
| `outline <file>` | A file's signatures and doc comments, without bodies |

### How one thing reaches another

```bash
graphlume path CaptureHandler.Serve Exec
```

```text
Serve (services/checkout-api/internal/api/capture.go:17)  ->  Exec (services/checkout-api/internal/store/raw.go:20)

Route 1 (1 hop(s)):
  1.  Serve  -calls->  Exec  services/checkout-api/internal/api/capture.go:25  exact (compiler)

Only shortest routes are shown; longer ones may exist.
```

Use `--from-path` and `--to-path` when either end is ambiguous, and `--max-depth` to look further.

### Learn a new codebase

```bash
graphlume tour          # entry points, then what each one calls, in reading order
graphlume communities   # files grouped by how they refer to each other
```

```text
1. main  (services/risk-engine/cmd/risk-engine/main.go:14)
   -> NewScorer  [exact, called at services/risk-engine/cmd/risk-engine/main.go:21]
   -> NewFlagger  [exact, called at services/risk-engine/cmd/risk-engine/main.go:21]
   -> Score  [exact, called at services/risk-engine/cmd/risk-engine/main.go:30]
     -> recentCount  [exact, called at services/risk-engine/risk/score.go:27]
…
```

### Routes, tables, events, config and infrastructure

```bash
graphlume api
```

```text
METHOD & PATH                   HANDLER   DECLARED AT
POST /v1/payments               Serve     services/checkout-api/internal/api/routes.go:20
GET /v1/payments/{id}           Serve     services/checkout-api/internal/api/routes.go:21
POST /v1/payments/{id}/capture  Serve     services/checkout-api/internal/api/routes.go:22
…
```

```bash
graphlume db
```

```text
TABLE             READS  WRITES  DECLARED HERE
payments          4      3       yes
ledger_entries    2      2       yes
fraud_flags       1      2       yes
idempotency_keys  1      2       yes
```

```bash
graphlume events
```

```text
BROKER  TOPIC             PRODUCERS  CONSUMERS  FQN
kafka   payment.captured  1          2          event:kafka:payment.captured
kafka   payment.refunded  1          0          event:kafka:payment.refunded
```

| Command | Answers |
|---|---|
| `api` | The HTTP endpoints the code exposes, and their handlers |
| `db` · `db table <name>` | Tables, and what reads and writes each one |
| `events` · `events topic <name>` | Topics on Kafka, RabbitMQ, SQS/SNS, Pub/Sub and ActiveMQ, with producers and consumers |
| `config` · `config --all` | Configuration keys: where each is declared and what reads it (mismatches by default) |
| `infra` | Services, ports and config from Dockerfiles, Compose, Kubernetes, Helm and Terraform. `--key <NAME>` says what a key change restarts; values are never read |
| `deps` | Declared dependencies, and whether anything imports them |

---

## 7. Change it safely

The flow for a change: **plan it, find its tests, make it, check what it reached.**

### Plan the change

```bash
graphlume plan-change Post
```

```text
GraphLume Change Plan for Post (method)
Location:      services/ledger/ledger/post.go:27
Risk Level:    MODERATE or higher (Score: 35/100; the analysis was cut at depth 3)
Blast Radius:  2 direct caller(s), 6 total reached

Execution Sequence:
  Step 1: Verify baseline: run existing 3 test file(s) before applying changes
  Step 2: Modify target declaration and implementation for Post (method) [services/ledger/ledger/post.go]
  Step 3: Update direct caller Capture in services/checkout-api/internal/payments/service.go to match new contract
  …
```

### Find the tests to run

```bash
graphlume tests Capture --path payments/service.go
```

```text
TEST                     FILE                                                     KIND  SIGNAL              CONFIDENCE
TestCapture_Happy        services/checkout-api/internal/payments/service_test.go  unit  graph               exact
TestCapture_Idempotent   services/checkout-api/internal/payments/service_test.go  unit  graph               exact
TestCaptureRoute_201     services/checkout-api/internal/api/capture_test.go       unit  graph               exact
TestRetryWorker_Backoff  services/checkout-api/internal/worker/retry_test.go      unit  interface_dispatch  medium

4 test(s) to run; 3 reached through a recorded call edge, 1 through a method Capture implements, the rest by convention.
```

### See what a branch puts at risk

```bash
graphlume impact --base main
```

```text
Blast radius of main..HEAD

3 files changed; 27 changed symbols found in the index, 27 analysed.
22 symbols at risk through recorded references, plus 0 reachable only through dispatch.
Riskiest change: libs/payments-core/kafka/kafka.go:Message — risk high or higher (81/100; …; certainty medium).
  This is the MAXIMUM over the changed symbols, never a total: two risks of 40 do not make an 80.
…
```

`pr-impact` gives the same analysis as a review report; `--out report.md` writes it as Markdown for a pull request.

### Rename

`rename` edits only the sites it can defend, and is a dry run until you add `--apply`.

```bash
graphlume rename Post Record --path ledger/post.go
```

```text
Rename Post -> Record  (services/ledger/ledger/post.go:Ledger.Post, services/ledger/ledger/post.go:27)
DRY RUN: nothing was written. Re-run with --apply to make these edits.

SITES
  exact  services/checkout-api/internal/payments/service.go:70  CALLS
  exact  services/ledger/ledger/post.go:27                      DEFINITION
  exact  services/ledger/ledger/post_test.go:26                 CALLS

LEFTOVER OCCURRENCES (2 line(s) still name Post; showing 2)
  services/checkout-api/internal/api/capture_test.go:43  res, err := http.Post(srv.URL+"/v1/payments/pay_1/capture", …)
  services/ledger/ledger/post.go:26  // Post books a captured payment: debit receivables, credit revenue.
```

Review each leftover by hand: here one is a different `Post` and the other is a comment. `--apply` exits 3, never 0, while any leftover remains. GraphLume never compiles your code, so build and test after the apply. `graphlume move` moves a Go declaration to another file of its package.

### Give the change a contract, then verify it

`compile` turns a request into an explicit task contract: what is in scope, which tests cover it and what must not change. `verify` checks the finished change against it.

```bash
graphlume compile "Add an idempotency check to refunds" --json > contract.json
# … make the change …
graphlume verify --contract contract.json --changed services/checkout-api/internal/payments/service.go
```

```text
VERIFY: ADVISORY

[LEAD] Refund (services/checkout-api/internal/payments/service.go) changed, and none of the tests this contract named was listed as covering it.
  evidence: contract names 7 covering test(s), none against Refund
…
```

| Exit | Verdict | Means |
|---:|---|---|
| 0 | pass | Every check ran and found nothing |
| 0 | advisory | Findings exist, and all of them are leads (`--fail-on-leads` makes this fail) |
| 1 | fail | At least one finding is a fact |
| 2 | unverified | There was nothing to check against; deliberately not a pass |

### Who to ask

```bash
graphlume owners Post --path ledger/post.go
```

```text
A change here reaches 7 symbols across 6 files (2 direct, 4 indirect, 0 through dispatch).

WHO HAS CONTEXT ON THIS CODE

  SHARE  REACHED  FILES  COMMITS  LAST TOUCH  HAS TOUCHED IT
  71%    5 of 7   4      5        2026-05-06  A. Rao <ar@users.noreply.example.com>
  43%    3 of 7   3      2        2026-04-14  J. Okafor <jo@users.noreply.example.com>
…
```

`owners` reads git history across everything the change reaches. It suggests who has context; it is never a finding about who is responsible, and the report says so.

---

## 8. Check the repository's health

| Command | Finds | Useful flags |
|---|---|---|
| `arch` | Violations of the architecture declared in `.graphlume.yml` | `--crossings`, `--fail-on high` |
| `policy check` | Violations of the `policy:` rules, for a branch | `--base`, `--out` (exits 2 on an error-severity violation) |
| `blindspots` | Unresolved calls, untested critical code, unmapped endpoints, orphan tables, missing config, unparsed files | `--kind`, `--fail-on` |
| `dead-code` | Definitions nothing calls or references, graded | `--all`, `--include-exported`, `--path` |
| `hotspots` | Files that change often relative to their size | `--commits`, `--include-tests` |
| `bus-factor` | Directories where recent work rests on the fewest people | `--months`, `--min-commits`, `--files` |
| `vulns` · `sbom` · `licenses` | Advisories against declared dependencies (you supply them) · a CycloneDX SBOM · dependency licences | |

```bash
graphlume policy check
```

```text
Policy check, main..HEAD, 0 changed file(s)

ERROR   graphlume.policy.architecture
        architecture rule handlers-never-touch-the-database (high, forbidden): handlers -> store, PurgeKeys CALLS Exec
        at services/checkout-api/internal/api/admin.go:25, exact confidence
…
```

`graphlume policy explain` lists every rule type with an example.

---

## 9. Give an agent less to read

```bash
graphlume context "add a retry limit to payment capture"
```

```text
Task: add a retry limit to payment capture
Understood as: Concepts: capture, limit, payment, retry, add; action: add. Anchored on 18 symbols named by the task …

KIND           NAME                            LOCATION                                               WHY
interface      Capturer                        services/checkout-api/internal/worker/retry.go:13      named by the task (matches "capture"); …
endpoint       POST /v1/payments/{id}/capture  services/checkout-api/internal/api/routes.go:22        named by the task (matches "capture" and "payment"); …
configuration  RETRY_BACKOFF_MS                config/prod.yml:5                                      named by the task (matches "retry"); …
…
```

The task is the first argument, in quotes. `context` ranks the code the task needs within a budget and says why each item is there; `--show-excluded` lists what it left out.

| Command | Produces |
|---|---|
| `context "<task>"` | The ranked context for one task, within `--max-files`, `--max-symbols` and `--max-lines` |
| `pack --prefix <dir>` | One prompt-ready bundle of files with a directory tree, secrets redacted (`--format xml|markdown|json`) |
| `minimize-diff [patch]` | A unified diff compressed for an agent to read |
| `outline <file>` | Signatures and doc comments only |

---

## 10. Configure a project

`.graphlume.yml` sits at the repository root and is shared through git. This is the sample repository's file, which declares three components and one rule:

```yaml
architecture:
  components:
    - name: handlers
      include: ["services/checkout-api/internal/api/**"]
      exclude: ["**/*_test.go"]
    - name: services
      include: ["services/checkout-api/internal/payments/**"]
    - name: store
      include: ["services/checkout-api/internal/store/**"]
  rules:
    - id: handlers-never-touch-the-database
      description: Handlers call services, never the database layer directly.
      from: [handlers]
      forbidden: [store]
      severity: high

policy:
  architecture:
    severity: error
  no_new_cycles:
    level: package
```

| Section | Controls |
|---|---|
| `architecture` | Components (path globs) and the dependencies allowed between them; read by `arch`, `policy` and the CI gate |
| `policy` | Rules that fail a pull request: `architecture`, `no_new_cycles`, `max_blast_radius` and more (`graphlume policy explain`) |
| `mcp` | `profile`: the tools agents get |

> [!IMPORTANT]
> The repository's config holds indexing settings, rules and policy only. It can never enable code execution: running a repository's build (`--allow-repo-build`) or editing tools (`serve --allow-edits`) is always your choice on the command line, never the repository's.

The full key list is in the [CLI reference](CLI.md).

---

## 11. The web portal

```bash
graphlume ui
```

The portal opens at `http://127.0.0.1:7777`: search, a page for every function, type, file, route and table, branch review, change planning, health reports and tool activity. It is read-only, has no login and listens only on loopback; `--port 0` picks a free port. Every answer shows the `graphlume tool call …` command that returns the same data, so anything you find there can be scripted or handed to an agent.

See the [portal guide](guides/portal-guide.md).

---

## 12. Share one index with a team

Over stdio, `serve` is a private server for one agent. With `--http` it serves MCP over Streamable HTTP, plus the same tools as plain REST at `/api/v1`.

```bash
graphlume serve --http 127.0.0.1:7788
```

Every request needs a bearer token: `GRAPHLUME_MCP_TOKEN`, or the one written to `.graphlume/mcp-token` (readable only by you).

```bash
curl -H "Authorization: Bearer $TOKEN" -d '{"name":"Capture"}' \
     http://127.0.0.1:7788/api/v1/tools/callers
```

The OpenAPI document is at `/api/v1/openapi.json`. Without a server, `graphlume tool call` runs the same tools from a shell:

```bash
graphlume tool call callers name=Post path=services/ledger/ledger/post.go
```

**For a team,** give each person or service its own named, scoped token:

```bash
graphlume serve create-token --name ci-bot --role viewer --profile standard --expires 2027-01-31 --tokens-file team-tokens.jsonl
graphlume serve --http 127.0.0.1:7788 --tokens-file team-tokens.jsonl --audit-log audit.jsonl
```

| Setting | Effect |
|---|---|
| `--role viewer \| editor \| admin` | Read only · editing tools may write when the server allows edits · editor plus listing every project |
| `--tool <name>` | Narrow the token to named tools |
| `--audit-log` | One JSON line per call, with the token's name; records the call, never the answer. `graphlume stats` summarises it |
| `--allow-remote`, `--allowed-host` | Listen beyond loopback, or accept a tunnel's hostname |
| `--allow-edits` | Let editing tools write to disk (otherwise they are dry runs) |
| `--watch` | Keep the index current while serving |

See [HTTP, REST and team servers](guides/protocols.md).

---

## 13. Gate pull requests in CI

The GitHub Action indexes the pull request, posts one sticky comment with its blast radius, checks your architecture and policy rules, and uploads SARIF to code scanning. Save this as `.github/workflows/graphlume.yml`:

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

`fetch-depth: 0` matters: the gate compares against the base branch and reads history.

| Input | Default | What it does |
|---|---|---|
| `fail-on` | `high` | Lowest severity that fails the check: `high`, `medium`, `low` or `none` |
| `version` | `v1.0.0` | The GraphLume release to run |
| `accuracy` | `auto` | `auto` imports compiler facts; `fast` uses heuristics only |
| `comment-pr` | `true` | Post or update the sticky pull request comment |
| `sarif-out` | `graphlume-out/graphlume.sarif` | Where the SARIF report is written |
| `pr-report-out` | `graphlume-out/pr-report.md` | Where the Markdown report is written |
| `upload-index` | `false` | Upload the index as `graphlume-index-<sha>` for `graphlume index pull`. Use it on `push` runs |

**In any other CI,** run the same check with the CLI:

```bash
graphlume init && graphlume index && graphlume resolve
graphlume ci check --base origin/main --format sarif --out graphlume.sarif --fail-on-violations
```

`ci check` writes `markdown`, `sarif` or `json`, and exits 2 on an architecture violation or an error-severity policy violation. See [CI/CD gates](operations/ci-cd-gates.md).

---

## 14. Scripting: JSON, global flags and exit codes

These flags work on every command:

| Flag | Effect |
|---|---|
| `--json` | Machine-readable JSON on stdout |
| `--project <dir>` | Run against another repository (default: the current directory) |
| `-q`, `--quiet` | No logging |
| `-v`, `--verbose` | Verbose logging |

```bash
graphlume callers Post --path ledger/post.go --json | jq -r '.callers[] | "\(.SrcQualified) \(.EvidenceFile):\(.EvidenceLine)"'
graphlume report --help     # render SARIF or a pull-request comment from other commands' --json output
graphlume query --help      # a read-only SQL query against the index
```

| Command | Non-zero exit when |
|---|---|
| `verify` | 1 on a fact, 2 when there was nothing to check (see [section 7](#give-the-change-a-contract-then-verify-it)) |
| `rename --apply` | 3 while any occurrence of the old name is left |
| `policy check`, `ci check --fail-on-violations` | 2 on an error-severity or architecture violation |
| `arch --fail-on`, `blindspots --fail-on` | A finding at that severity or worse exists |
| `bench --fail-on-regression` | Retrieval quality dropped |

`graphlume completion bash|zsh|fish|powershell` prints shell completions.

All environment variables are optional:

| Variable | Effect |
|---|---|
| `GRAPHLUME_CONFIG_HOME` | Where machine-wide configuration lives (default: the platform's config directory, or `XDG_CONFIG_HOME`) |
| `GRAPHLUME_MCP_TOKEN` | The bearer token for `serve --http`, at least 24 characters (default: a random token per run, written to `.graphlume/mcp-token`) |
| `GRAPHLUME_AUDIT_LOG` | The audit log file for `serve` and `tool call`, as `--audit-log` sets it |
| `GRAPHLUME_MEMORY_LIMIT`, `GOMEMLIMIT` | The soft memory limit for indexing, as `--memory-limit` sets it (default 768MiB) |
| `CODEX_HOME`, `KIMI_CODE_HOME` | Where `graphlume install` looks for those agents' configs |
| `NO_COLOR` | Turn off coloured output |

---

## 15. Troubleshooting

Start with these three; each prints what to do next.

```bash
graphlume doctor             # environment, config and index integrity (--verify-index for a deep check)
graphlume status             # what was indexed, skipped and resolved
graphlume agent --check      # what an agent can see and use
```

| Symptom | Fix |
|---|---|
| `no symbol named "X" in the index` | Check the spelling with `graphlume symbol X --fuzzy`. A method is found by its own name: `Post`, or `Ledger.Post` |
| Several definitions match | Add `--path <part of the file path>`; `explore` also takes `--line` |
| A result looks short | Read its notes, then run `graphlume blindspots` and `graphlume status`. Unresolved calls and missing compiler indexers are the usual causes |
| Few compiler-proven edges | `graphlume indexers list`, then `indexers install` and `resolve` |
| Answers describe old code | `graphlume index`, or keep it current with `watch` or `hooks install` |
| The agent ignores GraphLume | Add the guidance: `graphlume agent --guidance >> AGENTS.md` |
| The agent cannot start the server | Use an absolute path to the binary in its config (`which graphlume`) |
| Indexing runs out of memory | `graphlume index --memory-limit 1GiB --workers 2` |
| Index schema is out of date | `graphlume migrate`, or `graphlume init --force` to start over |
| The index is damaged (`database disk image is malformed`) | `graphlume init --all`: it moves the damaged file aside as a backup and rebuilds |
| `… is your home directory, not a project` | `cd` into a project first; GraphLume indexes one repository at a time |

To report a bug, attach the output of `graphlume bundle`: a sanitized diagnostic bundle with no source code. More cases are in [Troubleshooting](troubleshooting.md).

---

## 16. Remove GraphLume

Every removal command takes `--dry-run`. Run it first.

```bash
graphlume clean --dry-run    # preview: this project's .graphlume/ directory
graphlume clean
```

| Command | Removes |
|---|---|
| `clean` | This project's `.graphlume/` directory |
| `clean --agents` | GraphLume's registration from every detected agent |
| `clean --hooks` | GraphLume's git hook blocks in this repository |
| `uninstall` | Everything on this machine: processes, agent registrations, `~/.graphlume` and every registered project (same as `clean --all`) |
| `uninstall --self` | All of the above, and the binary |

`clean --explain` describes every file GraphLume creates and how to recover each one.

---

## Further reading

| Topic | Document |
|---|---|
| Every command and flag | [CLI reference](CLI.md) |
| Every MCP tool and its arguments | [MCP tools](reference/mcp-tools.md) |
| Agents, one by one | [Coding agents](guides/coding-agents.md) |
| HTTP, REST and team servers | [Protocols](guides/protocols.md) |
| The web portal | [Portal guide](guides/portal-guide.md) |
| How accurate the answers are | [Accuracy and benchmarks](accuracy.md) |
| How it works inside | [Architecture](DESIGN_AND_ARCHITECTURE.md) |
| Security model | [Security](security.md) |
| Common questions | [FAQ](FAQ.md) |
