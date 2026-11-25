# MCP tools reference

GraphLume runs as an MCP server over stdio (`graphlume --project <repo> serve`). This page lists the tools the server registers, taken from the server's own `tools/list` response. `TestToolReferenceMatchesTheServer` fails when the page and the server disagree about which tools exist, which profile exposes each, or what arguments each takes.

## Contents

- [Profiles](#profiles)
- [Tools](#tools)
- [Parameters](#parameters)
- [Reading an answer](#reading-an-answer)
- [Choosing between definitions](#choosing-between-definitions)
- [Paging](#paging)
- [What an answer will not state as fact](#what-an-answer-will-not-state-as-fact)
- [Names](#names)
- [Prompts and instructions](#prompts-and-instructions)

## Profiles

Every tool's schema is sent to the agent on every request, so the default profile is small. Set the profile in `.graphlume.yml`:

```yaml
mcp:
  profile: standard   # minimal (default), standard, full, or compact
```

| Profile | Tools |
|---|---|
| `compact` | 1: `graphlume` (front-door interface saving ~25 KB of prompt context; actions: context, search, symbol, callers, callees, references, impact, tests, diff, overview, query, and estate when an estate is configured) |
| `minimal` (default) | 8: `context`, `overview`, `search`, `symbol`, `definition`, `references`, `callers`, `explore` |
| `standard` | 19: the 8 above and `tests`, `impact`, `coverage`, `rename`, `endpoints`, `tables`, `config`, `outline`, `diff`, `plan`, `blindspots` |
| `full` | 43: the 19 above and `changes`, `patch`, `owners`, `callees`, `implementations`, `trace`, `deadcode`, `hotspots`, `busfactor`, `communities`, `graph`, `structure`, `stats`, `estate` (registered only when an estate is configured), and the report tools `policy`, `arch`, `deps`, `licenses`, `vulns`, `sbom`, `tour`, `adr`, `lessons`, `events` (registered wherever the CLI serves tools; see [the report tools](#policy)), so 42 without an estate |

The counts are what `graphlume tool list --profile <name>` prints with an estate configured. `estate` is listed only when the server has an estate: `graphlume serve --estate <file>`, `estate:` in user config, or `estate:` in `.graphlume.yml` (see [`estate`](#estate)).

## Tools

| Tool | Profile | Parameters | What it does |
|---|---|---|---|
| `context` | minimal (default) | `task` (required), `hub_degree_limit`, `limit`, `max_files`, `max_tokens`, `min_confidence`, `offset` | CALL THIS FIRST when you are about to change code, before exploring the repository by hand. |
| `overview` | minimal (default) | none | Call this FIRST, before any other GraphLume tool. |
| `search` | minimal (default) | `query` (required), `limit` | Full-text search across indexed source. |
| `symbol` | minimal (default) | `name` (required), `kind`, `min_confidence` | Locate a symbol's definition by name. |
| `definition` | minimal (default) | `name` (required), `line`, `min_confidence`, `path` | Get a symbol's definition with a bounded source excerpt and its immediate relationships. |
| `references` | minimal (default) | `name` (required), `limit`, `line`, `min_confidence`, `offset`, `path`, `within` | Find where a symbol is used. |
| `callers` | minimal (default) | `name` (required), `limit`, `line`, `min_confidence`, `offset`, `path`, `within` | Find the functions and methods that call a symbol — the callers, not merely the files mentioning it. |
| `tests` | standard | `symbol` (required), `hub_degree_limit`, `limit`, `line`, `max_depth`, `min_confidence`, `offset`, `path` | Find the tests covering a symbol, and which signal found each one — a proven call edge is far stronger evidence than a matching filename, and the response names which you got. |
| `impact` | standard | `symbol` (required), `hub_degree_limit`, `limit`, `line`, `min_confidence`, `offset`, `path` | Before changing a symbol, find out what depends on it. |
| `coverage` | standard | none | Ask this when a callers, references or impact result came back shorter than you expected, BEFORE concluding anything from it. |
| `rename` | standard | `name` (required), `new_name` (required), `limit`, `line`, `min_confidence`, `offset`, `path` | Plan a symbol rename. |
| `endpoints` | standard | `limit`, `method`, `offset`, `query`, `within` | List the HTTP endpoints this service exposes, with the handler that serves each one and where it is declared. |
| `tables` | standard | `limit`, `offset`, `query`, `table`, `within` | Report which code reads and which code writes each database table, with per-table read/write counts. |
| `config` | standard | `key`, `limit`, `offset`, `query`, `status` | Look up one configuration key by name: where it is declared and how many places read it, joined into a single mismatch check; without a key, list every key a page at a time. |
| `outline` | standard | `path` (required) | Get a token-efficient structural outline of a source file by pruning implementation bodies while preserving all imports, types, interfaces, structs, function signatures, and docstrings. |
| `diff` | standard | `diff` (required) | Compress unified git diffs into structured, token-efficient representations. |
| `plan` | standard | `symbol` (required), `hub_degree_limit`, `limit`, `line`, `min_confidence`, `offset`, `path` | Generate an ordered change plan for a symbol: the direct callers the index bound, a risk level, the test files found to cover it, and the steps in order. |
| `blindspots` | standard | `kind`, `limit`, `offset` | Audit the repository for code intelligence blindspots and hidden risks. |
| `explore` | minimal (default) | `name` (required), `limit`, `line`, `min_confidence`, `path` | Deep-dive on a symbol in one call: returns its definition with verbatim line-numbered source excerpt, upstream callers, downstream callees, blast-radius impact summary, and linked HTTP routes. |
| `changes` | full | `base` (required), `head`, `kind`, `limit`, `min_confidence`, `offset` | Find out what a range of commits puts at risk: the symbols the change touched, everything those can reach, and the tests among them. |
| `patch` | full | `content`, `diff`, `path` | Dry-run a change before writing it: pass a unified diff (one or more files), or one file's path and full new content. |
| `owners` | full | `symbol` (required), `limit`, `line`, `months`, `path` | Find who has recent context on the code a change would touch: the people who have lately changed the symbols a change to this one can reach, ranked by how much of that code they have worked on. |
| `callees` | full | `name` (required), `limit`, `line`, `min_confidence`, `offset`, `path` | List what a symbol calls: the definitions inside this repository that its body invokes, each with the file and line of the call and how the binding was established (exact, high, medium or low). |
| `implementations` | full | `name` (required), `direct_only`, `limit`, `line`, `min_confidence`, `offset`, `path` | List a type's hierarchy in both directions: what implements or extends it, directly or through other subtypes, and what it implements or extends itself. |
| `trace` | full | `from` (required), `to` (required), `exclude`, `from_line`, `from_path`, `kinds`, `max_depth`, `min_confidence`, `through`, `to_line`, `to_path`, `via`, `via_line`, `via_path` | Find how one symbol reaches another: the shortest chains of calls and references from 'from' to 'to', hop by hop, each hop with its file, line and confidence. |
| `deadcode` | full | `include_exported`, `include_test_only`, `limit`, `min_certainty`, `path` | List definitions that nothing in the index calls or references, graded by how far that can be trusted. |
| `hotspots` | full | `commits`, `include_tests`, `limit`, `path` | Rank files by how often they change against how much code they hold, from recent git history, and list for each the files that are usually committed with it. |
| `busfactor` | full | `commits`, `depth`, `files`, `limit`, `min_commits`, `months`, `offset`, `path` | Rank directories by how few people made their recent commits: bus factor, the largest contributor's share and how long ago they last changed it. |
| `communities` | full | `limit`, `members`, `min_confidence`, `path` | Group files by how they refer to each other: one row per community, then its most-referenced files. |
| `graph` | full | `query` (required), `limit` | Execute a guarded, read-only SQL query against the repository's code graph (tables: symbols, edges, files, file_edges, file_symbols, references, http_routes, data_access). |
| `structure` | full | `depth`, `include_subfolders`, `limit`, `min_confidence`, `offset`, `owners`, `path` | Folder-level map of the repository: one row per folder with its files, definitions, how many relationships leave it and reach it from other folders, how much of its code a test reaches, who has recently changed it, and a relative risk grade, plus the relationships between folders by confidence tier. |
| `policy` | full | `base`, `head` | Check the repository's own policy rules against the change between two git revisions and the index: symbols a change puts at risk, new dependency cycles, architecture violations and API contract findings. |
| `arch` | full | `crossings`, `min_confidence` | Check the code against the architecture the repository declares in .graphlume.yml: components, allowed dependencies, and every crossing and violation with the file and line that proves it. |
| `deps` | full | `all` | List the dependencies the project declares in its manifests and how many indexed files import each. |
| `licenses` | full | none | Report the licence of each declared third-party dependency from files already on this machine, with its source, evidence path and confidence; a licence not found locally is unknown with the reason. |
| `vulns` | full | `advisories`, `all`, `ignore`, `limit` | Check the declared dependencies against OSV advisories you supply as files in the repository, and say what the index shows about their use. |
| `sbom` | full | `name` | Produce the CycloneDX software bill of materials of the declared dependencies. |
| `tour` | full | `depth`, `limit`, `min_confidence`, `width` | A reading order for an unfamiliar codebase: entry points, then what each calls, breadth first. |
| `adr` | full | `query` | List the architecture decision records the repository keeps, or show the ones matching a number or part of a title. |
| `lessons` | full | `top` | Report what the recorded task lessons say the context keeps missing, and what keeps helping. |
| `events` | full | `broker`, `topic` | List the event bus topics the code publishes to or consumes from, each with its producers and consumers. |
| `stats` | full | none | Report what the current session has used: calls, failures, answer sizes, time, and the cost of the tool definitions. |
| `estate` | full | `action` (required), `depth`, `limit`, `min_confidence`, `offset`, `path`, `symbol` | Ask across the repositories of a multi-repository estate: HTTP route links, shared tables and Go module links between members, and callers and impact of `member:symbol` across them. |

## Parameters

### `context`

CALL THIS FIRST when you are about to change code, before exploring the repository by hand. Give it the task in plain English and it returns the minimum set of files, symbols, tests and database tables the change actually touches, found by walking the call graph rather than by keyword matching. Every included item states why it is there, and notable near-misses are listed with the reason they were excluded. Using this instead of grepping the repository is the point of this server; prefer it over search and symbol when you are starting a change rather than looking up one known symbol.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `task` | string | yes | Describe the change you are about to make, in plain English, as you would to a colleague. For example: 'Add idempotency to payment callback processing'. Full sentences work better than keywords — the task is parsed for intent, not matched literally. |
| `hub_degree_limit` | integer | no | Stop the walk from expanding THROUGH any symbol with more than this many edges. Default 0 (off), which is what every result you have seen so far used. Set it (50 is a reasonable start) when a result is enormous because the walk crossed a logger, a util or a base class; pruned symbols are still listed, only their neighbours are not explored, and the response says how many were cut. |
| `limit` | integer | no | Most rows to return. Default 50, maximum 200. |
| `max_files` | integer | no | Cap on returned items (default 20). Raise only if the default answer is genuinely too narrow. |
| `max_tokens` | integer | no | Estimated token budget (characters divided by 4) to assemble the pack for, 1000 to 64000. Default 8000. A larger budget carries more source and more items; a smaller one fewer. |
| `min_confidence` | string | no | Lowest confidence to return: exact, high, medium or low. Default low. Recommended: 'high' for safe automated code changes and blast radius analysis to filter out heuristic false positives. |
| `offset` | integer | no | Rows to skip. The answer names the next offset when more rows exist. |

### `overview`

Call this FIRST, before any other GraphLume tool. Reports what the project is, how much of it is indexed, which languages it uses, and how much of the code GraphLume could resolve. The resolution figure is the important one: it tells you whether relationship answers for this repo are trustworthy or mostly inferred. Do not guess at coverage — this tool states it.

No parameters.

### `search`

Full-text search across indexed source. Prefer this over your own grep: it searches the index and returns the paths of the matching files, best match first, at a fraction of the context. Use it when you know roughly what you are looking for but not where it lives, and to confirm any relationship result that came back empty, incomplete or low-confidence.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `query` | string | yes | Search terms. Matches identifiers and code text; camelCase identifiers are searchable by their parts, so 'callback' finds handleCallback. Punctuation separates words: 'errors.New' finds those two side by side. |
| `limit` | integer | no | Most files to return. Default 20, maximum 200. |

### `symbol`

Locate a symbol's definition by name. Faster and more precise than searching when you already know the identifier. Every definition of the name is listed with its file and line; when there are several, pass one row's `path` and `line` to the other tools to choose it. If this returns nothing, the symbol may still exist under a different name or in an unindexed file — fall back to search rather than concluding it is absent.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Symbol name to find, e.g. 'TransactionService' or 'updateStatus'. Exact match on the name, not a substring. |
| `kind` | string | no | Optional filter: class, interface, function, method, field, constant, type_alias. |
| `min_confidence` | string | no | Lowest confidence to return: exact, high, medium or low. Default low. Recommended: 'high' for safe automated code changes and blast radius analysis to filter out heuristic false positives. |

### `definition`

Get a symbol's definition with a bounded source excerpt and its immediate relationships. Call this after symbol instead of reading the whole file. The excerpt is bounded and may be truncated; the response says so explicitly when it is, so check that flag rather than assuming you have the full body.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Symbol name, e.g. 'updateStatus' or 'TransactionService'. |
| `line` | integer | no | Line the definition starts on, to choose between several in one file (overloads). |
| `min_confidence` | string | no | Lowest confidence to return: exact, high, medium or low. Default low. Recommended: 'high' for safe automated code changes and blast radius analysis to filter out heuristic false positives. |
| `path` | string | no | File or directory of the definition, to choose between symbols that share the name. |

### `references`

Find where a symbol is used. Every result carries a confidence tier: 'exact' means a compiler proved the binding, 'high' and 'medium' and 'low' mean GraphLume inferred it and may be wrong. Pass min_confidence='high' when a wrong result costs more than a missing one. CRITICAL: an EMPTY result is ambiguous — it may mean nothing references this symbol, or that GraphLume could not resolve the references that exist. Never conclude a symbol is unused from an empty result; confirm with search.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Symbol name whose relationships you want. |
| `limit` | integer | no | Most rows to return. Default 50, maximum 200. |
| `line` | integer | no | Line the definition starts on, to choose between several in one file (overloads). |
| `min_confidence` | string | no | Lowest confidence to return: exact, high, medium or low. Default low. Recommended: 'high' for safe automated code changes and blast radius analysis to filter out heuristic false positives. |
| `offset` | integer | no | Rows to skip. The answer names the next offset when more rows exist. |
| `path` | string | no | File or directory of the definition, to choose between symbols that share the name. |
| `within` | string | no | Keep only rows whose call site is in this file or under this directory (e.g. 'src/'). The first line and the verdict then describe those rows alone. |

### `callers`

Find the functions and methods that call a symbol — the callers, not merely the files mentioning it. Use before changing a signature or behaviour. Every result carries a confidence tier, from 'exact' (a compiler proved the binding) down through 'high', 'medium' and 'low' (GraphLume inferred it and may be wrong); pass min_confidence='high' to see only bindings GraphLume can defend. CRITICAL: an EMPTY result is ambiguous and unresolved calls are reported separately, so absence of callers here is NOT evidence the symbol is dead. Confirm with search before deleting anything.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Symbol name whose relationships you want. |
| `limit` | integer | no | Most rows to return. Default 50, maximum 200. |
| `line` | integer | no | Line the definition starts on, to choose between several in one file (overloads). |
| `min_confidence` | string | no | Lowest confidence to return: exact, high, medium or low. Default low. Recommended: 'high' for safe automated code changes and blast radius analysis to filter out heuristic false positives. |
| `offset` | integer | no | Rows to skip. The answer names the next offset when more rows exist. |
| `path` | string | no | File or directory of the definition, to choose between symbols that share the name. |
| `within` | string | no | Keep only rows whose call site is in this file or under this directory (e.g. 'src/'). The first line and the verdict then describe those rows alone. |

### `tests`

Find the tests covering a symbol, and which signal found each one — a proven call edge is far stronger evidence than a matching filename, and the response names which you got. Use before changing behaviour, to know what to run. A symbol reported as untested is a positive finding, not silence; when the calls to the symbol could not be bound the answer says UNKNOWN instead, and means it.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `symbol` | string | yes | The symbol you are about to change, e.g. 'updateStatus' or 'TransactionService'. |
| `hub_degree_limit` | integer | no | Stop the walk from expanding THROUGH any symbol with more than this many edges. Default 0 (off), which is what every result you have seen so far used. Set it (50 is a reasonable start) when a result is enormous because the walk crossed a logger, a util or a base class; pruned symbols are still listed, only their neighbours are not explored, and the response says how many were cut. |
| `limit` | integer | no | Most rows to return. Default 50, maximum 200. |
| `line` | integer | no | Line the definition starts on, to choose between several in one file (overloads). |
| `max_depth` | integer | no | Most calls between a test and the symbol, 1 to 8. 1 lists the tests that call it directly; 2 adds tests that call a function that calls it. Default 4. |
| `min_confidence` | string | no | Lowest confidence to return: exact, high, medium or low. Default low. Recommended: 'high' for safe automated code changes and blast radius analysis to filter out heuristic false positives. |
| `offset` | integer | no | Rows to skip. The answer names the next offset when more rows exist. |
| `path` | string | no | File or directory of the definition, to choose between symbols that share the name. |

### `impact`

Before changing a symbol, find out what depends on it. Separates DIRECT callers (a proven call edge) from INDIRECT reach and from dispatch rows the runtime MAY execute but no call site proves, and names what it CANNOT see — dynamic dispatch, unresolved references, traversal limits. The risk level is 'unknown', never 'low', when the callers could not be determined, and the first line says so. The result is a LOWER BOUND: treat an empty or short result as 'nothing found', never as proof that changing the symbol is safe.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `symbol` | string | yes | The symbol you are about to change, e.g. 'updateStatus' or 'TransactionService'. |
| `hub_degree_limit` | integer | no | Stop the walk from expanding THROUGH any symbol with more than this many edges. Default 0 (off), which is what every result you have seen so far used. Set it (50 is a reasonable start) when a result is enormous because the walk crossed a logger, a util or a base class; pruned symbols are still listed, only their neighbours are not explored, and the response says how many were cut. |
| `limit` | integer | no | Most rows to return. Default 50, maximum 200. |
| `line` | integer | no | Line the definition starts on, to choose between several in one file (overloads). |
| `min_confidence` | string | no | Lowest confidence to follow: exact, high, medium or low. Default medium, so low relationships (a hint that the name exists, not a defended binding) are left out and counted; pass 'low' to walk them too, or 'high' when the result will gate an automated change. |
| `offset` | integer | no | Rows to skip. The answer names the next offset when more rows exist. |
| `path` | string | no | File or directory of the definition, to choose between symbols that share the name. |

### `coverage`

Ask this when a callers, references or impact result came back shorter than you expected, BEFORE concluding anything from it. It reports how many of this index's references were actually bound to a definition, and names the two populations excluded from that rate: the ambiguous ones, where several definitions matched and GraphLume refused to guess, and the ones that matched nothing here at all, which are mostly library calls. A low figure means every relationship answer from this index is a LOWER BOUND drawn from a minority of the evidence — an empty result then tells you about the index, not about the code — and the response names the command that raises it. Takes no arguments.

No parameters.

### `rename`

Plan a symbol rename. Returns the edit SITES — a file and a line each, with no column — split into 'apply' (confidence 'exact': a compiler proved the binding) and 'review' (GraphLume inferred the binding and may be wrong). You must locate the identifier on each line yourself; a line may contain it more than once or as part of a longer name. WRITES NOTHING. The plan is never complete: references GraphLume could not resolve are absent and are not reported as absent, so after applying it, run search for the old name to find bindings that were missed entirely.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Symbol to rename. |
| `new_name` | string | yes | The replacement identifier. Used only to render the plan; nothing is written. |
| `limit` | integer | no | Most rows to return. Default 50, maximum 200. |
| `line` | integer | no | Line the definition starts on, to choose between several in one file (overloads). |
| `min_confidence` | string | no | Lowest confidence to return: exact, high, medium or low. Default low. Recommended: 'high' for safe automated code changes and blast radius analysis to filter out heuristic false positives. |
| `offset` | integer | no | Rows to skip. The answer names the next offset when more rows exist. |
| `path` | string | no | File or directory of the definition, to choose between symbols that share the name. |

### `endpoints`

List the HTTP endpoints this service exposes, with the handler that serves each one and where it is declared. Routes are recognised from the framework's own syntax — a Spring annotation, a Go router registration, a FastAPI or Flask decorator, an Express member call — never from a string that merely looks like a path, which is what keeps a log message mentioning 'GET /users' out of the result. Pass method to filter to one HTTP verb, query to match part of a path or handler name, within to keep one folder; the answer is paged (limit, offset, next_offset) and states the total. NOT covered: OpenAPI documents, GraphQL schemas, gRPC service definitions and API-gateway configuration — a service defined entirely in those reports no endpoints here, which is a coverage limit, not a finding about the code. Prefer this over grepping route decorators by hand.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `method` | string | no | Filter to one HTTP method, e.g. GET. Case-insensitive. |
| `query` | string | no | Keep only endpoints whose method and path, or handler name, contain this text, e.g. '/orders'. Case-insensitive. |
| `within` | string | no | Keep only endpoints declared in this file or under this directory, e.g. 'services/billing/'. |
| `limit` | integer | no | Most rows to return. Default 50, maximum 200. |
| `offset` | integer | no | Rows to skip. The answer names the next offset when more rows exist. |

Without arguments the answer is what it always was: the first 50 routes. `page.total` is how many match, and `page.next_offset` is the offset that reads on (see [Paging](#paging)).

### `tables`

Report which code reads and which code writes each database table, with per-table read/write counts. Pass table to look up one table by name, query to match part of a name, or within to count only the code in one folder; the answer is paged (limit, offset, next_offset) and states the total. READ and WRITE are counted separately: 'what touches this table' is a weaker question than 'what writes it', and the second is the one to check before changing a column. A table no migration here declares is still reported — that is the normal shape of a service reading a database another service owns. Some statements are assembled at run time; the response says how many, because whether that exact statement executes as written cannot be established by a static read. Prefer this over grepping for SQL string literals or ORM calls: those are exactly the queries this scan may also miss, and this tool says so instead of reporting a false all-clear.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `table` | string | no | Filter to one table name. Case-insensitive. |
| `query` | string | no | Keep only tables whose name contains this text, e.g. 'order'. Case-insensitive. |
| `within` | string | no | Count only the reads and writes made by code in this file or under this directory, e.g. 'services/billing/'. A table no code there touches is left out. |
| `limit` | integer | no | Most rows to return. Default 50, maximum 200. |
| `offset` | integer | no | Rows to skip. The answer names the next offset when more rows exist. |

### `config`

Look up one configuration key by name: where it is declared and how many places read it, joined into a single mismatch check. Without key it lists every key, a page at a time, each with where it is declared, how often it is read and its status (undeclared, unread or declared), undeclared first; filter with query and status. The two findings worth acting on are a key READ but nowhere DECLARED — the way a service fails at start-up — and a key declared and never read, which is either dead configuration or a lookup this scan cannot see. VALUES ARE NEVER READ: no field in the response can hold one, so a wrong redaction rule cannot leak one either; a key whose name looks like a credential is labelled sensitive by name alone. Declarations are read from .env, YAML, JSON, TOML and .properties files — Kubernetes, Helm, Terraform and CI definitions are not, so a key set only there reports as undeclared even though it works in production, which is a coverage limit, not a finding. Prefer this over grepping for the key string across the repository.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `key` | string | no | The configuration key to look up, e.g. DATABASE_URL. Omit it to list every key, a page at a time. |
| `query` | string | no | Listing only: keep keys whose name contains this text. Case-insensitive. |
| `status` | string | no | Listing only: keep keys in one state: undeclared (read, declared nowhere), unread (declared, read by no code) or declared (declared and read). |
| `limit` | integer | no | Most rows to return. Default 50, maximum 200. |
| `offset` | integer | no | Rows to skip. The answer names the next offset when more rows exist. |

With a key the answer is unchanged. Without one, each row is a `config_key` item whose `why` starts with its status: `undeclared: 3 read(s), declared nowhere; read at <file>:<line>`, `unread: declared in <file>:<line>, ...` or `declared: 2 read(s); declared in <file>:<line>`. The summary counts each status; `page.total` is how many rows match.

### `outline`

Get a token-efficient structural outline of a source file by pruning implementation bodies while preserving all imports, types, interfaces, structs, function signatures, and docstrings. Use this instead of reading an entire large file (reducing 10,000 tokens to 300 tokens) when you only need to inspect declarations, API contracts, or symbol signatures.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `path` | string | yes | File path to outline (relative to repo root or absolute). Prunes implementation bodies to reduce token size while preserving all types, structs, interfaces, signatures, and docstrings. |

### `diff`

Compress unified git diffs into structured, token-efficient representations. Strips redundant index hashes and file timestamps, extracts enclosing symbol headers, and collapses unchanged context runs to minimize context token usage. Use this when analyzing pull requests, patches, or git diffs to preserve context window space.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `diff` | string | yes | Unified diff text (e.g. git diff output) to compress into a structured, token-efficient representation. |

### `plan`

Generate an ordered change plan for a symbol: the direct callers the index bound, a risk level, the test files found to cover it, and the steps in order. Use it before modifying or refactoring existing code. The callers are the ones GraphLume could bind: when they could not be determined the risk is 'unknown' and the first line says so.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `symbol` | string | yes | The symbol you are about to change, e.g. 'updateStatus' or 'TransactionService'. |
| `hub_degree_limit` | integer | no | Stop the walk from expanding THROUGH any symbol with more than this many edges. Default 0 (off), which is what every result you have seen so far used. Set it (50 is a reasonable start) when a result is enormous because the walk crossed a logger, a util or a base class; pruned symbols are still listed, only their neighbours are not explored, and the response says how many were cut. |
| `limit` | integer | no | Most rows to return. Default 50, maximum 200. |
| `line` | integer | no | Line the definition starts on, to choose between several in one file (overloads). |
| `min_confidence` | string | no | Lowest confidence to return: exact, high, medium or low. Default low. Recommended: 'high' for safe automated code changes and blast radius analysis to filter out heuristic false positives. |
| `offset` | integer | no | Rows to skip. The answer names the next offset when more rows exist. |
| `path` | string | no | File or directory of the definition, to choose between symbols that share the name. |

### `blindspots`

Audit the repository for code intelligence blindspots and hidden risks. Detects unresolved external and internal calls, untested critical functions with high fan-in, unmapped HTTP endpoints, orphan database tables, missing config declarations, and unparsed files. Use this to audit codebase health and find coverage gaps.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `kind` | string | no | Keep one category, a page at a time: unresolved_call, untested_critical, unmapped_endpoint, orphan_table, missing_config or unparsed_file. Omit it for every category. |
| `limit` | integer | no | Most rows to return. Default 50, maximum 200. |
| `offset` | integer | no | Rows to skip. The answer names the next offset when more rows exist. |

Without arguments the answer is unchanged: every category, the rows over the answer's bound counted per category under `omitted`. With `kind` (or `limit` or `offset`) it lists that category's findings a page at a time, and `page.total` is how many there are (each category stops at 50).

### `explore`

Deep-dive on a symbol in one call: returns its definition with verbatim line-numbered source excerpt, upstream callers (with confidence tiers: 'exact' vs 'heuristic'), downstream callees, blast-radius impact summary, and linked HTTP routes. An empty callers result is ambiguous — confirm with search before assuming a symbol is dead.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Symbol name to explore. |
| `limit` | integer | no | Maximum callers/callees to return (default 10). |
| `line` | integer | no | 1-based line number for precise symbol selection. |
| `min_confidence` | string | no | Lowest confidence to return: exact, high, medium or low. Default low. Recommended: 'high' for safe automated code changes and blast radius analysis to filter out heuristic false positives. |
| `path` | string | no | File path to disambiguate symbol. |

### `changes`

Find out what a range of commits puts at risk: the symbols the change touched, everything those can reach, and the tests among them. Use it to review a branch or a pull request before merging, or to check your own work before you push — pass the base branch and, optionally, the head. Changed symbols are listed first, then the tests that reach them (run those first), then the other code within reach, each with its confidence tier. It reads the index as built: if the index is older than the head of the range, symbols added since are missing and the response says so. To ask about ONE symbol, use impact. Needs a git repository.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `base` | string | yes | The revision the change starts from: a branch, tag or commit, e.g. 'main' or 'origin/main'. |
| `head` | string | no | The revision the change ends at. Default 'HEAD'. |
| `kind` | string | no | Return only one list, to page through it with limit and offset: changed (the changed symbols), file (every changed file), reached (code that depends on the changes, tests excluded) or test (tests that reach them). Default: all lists together. |
| `limit` | integer | no | Most rows to return. Default 50, maximum 200. |
| `min_confidence` | string | no | Lowest confidence to follow: exact, high, medium or low. Default medium. Use 'high' when the result will gate a merge. |
| `offset` | integer | no | Rows to skip. The answer names the next offset when more rows exist. |

### `patch`

Dry-run a change before writing it: pass a unified diff (one or more files), or one file's path and full new content. Lists the callers that removed or re-signed definitions would break, and for Go type-checks the patched packages and their importers in memory and names every line that would stop compiling. Nothing is written.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `content` | string | no | The proposed full file content to dry-run speculatively in memory. With path; or pass diff instead. |
| `diff` | string | no | A unified diff (git diff, diff -u) of one or more files, applied in memory to the working tree. Use instead of path and content. |
| `path` | string | no | File path of the file being modified, e.g. 'internal/service.go'. With content; or pass diff instead. |

### `owners`

Find who has recent context on the code a change would touch: the people who have lately changed the symbols a change to this one can reach, ranked by how much of that code they have worked on. Use it to suggest a reviewer for a pull request, or to find who to ask before changing unfamiliar code. It reports who CHANGED the code recently, read from git history, and not who owns it or must approve it: someone in CODEOWNERS may be absent. Names only; email addresses are never returned. Needs a git repository.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `symbol` | string | yes | The symbol you are changing, e.g. 'updateStatus' or 'TransactionService'. |
| `limit` | integer | no | Most people to list. Default 5. |
| `line` | integer | no | Line the definition starts on, to choose between several in one file (overloads). |
| `months` | integer | no | How many months of history to read, counted back from the newest commit. Default 12 (at most 5000 commits). |
| `path` | string | no | File or directory of the definition, to choose between symbols that share the name. |

### `callees`

List what a symbol calls: the definitions inside this repository that its body invokes, each with the file and line of the call and how the binding was established (exact, high, medium or low). Use it to read a function's dependencies without opening the file, or to walk down from an entry point. It lists only calls the index could bind to a definition HERE: calls into libraries and the standard library never appear, so an empty or short result means 'nothing bound inside this repository', not 'calls nothing'. For the opposite direction use callers.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Symbol name whose relationships you want. |
| `limit` | integer | no | Most rows to return. Default 50, maximum 200. |
| `line` | integer | no | Line the definition starts on, to choose between several in one file (overloads). |
| `min_confidence` | string | no | Lowest confidence to return: exact, high, medium or low. Default low. Recommended: 'high' for safe automated code changes and blast radius analysis to filter out heuristic false positives. |
| `offset` | integer | no | Rows to skip. The answer names the next offset when more rows exist. |
| `path` | string | no | File or directory of the definition, to choose between symbols that share the name. |

### `implementations`

List a type's hierarchy in both directions: what implements or extends it, directly or through other subtypes, and what it implements or extends itself. Use it before changing an interface, an abstract method or a base class, when the callers of the declaration are not the whole story because the work happens in the types built on it. Each row carries its confidence tier; exact means a compiler index proved the relationship. Only DECLARED relationships are visible from syntax, so a Go type that satisfies an interface without naming it is absent until a compiler index is imported — an empty result means none declared, not none exist.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | Symbol name whose relationships you want. |
| `direct_only` | boolean | no | Only direct subtypes. Default false: also those reached through other subtypes, direct ones first. |
| `limit` | integer | no | Most rows to return. Default 50, maximum 200. |
| `line` | integer | no | Line the definition starts on, to choose between several in one file (overloads). |
| `min_confidence` | string | no | Lowest confidence to return: exact, high, medium or low. Default low. Recommended: 'high' for safe automated code changes and blast radius analysis to filter out heuristic false positives. |
| `offset` | integer | no | Rows to skip. The answer names the next offset when more rows exist. |
| `path` | string | no | File or directory of the definition, to choose between symbols that share the name. |

### `trace`

Find how one symbol reaches another: the shortest chains of calls and references from 'from' to 'to', hop by hop, each hop with its file, line and confidence. Use it to answer 'how does this request end up writing that table', to confirm a suspected dependency before a refactor, or to explain a blast radius entry you did not expect. Only the SHORTEST routes are returned, at most three. No route found is not proof of independence: one unresolved call or interface dispatch along the way breaks the chain, and the response says so.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `from` | string | yes | Symbol the path starts at, e.g. 'handleCallback'. |
| `to` | string | yes | Symbol the path should reach, e.g. 'saveTransaction'. |
| `exclude` | array | no | Symbol names to exclude from paths. |
| `from_line` | integer | no | Line the 'from' definition starts on. |
| `from_path` | string | no | File or directory of the 'from' definition, when several symbols share the name. |
| `kinds` | array | no | Edge kinds to traverse (e.g. ['calls']). |
| `max_depth` | integer | no | Longest route to look for, in hops. Default 6. Raise it only if the answer was 'no route found' and you expect a long one. |
| `min_confidence` | string | no | Confidence floor: exact, high, medium, or low. |
| `through` | array | no | Further symbols the path must pass through, in order, after 'via' (at most 3; each must name one definition). |
| `to_line` | integer | no | Line the 'to' definition starts on. |
| `to_path` | string | no | File or directory of the 'to' definition, when several symbols share the name. |
| `via` | string | no | Intermediate symbol the path must pass through. |
| `via_line` | integer | no | Line the 'via' definition starts on. |
| `via_path` | string | no | File path of the 'via' definition. |

### `deadcode`

List definitions that nothing in the index calls or references, graded by how far that can be trusted. 'likely' means the definition is known to be unexported, its file parsed cleanly, no unbound reference anywhere is spelled like it and its name appears nowhere else in the source text; 'possible' means one of those could not be ruled out, and the row says which. Export status is known for Go only, so in other languages nothing is graded better than 'possible'. Only 'likely' is returned unless you ask. Use it to find cleanup candidates or to check whether code you are about to maintain is reachable at all. NEVER delete on this evidence alone: reflection, dependency injection, framework registration and other repositories all reach code without a reference this index can bind, so search for the name first. Tests, entry points and, by default, exported definitions are left out.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `include_exported` | boolean | no | Also report exported definitions, which are never better than 'possible'. Default false: in a library the whole public API has no caller in the index. |
| `include_test_only` | boolean | no | Also list definitions that DO have callers, every one of them in a test file. They come in their own section, always 'possible' and never 'likely': production code only its tests keep alive is a judgement call, not unused code. Default false: it is a second pass over the index. |
| `limit` | integer | no | Most candidates to return. Default 25. |
| `min_certainty` | string | no | 'likely' (the default) returns only candidates where every ordinary reason for a missing caller was ruled out. 'possible' adds the rest, which are mostly uses the index could not bind: search before trusting any of them. |
| `path` | string | no | Only report definitions in files under this path prefix, e.g. 'internal/billing/'. |

### `hotspots`

Rank files by how often they change against how much code they hold, from recent git history, and list for each the files that are usually committed with it. Use it to decide where a refactor pays back, to see where review attention should go, and — through 'changes with' — to find files coupled to the one you are editing even though no call or import connects them, which is a dependency impact cannot see. The score is relative to this repository and this commit window; it orders the list and is not a quality measurement. Needs a git repository.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `commits` | integer | no | How many recent commits to read. Default 500. |
| `include_tests` | boolean | no | Also rank test files. Default false: tests change with the code they cover. |
| `limit` | integer | no | Most files to return. Default 10. |
| `path` | string | no | Only rank files under this path prefix. |

### `busfactor`

Lists the directories where recent work is concentrated in the fewest people,
most concentrated first. The bus factor of a directory is the smallest number
of people whose commits make up at least half of the commits that touched it in
the history read; one means a single person made half or more of them.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `path` | string | no | Only measure files under this path prefix, e.g. 'internal/billing/'. |
| `months` | integer | no | How many months of history to measure, counted back from the newest commit. Default 12. |
| `commits` | integer | no | The most recent commits to read to cover those months (the commit budget). Default 5000, which is also the maximum. |
| `limit` | integer | no | Most directories to return. Default 15, maximum 200. |
| `offset` | integer | no | Directories to skip, for the next page. The answer names the next offset when more measured directories exist. |
| `min_commits` | integer | no | Fewest commits a directory needs before it is measured. Default 5. Directories below it are counted in the answer, not listed. |
| `depth` | integer | no | Group by the first N path segments instead of by each file's own directory, e.g. 1 for top-level directories. Default 0. |
| `files` | boolean | no | Measure each file instead of each directory. Default false. |

Each row gives the bus factor, the commits and distinct author names behind it,
the largest contributors with their shares, and for the largest the date of
their last change there and how many days before the newest commit read that
was. The summary gives the same figure for every commit read taken together.

What the answer always states:

- the window: the months asked for (12 by default, counted back from the newest
  commit), how many commits fell inside it, the dates they span, and whether the
  commit budget ran out before the whole period was read. The structured answer
  carries the same in `history_window` (`months`, `since`, `from`, `to`,
  `commits`, `budget`, `covered`), and `page` gives the measured total and the
  next offset;
- how many directories had too few commits to measure. Those are unmeasured, not
  low risk, and an empty list means nothing could be measured;
- how many directories the history touched that the index does not hold;
- that it counts who changed the code, not who understands it;
- that a shallow clone or a squash-merge workflow distorts the figures. On a
  shallow clone it says so in a note of its own, with how many commits the
  clone holds. `hotspots` and `owners` say the same.

People are the author names git recorded, exactly as spelled. Names are never
merged: when two names committed from one address the answer names both and
leaves them apart. No email address is returned, in the text or the structured
result. Without a git repository the tool answers that nothing was measured; it
does not fail. Command: `graphlume bus-factor`.

### `communities`

Groups the repository's files into communities: sets of files whose calls and
references run between each other more than they run outwards. Use it to see how
an unfamiliar repository divides, or to find the files that belong with the one
about to change.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `path` | string | no | Only group files under this path prefix. Relationships that leave the prefix are counted and not followed. |
| `limit` | integer | no | Most communities to return, largest first. Default 8. |
| `members` | integer | no | Most files to list for each community, most referenced first. Default 5. |
| `min_confidence` | string | no | Weakest relationship that links two files: exact, high, medium or low. Default medium. |

Each community is one `community` row followed by its `member` rows.

| Part of the row | Meaning |
|---|---|
| Name | The label: the directory every member is under, or, when they share none, the most-referenced member. The row says which. |
| Path | The community's id: its first member in path order. A real file, and the same on every call over the same index, whatever `limit` and `members` are. |
| Reason | Size, the directory holding most of it, relationships inside against relationships crossing its boundary, and how many members are not listed. |

What the answer does not claim, and says so each time:

- A community describes how the indexed code refers to itself. It is not a
  declared architecture.
- Only relationships the index bound are counted. A reference it could not bind
  is a missing link, so files that work together can be reported apart or in no
  community. The answer states how many references are unbound.
- Low-confidence relationships are left out by default, because one wrong name
  match can merge unrelated groups. The answer counts what was left out;
  `min_confidence: low` counts them.
- Files in no community are counted, not listed. Unlinked is not unused.

Bounds: a community and each listed member are one item each, and an answer
holds at most 50, so `members` is reduced when `limit` is large and the answer
says by how much. A file graph of more than 250,000 file-to-file links is not
partitioned: the answer is empty, marked truncated, and asks for a `path`.

The partition is deterministic label propagation over the file graph; no model
and no randomness is involved. Command: `graphlume communities`.

### `graph`

Execute a guarded, read-only SQL query against the repository's code graph (tables: `symbols`, `edges`, `files`, `file_edges`, `file_symbols`, `references`, `http_routes`, `data_access`). `file_edges` holds the edges between files already summed, one row per `src_file_id`, `dst_file_id`, `kind` and `confidence` with the count `n`, and `file_symbols` each file's definition count `n`; group those by path to compare directories, which stays fast on a large index where grouping `edges` does not. Enables custom topological filtering and multi-hop queries without multi-roundtrip MCP calls.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `query` | string | yes | A read-only SQL query (SELECT or WITH) against index tables: symbols, edges, files, file_edges (edges summed per src_file_id, dst_file_id, kind, confidence; count n), file_symbols (definitions per file_id; count n), references, http_routes, data_access. |
| `limit` | integer | no | Maximum rows to return (default: 50, maximum: 200). |

### `structure`

Folder-level map of the repository, read from the index's per-file relationship sums, so it answers in milliseconds on an index of a million edges. One row per folder at the requested depth, and the relationships between those folders. Use it to see how a repository is organised and which folders depend on which; use `impact` or `callers` for one symbol. Counts are of indexed files only, and a relationship the index could not bind is not counted.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `path` | string | no | Folder to look inside, e.g. 'services/'. Default: the repository root. |
| `depth` | integer | no | How many folder levels below path to group by. Default 1: the folders directly inside path. |
| `include_subfolders` | boolean | no | Count everything below a listed folder in that folder's figures. Default true. False counts only the files directly in it. |
| `limit` | integer | no | Most folders to list. Default 200. |
| `offset` | integer | no | Folders to skip, for the next page. Use next_offset from the previous answer. |
| `owners` | boolean | no | Name who has recently changed each folder, from git history. Default true. False skips reading history. |
| `min_confidence` | string | no | Lowest confidence to return: exact, high, medium or low. Default low. Recommended: 'high' for safe automated code changes and blast radius analysis to filter out heuristic false positives. |

Unlike the other tools, the structured answer is not the usual envelope. `text` is a table; `result` is:

```json
{
  "root": "services",
  "folders": [
    {"path": "services/checkout-api", "name": "checkout-api", "kind": "module", "language": "go",
     "files": 18, "symbols": 117, "calls_out": 67, "calls_in": 22, "tested_pct": 61, "risk": "high",
     "owners": [{"name": "A. Rao", "share": 0.41}]}
  ],
  "edges": [
    {"from": "services/checkout-api", "to": "libs", "calls": 42, "exact": 42, "high": 0, "medium": 0, "low": 0}
  ],
  "truncated": false,
  "next_offset": 200,
  "total_folders": 12,
  "min_confidence": "low",
  "index_revision": 1,
  "provenance": {},
  "notes": []
}
```

| Field | Meaning |
|---|---|
| `root` | The folder asked about; `.` is the repository root. |
| `folders[].kind` | `module` when the folder directly holds a `go.mod`, `package.json`, `pyproject.toml`, `Cargo.toml`, `pom.xml`, `build.gradle`, `composer.json` or similar manifest; otherwise `folder`. |
| `folders[].self` | Present and true on the row for the files directly in `path`, whose `name` is `.`. |
| `folders[].calls_out`, `calls_in` | Relationships (calls, instantiations, implements, reads, writes and the like, not plain references) between a file in this folder and a file in another. Relationships inside one folder are not counted. |
| `folders[].tested_pct` | Share of the folder's files that define something and are reached by a relationship from a test file (found by file naming). Absent when the folder has no such file. A floor: tests that reach code through an interface or reflection are not seen. |
| `folders[].risk` | `low`, `medium` or `high`, relative to the folders listed: how much of the cross-folder traffic lands on the folder, weighed against how little of it a test reaches. It orders the list; it is not a quality measure. |
| `folders[].owners` | The names behind the most commits to the folder in the recent history, with their share. Names only. Absent when no history is available. |
| `edges` | One row per ordered pair of folders with relationships between them, strongest first, at most 500. A folder outside `path` is named by its top-level folder. |
| `truncated`, `next_offset` | The list was cut; pass `next_offset` as `offset` for the rest. |

### `policy`

Check the repository's own policy rules (the `policy:` block of `.graphlume.yml`) against the change between two git revisions and the index. Reads only. Answers what [`graphlume policy check`](../CLI.md#policy) answers; the structured result is the JSON that command prints with `--format json`.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `base` | string | no | Git revision to compare from. Default: origin/main, main or master. |
| `head` | string | no | Git revision to compare to. Default HEAD. |

A policy that fails its rules is a finding, not a tool failure: `passed` is false and `is_error` stays false. A repository with no `policy:` block has nothing to check, and the answer says so.

### `arch`

Check the code against the architecture the repository declares in `.graphlume.yml`. Reads only. The structured result is the JSON of `graphlume arch --json`; with no declared architecture it is `{"architecture_declared": false, ...}`.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `crossings` | boolean | no | Also list every component-to-component dependency in the text, not only the violations. The structured result has them either way. |
| `min_confidence` | string | no | Lowest confidence to return: exact, high, medium or low. Default low. Recommended: 'high' for safe automated code changes and blast radius analysis to filter out heuristic false positives. |

### `deps`

The dependencies the project declares in its manifests and how many indexed files import each. The structured result is the JSON of `graphlume deps --json`.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `all` | boolean | no | List every declared dependency in the text, not only the unused ones. The structured result lists every one either way. |

### `licenses`

The licence of each third-party dependency the index declares. It takes no parameters. The structured result is the JSON of `graphlume licenses --json`, the form of that command that writes no file and runs no toolchain (without `--json` it regenerates `THIRD_PARTY.md`, which a tool never does).

Licences are read only from files already on this machine. Nothing is downloaded, no package manager is run, and no code is executed. Sources, in order of trust:

| Source | Where |
|---|---|
| `declared-metadata` | npm: `node_modules/<pkg>/package.json` `license`/`licenses`, then the `license` field of `package-lock.json` / `npm-shrinkwrap.json` (pnpm and yarn lockfiles record none). Python: `*.dist-info/METADATA` (`License-Expression`, `License`, licence classifiers) in a virtualenv inside the repository (`.venv`, `venv`, `env`). Cargo: the crate's `Cargo.toml` `license` in `$CARGO_HOME/registry/src` (the version from `Cargo.lock`). Maven and Gradle: `<licenses>` in the POM in `~/.m2/repository` or the Gradle cache, following up to four parent POMs. |
| `license-file` | `LICENSE*`, `LICENCE*`, `COPYING*` in the Go module cache (`$GOMODCACHE`, else `$GOPATH/pkg/mod`, else `~/go/pkg/mod`; read from the environment and Go's env file, never by running `go`), `vendor/`, `node_modules/<pkg>`, a wheel's licence files, and a crate's directory. |
| `unknown` | None of the above was found; `reason` says what was looked for. |

Licence text is identified by a built-in classifier: an `SPDX-License-Identifier` line first (exact), then canonical templates of MIT, MIT-0, ISC, 0BSD, BSD-2-Clause, BSD-3-Clause and Zlib with the holder and year as wildcards (exact, or high inside a longer file), then title-and-phrase fingerprints of Apache-2.0, MPL-2.0, GPL-2.0, GPL-3.0, LGPL-2.1, LGPL-3.0, AGPL-3.0, EPL-2.0, CC0-1.0 and Unlicense (high, medium or low), then standard licence notices (medium). A name alone is never turned into a licence unless it is an SPDX identifier or an unambiguous licence name; "BSD License" is not.

```json
{"index_revision": 1, "total": 11, "by_ecosystem": {"go": 7, "npm": 4}, "licenses_read": true,
 "summary": {"by_category": {"permissive": 8, "weak_copyleft": 1, "strong_copyleft": 0, "other": 0, "unknown": 2},
             "by_source": {"declared-metadata": 4, "license-file": 5, "unknown": 2},
             "by_confidence": {"exact": 6, "high": 3}},
 "dependencies": [{"name": "golang.org/x/sys", "version": "v0.50.0", "ecosystem": "go", "direct": false,
                   "manifest": "go.mod", "line": 9, "license": "BSD-3-Clause", "source": "license-file",
                   "path": "~/go/pkg/mod/golang.org/x/sys@v0.50.0/LICENSE", "confidence": "exact", "category": "permissive"},
                  {"name": "lodash", "version": "^4.17.21", "ecosystem": "npm", "scope": "runtime", "direct": true,
                   "manifest": "apps/web/package.json", "line": 13, "license": "unknown", "source": "unknown",
                   "category": "unknown", "reason": "not installed under node_modules, and no package-lock.json records its licence"}],
 "elapsed_ms": 41, "budget_spent": false, "note": "..."}
```

`license` is an SPDX expression or `"unknown"`; `declared` carries the metadata's own words when they are not one. `path` is repository-relative inside the repository and `~/`-relative under the home directory. `category` is `permissive`, `weak_copyleft`, `strong_copyleft`, `other` (a valid identifier the report does not place, such as a `LicenseRef-`) or `unknown`; an `OR` takes the least restrictive choice and an `AND` the most. The search is bounded at 20 seconds; anything not reached is `unknown` and `budget_spent` is true. The repository policy has no licence rules, so no row is reported as denied.

### `vulns`

Check the declared dependencies against OSV-format advisories you supply, and say what the index shows about their use. Nothing is downloaded and no advisories are built in. The structured result is the JSON of `graphlume vulns --json`; with no advisories it is `{"checked": false, ...}` and says where to get them.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `advisories` | array | no | OSV-format advisory files, or directories of them, inside the repository, e.g. ['security/osv.json']. Absolute paths, paths that leave the repository and symbolic links out of it are refused. |
| `ignore` | array | no | Advisory ids or aliases to leave out. The report lists what was ignored. |
| `all` | boolean | no | List the not-affected comparisons and every importing file in the text. |
| `limit` | integer | no | Importing files and symbol references shown per finding in the text. Default 5. |

The command's `--fail-on` gate is not offered: the exit status of a tool is not a pipeline's.

### `sbom`

The CycloneDX 1.5 SBOM of the declared dependencies. The structured result is the whole document, the same bytes `graphlume sbom` writes; the text lists the first components. A document over 8 MB is refused with a pointer to the command line.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `name` | string | no | Name of the project the SBOM describes. Default: the root manifest's own module or package name. |

### `tour`

A reading order for the codebase: entry points, then what each calls. The structured result is the JSON of `graphlume tour --json`.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `limit` | integer | no | Most entry points to show. Default 8, at most 50. |
| `depth` | integer | no | How many calls deep to follow from each entry point. Default 2, at most 6. |
| `width` | integer | no | Most callees to follow from each symbol. Default 5, at most 20. |
| `min_confidence` | string | no | Lowest confidence to return: exact, high, medium or low. Default high for this tool. |

### `adr`

The architecture decision records the repository keeps, or the ones matching a number or part of a title. The structured result is the JSON of `graphlume arch adr --json`: `{"adrs": [...]}`. A query that matches nothing is an answer, an empty list, not a failure.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `query` | string | no | An ADR number or part of its title or path. Default: list every record. |

### `lessons`

What the recorded task lessons say the context keeps missing. Reads the local `.graphlume/lessons.jsonl` that `graphlume lesson add` writes; this tool only reads it. The structured result is the JSON of `graphlume lesson report --json`.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `top` | integer | no | How many names to list in each ranking. Default 10, at most 100. |

### `events`

The event bus topics the code publishes to or consumes from (Kafka, RabbitMQ, SQS, SNS, Pub/Sub, JMS), each with the function, file, line and confidence of every producing and consuming site. Reads only. `graphlume events --json` prints an array; a tool's structured result is an object, so the array is `topics`, each element unchanged:

```json
{"index_revision": 1, "total": 2, "matched": 2,
 "topics": [{"symbol_id": 9, "broker": "kafka", "topic": "orders.created", "fqn": "event:kafka:orders.created",
             "producers": [{"symbol_id": 4, "symbol_name": "Publish", "symbol_fqn": "order.Publish",
                            "file_path": "order/publish.go", "line": 12, "confidence": "high"}],
             "consumers": []}],
 "note": "..."}
```

| Parameter | Type | Required | Description |
|---|---|---|---|
| `topic` | string | no | Keep only topics whose name or qualified name contains this text. Case-insensitive. |
| `broker` | string | no | Keep only one broker, e.g. kafka, rabbitmq, sqs, sns, pubsub. Case-insensitive. |

A topic with no producer here is published by another service or by nothing; a topic named only in configuration, or built at run time, is not listed.

All ten run the command of the same name inside the server process. They need the report runner the command line supplies, so they exist wherever the CLI serves tools (`serve`, `serve --http`, `tool`, `ui`, `studio`) and not in a server embedded without it. Their text is the command's own text output (`tour` and `events` lay theirs out from the JSON, so the index is read once), framed and cut to 20 KB; their structured result is not the usual envelope but the command's JSON, unchanged.

### `stats`

Reports what the calling session has used so far. It takes no arguments and
does not count the call that asks.

This is a real answer, from an MCP session over stdio that had made seven
calls (the remaining notes are elided):

```text
=== SYSTEM METADATA ===
This session has made 7 tool call(s) before this one, 2 of which failed. Their answers totalled 32603 bytes of text, an estimated 8153 tokens.
index_revision: 1  status: ready
note: This is usage, not a saving. GraphLume has no measurement that supports a token saving, so none is reported and none should be inferred from these figures.
...
session: transport=mcp calls=7 failed=2 answer_bytes=32603 est_answer_tokens=8153 structured_bytes=40621 time_ms=136.8 since=2026-11-12T09:35:35Z
tool_definitions: profile=full tools=42 schema_bytes=50991 est_tokens_per_request=12766
usage: callers calls=3 failed=1 answer_bytes=8138 est_answer_tokens=2035 time_ms=14.4 slowest_ms=8.0
usage: (unregistered) calls=1 failed=1 answer_bytes=0 est_answer_tokens=0 time_ms=0.0 slowest_ms=0.0
usage: overview calls=1 failed=0 answer_bytes=958 est_answer_tokens=240 time_ms=13.0 slowest_ms=13.0
usage: context calls=1 failed=0 answer_bytes=22977 est_answer_tokens=5745 time_ms=108.0 slowest_ms=108.0
usage: search calls=1 failed=0 answer_bytes=530 est_answer_tokens=133 time_ms=1.4 slowest_ms=1.4
cited_files: files=16 bytes=283106 est_tokens=70777
```

The same figures are in the structured result under `session_stats`, where
`token_basis` is `"estimated"` and `cited_files.meaning` states what that
figure is.

| Line | What it is | How it is known |
|---|---|---|
| `session` | Calls, failed calls, the size of the answers' text, the size of their structured copy, and time spent answering | Counted by the server as each call finishes |
| `usage` | The same, per tool. A name the profile does not expose is counted under `(unregistered)` | Counted |
| `est_answer_tokens` | The answer size in tokens | Estimated: bytes / 4, rounded up, per answer. No tokenizer is run |
| `tool_definitions` | What the exposed tools' names, descriptions and input schemas cost on every model request. The same number `graphlume tool list` prints | Bytes counted, tokens estimated. The server cannot see how many requests the client's model made, so there is no session total |
| `cited_files` | The size, when indexed, of the distinct indexed files the answers named. Each file once per session | Counted, from the index |

**No saving is reported.** The one agent benchmark run so far found no token
advantage on simple lookups ([benchmarking](../BENCHMARKING.md#5-agent-benchmark)),
so there is no measured saving to print. `cited_files` is the only comparison,
and it is an upper bound on what reading those files whole would have cost: an
agent with search would not necessarily have read them, and could have read
parts.

What "this session" covers depends on how the call arrived, and the answer says
which:

| Surface | The session is |
|---|---|
| MCP (stdio or HTTP) | One client connection. Counters are discarded when it ends |
| REST | Every REST call since the server started, from every caller |
| `graphlume tool call` | That one command, so the report is always empty. Use [`graphlume stats`](../CLI.md#stats) on an audit log instead |

The tool is on the `full` profile only. Every exposed tool's definition is
charged to every model request, and a tool that reports cost should not raise
it for sessions that never ask (it costs about 130 tokens). The counting runs
on every profile. The server keeps counters for its 64 most recently active
sessions, one small block per tool, and never a list of calls.

Setup for each client: [agent integration](../agent-integration.md).

### `estate`

Ask across the repositories of a multi-repository estate, the members named in its estate file. It answers what [`graphlume system`](../CLI.md#system) answers on the command line, for an agent.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `action` | string | yes | `links`: HTTP route links between members (an outbound call in one, the route it reaches in another), tables more than one member touches, and Go function and method calls into a module another member defines, with the version warnings for each importer and provider. `callers`: who calls `symbol`, in its own member and in the others. `impact`: what depends on `symbol` across members. |
| `symbol` | string | no | For `callers` and `impact`: `member:name`, e.g. `lib:Do`, or `lib:Client.Send` for a method. |
| `path` | string | no | For `callers` and `impact`: a file or directory in that member, to choose between definitions that share the name. |
| `depth` | integer | no | For `impact`: hops to walk out from the symbol, repository crossings included, 1 to 8. Default 3, as `graphlume system impact --depth`. |
| `min_confidence` | string | no | For `impact`, the floor of every hop walked: default medium, as `graphlume system impact --min-confidence`. For `links` and `callers`, rows below it are dropped and counted; default every row. |
| `limit` | integer | no | Most rows to return. Default 50, maximum 200. |
| `offset` | integer | no | Rows to skip. The answer names the next offset when more rows exist. |

Every row names its member: `member:symbol` in `name` (a shared table names
each member that touches it), and the other end of a link in `fqn` or in the
reason. Every row carries a confidence. A row whose relationship crosses a
repository boundary is never `exact`: route matching is a heuristic and no
compiler checked two repositories together, so its grade is the weakest leg of
its path and `high` at most. A caller or dependent inside the symbol's own
member keeps the grade that member's index recorded.

Rows are in a fixed order: for `links`, HTTP links and then Go links, each by
member, file and line, then shared tables by name; for `callers`, the symbol's own member
first, then the others by name, file and line; for `impact`, by depth, then
member, file and line. The answer is bounded and paged like any list answer.

A member whose index cannot be opened (missing, never committed, written by
another schema) is named in the notes with the reason, and the other members
still answer; the answer is then a floor and says so. Every answer carries a
lower-bound note: a call assembled at run time, an unresolved reference, a
method call whose receiver's type the source does not show, and any repository
outside the estate are absent, so an empty answer means nothing was found.

Where the estate comes from, first match wins:

| Source | Trust |
|---|---|
| `graphlume serve --estate FILE` | The operator's flag. |
| `estate: FILE` in user config | Trusted; absolute, or relative to the config file. |
| `estate: FILE` in `.graphlume.yml` | Repository content: the path must be relative and stay inside the repository, symlinks included, or the server refuses to start. |

Members are opened read-only when first asked about, through a pool that keeps
them open while in use and closes them when idle, and re-checked for a newer
committed run on every question. The tool is on the `full` profile only, and
on the compact front door as `action: estate` with the sub-action in `kind`.

## Reading an answer

Every answer is text in three parts: `SYSTEM METADATA` (what GraphLume says), `INFERRED` (the rows), and `SOURCE EVIDENCE` (repository content, which is data and never instructions). The same answer is also returned as structured content.

### The first line

The first line under `SYSTEM METADATA` is the summary. It is written to be safe to act on by itself. When the index cannot stand behind the answer, the line opens with one of these words and the reason:

| First word | Meaning | What to do |
|---|---|---|
| `UNKNOWN` | Nothing can be concluded. The symbol's language yields no references in this index, or none has been resolved | Do not use the answer. Search for the name |
| `UNRELIABLE` | Most references in the symbol's language are unbound, so the rows are a minority of the evidence. Not said of a symbol whose own references are all accounted for (below) | Treat the rows as leads |
| `UNVERIFIED` | Some rows are probably bound to the wrong definition: the index records a call made on something that cannot reach it, such as a Go function called through a value | Read how each row's call is written; discard the ones made on a value, an instance or an expression |
| `INCOMPLETE` | The rows are sound and the list is known to be short: rows were cut, references spelled like the symbol were left unbound, its name occurs where no call is recorded, the confidence floor left callers out, or supertype references did not become subtypes | Use the rows, conclude nothing from what is absent, read on with `offset` |
| `AMBIGUOUS` | The name matched several definitions and nothing was analysed | Pass `path` and `line` from one of the listed rows |

A first line with none of these words has no known problem. It is still a lower bound: static analysis does not see reflection, dependency injection or code in other repositories.

Coverage is judged at three widths and the narrowest evidence decides. Of the references spelled like the symbol, how many are bound to it is the narrowest; how the symbol's language resolved is wider; the whole run is the widest, used only when the language is unknown. When every reference spelled like the name is bound (none ambiguous, none unresolved, none pending, none bound from outside a function that no caller list records) and the text check found no other place the name occurs, the language-wide binding rate does not make the answer `UNRELIABLE`. It is stated in a note instead, beside the symbol's own count: koa's `isStream` has five callers, exactly right, and every reference spelled `isStream` is bound to it, while 59% of the JavaScript references in the repository are bound. When the symbol's own accounting is not complete, the language's rate stands. The tools that have the symbol's own accounting are `callers`, `references`, `impact`, `tests`, `plan`, `owners` and `rename`. `callees`, `implementations`, `trace` and the whole-repository reports have none for a name, and are graded on the language.

Coverage is judged for the language of the symbol asked about, not for the whole repository. A Kotlin symbol in a repository with a few Java files is described by the Kotlin files. When a language yields no references at all the answer says so in these words: "GraphLume does not extract calls from Kotlin; nothing about callers can be concluded." That is said of the language when the index holds at least 20 definitions in it and no reference from any of them. With fewer, the answer says only that the index holds no reference from those files; nothing about callers follows either way. `graphlume resolve` is named as a remedy only when references are still pending.

`coverage` lists the same figures for each language in the index.

An unknown risk is still a risk cut short: when the walk stopped at a depth limit or another bound, the first line says so after "Risk unknown", as it does after every other level.

### A function passed as a value

A function handed to something by name (`.then(handleResponse)`, `register("x", handler)`, a table of callbacks) is used, and the use is not a call, so no edge records it. The graph cannot see it. The text can, because the name is still there.

`impact`, `callers`, `tests` and `plan` read the stored text for the target, and for `impact`, `tests` and `plan` also for up to four symbols on the reported path that have two or fewer recorded callers. They count the places where the name occurs as an identifier in a SOURCE file of a language that can reference it, and that are none of these: a definition of it, inside a definition of it, an `import`, `use`, `require` or `#include` statement (every line of one that spans several, and a CommonJS `const x = require(...)`), a declaration or prototype of the function itself (`def`, `fn`, `func`, `function`, and `void f(int);` in C, C++ and Java, including a header that pairs with the file defining it), inside a string, a comment-only line, or the line of any reference the index recorded. Only functions and methods are checked, and a constructor never is: its name is its type's.

"A language that can reference it" is the resolver's rule for which languages can call which: the language of the file that defines the name, and the members of its family (JavaScript, TypeScript, TSX, JSX and Vue; C, C++ and Objective-C; Java, Kotlin, Scala and Groovy; and the others in `languageFamilies` in `internal/resolve/infer/calltraits.go`, which `internal/registry/languages.go` mirrors). A Go function is looked for in Go files and nowhere else. Markdown, reStructuredText, YAML, JSON, TOML and every other prose, text-only, configuration or data format is never read, so a CHANGELOG, a README or a workflow file that says the name is not a use of it. Without this, cobra's `CompErrorln` (one caller, correct) opened `INCOMPLETE` because a documentation page contains it.

When there are such places the first line leads `INCOMPLETE` and says so:

```text
INCOMPLETE — respond: its name appears in at least 9 other place(s) that are not recorded as calls: it may be passed as a value; handleResponse, on the path to it, is named in 1 other place(s) not recorded as calls (it may be passed as a value), so what reaches handleResponse is not listed. respond (lib/application.js:288): 1 direct, 0 indirect. Risk low or higher (...)
```

Three things follow. The verdict cannot be `strong`. Certainty is never `high`. And no tool says "no test reaches this" as a fact: `tests` says whether one does is `NOT KNOWN`, and `impact` adds no points for missing tests.

This is a doubt and never a finding. The same name may belong to an unrelated symbol, or to a property (`ctx.respond`). A place counted is not a place proven to pass the function. The check reads the text of at most a dozen source files per name, so a name found in more files than that is reported "at least". A name with more recorded references than 5,000 is not checked.

### Callers the confidence floor left out

`impact` follows relationships at `medium` or better unless `min_confidence=low` is passed; `callers` follows every tier. When the floor kept symbols out, the first line says how many and how to bring them back:

```text
readJsonValue (moshi/src/main/java/com/squareup/moshi/JsonReader.kt:446): 6 direct, 0 indirect; 1 more symbol(s) at low confidence are excluded; pass min_confidence=low. Risk unknown: ...
```

When the number of direct callers shown is zero only because of the floor, the answer leads `INCOMPLETE` and says so. `impact` counts distinct calling symbols and `callers` counts call sites, so the second can be larger for the same symbol even with no floor: `callers` also lists calls at low confidence and calls the symbol makes to itself, which `impact` leaves out. A note gives `impact`'s own symbol and site counts when they differ, and does not quote a number for `callers`.

When a reference is "ambiguous" the answer says why in terms that are true of the name. "Several definitions share the name and none was chosen" is said only when the index does not hold exactly one definition of it; for a name with a single definition (the stubs of Python's `@overload` are folded into it) the answer says that the index holds one and that the evidence at those calls did not bind them to it.

### The verdict

An answer whose rows carry a confidence tier has a `verdict` line, in structured form `verdict.status`. One function computes it from three inputs: coverage, the tiers of the rows, and whether rows were cut. It can never be stronger than the coverage allows.

| Status | Meaning |
|---|---|
| `proven` | Every row is compiler-proven, coverage is good, nothing was cut |
| `strong` | No row is below `high`, coverage is good, nothing was cut |
| `incomplete` | Rows are `high` or better, and the list is known to be short |
| `mixed` | Rows differ in strength, or some are probably bound to the wrong definition |
| `weak` | Every row is inferred, most of them weakly |
| `unreliable` | Coverage is low or unknown. The rows are not an answer, whatever their tiers |
| `empty` | Nothing was returned, which is not evidence of nothing |

`proven` and `strong` are the only two that say the rows may be acted on. Neither is reachable when the first line carries one of the words above.

### Other fields

| Field | Text line | Meaning |
|---|---|---|
| `reliability.level` | first word of the summary | `good`, `incomplete`, `unverified`, `low` or `none`. Present on relationship answers |
| `page` | `truncated:` | `offset`, `limit`, `total`, `returned`, `next_offset` for a paged list |
| `outcome` | `outcome:` | `ambiguous_name` when the answer is a list of definitions to choose from; `kind_not_indexed` when the name has definitions and none of the kind the tool is about (no rows). Absent otherwise |
| `truncated`, `truncated_reason` | `truncated:` | Rows or traversal were cut, and why |
| `omitted` | `omitted:` | What the cut rows were, by kind and confidence |
| `notes` | `note:` | Caveats, most important first |

### Risk

`impact`, `plan` and `changes` report a risk level: `low`, `moderate`, `high` or `unknown`.

`unknown` is reported in place of `low` when the callers could not be determined: references spelled like the symbol exist and were not bound to it, or its language yields no references. The answer then prints the counts and no score. A score computed without the callers is not a low score.

`moderate` and `high` stay as scored when callers are missing, because a floor survives missing evidence. The summary then reads "Risk moderate or higher".

Any level reads "or higher", with the reason, when the analysis is known to be a floor for another cause: the walk was cut at its depth or node limit ("Risk low or higher (score 6; the analysis was cut at depth 3)"), or the name occurs where no call is recorded (see above). A cut analysis does not keep a bare `low`.

`tests` follows the same rule. When no test is found and the calls to the symbol could not be bound, it says whether the symbol is tested is unknown. It does not say the symbol is untested. It says the same, as `NOT KNOWN`, when the name of the target or of a symbol on its path occurs where no call is recorded.

## Choosing between definitions

A tool that takes `name` or `symbol` answers for one definition. When several share the name it does not pick one. It returns `outcome: ambiguous_name` and one row per definition, with the kind, the owning type, the parameter count, the lines and the first line of the declaration. Each row ends with the arguments that select it:

```text
- candidate fromJson  gson/src/main/java/com/google/gson/Gson.java:850  (method, in class Gson, 2 parameter(s), lines 850-852; select with path=gson/src/main/java/com/google/gson/Gson.java line=850)
```

This is an ordinary answer, not an error. `graphlume tool call` exits 0.

| Argument | On | Selects |
|---|---|---|
| `path` | every tool that takes `name` or `symbol` | The file. Matched as the exact file first, then as whole path segments (a directory or a file name), then as a substring |
| `line` | the same tools | The definition that starts on that line, or the innermost one that contains it |
| `from_path`, `from_line`, `to_path`, `to_line` | `trace` | The same, for each end |

`path` and `line` together select any definition. A qualified name does not: it is the file and the identifier, so overloads in one file share it.

A name may also be written as an owner and a member: `Guard.AgainstNull`, `Guard::AgainstNull`, `Mailer#deliver`. It is tried only when the name is not itself a definition and not a qualified name, and it selects the member whose qualified name ends in the owner and the member. Leading segments the index does not hold, such as a namespace, are dropped one at a time (`Serilog.Guard.AgainstNull` finds `Guard.AgainstNull`). An owner that does not declare the member finds nothing.

A tool that is about one kind of thing prefers that kind when several definitions remain. `implementations` prefers a type over a method of the same name. `callers`, `callees` and `trace` prefer a function or method. The answer says when it chose this way. `definition` shows every matching definition and `symbol` lists them all.

When the name has definitions and none is of that kind, the tool does not answer for the wrong one. The first line says so, with `outcome: kind_not_indexed` and no rows:

```text
UNKNOWN — no type named "BenchmarkReporter" is indexed, so implementations answered for nothing. The only definition(s) of that name: a method at src/reporter.cc:33, and none has an extends or implements relationship recorded.
```

A zero there would be a statement about a constructor, read as one about the type. A `path` or `line` is the caller choosing and is honoured. `implementations` still answers for a method that has an extends or implements relationship recorded (an override is one), and `callers` still answers for a class, where it counts what constructs it.

### Type hierarchies

`implementations` lists every type that reaches the target through `extends` and `implements` edges, direct ones first, then by depth and the strength of the path. Each type appears once, at its shallowest depth, and a row below depth 1 says which type it names and that its confidence is the weakest link of the path. The walk is bounded at depth 8 and 20,000 types; a floor is reported "at least". `direct_only=true` lists the direct subtypes alone and still counts the rest:

```text
HandlerInterface (src/Monolog/Handler/HandlerInterface.php:21): 69 implementing or extending it (4 directly, 65 through other types; direct_only=true lists the direct ones alone), 0 it implements or extends.
```

A row is always a type. An anonymous class expression, or a class declared inside a function body, is attributed by the extractor to the function that contains it, so its extends edge starts at a function (hono's `buildInitParams` holds a class that extends `RegExpRouter`). Such an edge is not a subtype row. It is counted on the first line, in one clause, and the enclosing functions are named in a note:

```text
Router (src/router.ts:29): 6 implementing or extending it, all directly; 1 anonymous or local type(s) inside functions also extend it (not rows: they are not types), 0 it implements or extends.
note: 1 anonymous or local type(s) declared inside functions extend or implement Router: inside buildInitParams (src/router/reg-exp-router/prepared-router.ts:98). They are subtypes with no name of their own, so they are counted here and are not rows of the list.
```

This does not lead `INCOMPLETE` by itself: the edges are recorded and counted. The anonymous expressions the index recorded no edge for are the text check below.

Supertypes are the target's direct ones. The index holds supertype references that did not become subtypes, and when it does the first line leads `INCOMPLETE` with their count, so the verdict cannot be `strong`:

| What the first line counts | Why the type is missing |
|---|---|
| Supertype references that bound to the target and became no edge | The extending type has no definition in the index: `impl Matcher for ExternalType` |
| Supertype references that are ambiguous, unresolved or pending | The spelling is all that is known |
| Lines in a source file that declare ANOTHER named type with the target in its supertype list, and are recorded as no supertype reference | The extractor did not read the declaration, such as `class URL(click.ParamType[str])` |
| Lines that build an anonymous class or object with the target as a supertype (`new Target() {`, Kotlin's `object : Target()`, `new class extends Target`), counted apart | An anonymous type has no name, so a list of types cannot carry it. The first line says "N anonymous class or object expression(s) name it as a supertype and are not listed" |

The last two are a text check of the kind described under "A function passed as a value", and a doubt, not a finding. It reads source files of a compatible language only, and a line counts only when it reads as a declaration in the form its language writes it: `extends` and `implements` in Java, TypeScript and PHP, `:` in Kotlin, C#, C++ and Swift, `extends` and `with` in Scala, `impl Target for X` and `trait X: Target` in Rust, the parenthesised bases of a Python `class`, `<` and `include` in Ruby. The target must be in the supertype list as a whole name at its top level. So a generic constraint (`<T extends Target>`, `where T : Target`), a wildcard bound (`? extends Target`), a type argument of another supertype (`extends Base<Target>`), a constructor or method parameter, a forward declaration (`class Target;`), the target's own declaration and every line of documentation are not counted. Go has no pattern, because its interfaces are satisfied without naming them and its embedded types are references. Types that reach the target only through a type whose own supertype reference is unbound are not counted. The count of supertype references that did not bind comes from the index and not from the text, and is unaffected.

## Paging

`references`, `callers`, `callees`, `implementations`, `rename`, `impact`, `tests`, `plan`, `endpoints`, `tables` and `config` (when it lists keys) take `limit` and `offset`.

| Argument | Default | Maximum |
|---|---|---|
| `limit` | 50 | 200 |
| `offset` | 0 | none |

An answer that does not carry every row says how many exist and what to pass next:

```text
truncated: showing rows 1-50 of 195 caller(s), so 145 are not shown here. Pass offset=50 to read on (limit accepts up to 200)
```

An answer is also limited to 24 KB, so a page can hold fewer rows than `limit`. `page.returned` and `page.next_offset` say how many it holds. `search`, `deadcode`, `hotspots` and `owners` take `limit` with their own defaults and no `offset`.

## What an answer will not state as fact

**Whether a definition is exported.** The index sets its exported flag from the first letter of the name. That is the language's own rule in the languages below and a guess everywhere else. Outside them an answer says the export status is unknown, `impact` adds no points for it either way, and `deadcode` grades the candidate `possible`.

| Language | Why the flag is trusted |
|---|---|
| `go` | An upper-case first letter is the language's own export rule |

The table is `exportedFlagTrusted` in `internal/capability/exported.go`. `TestExportedTableIsDocumented` keeps this copy in step with it.

**How often a low-confidence relationship is right.** It varies by language and by project. `impact` leaves `low` relationships out by default and counts what it left out, but quotes no precision figure. [Accuracy](../accuracy.md) says how precision is measured, and `graphlume accuracy` measures it on your repository.

**That a row is a call to the definition.** A qualified call is bound through what its qualifier names: an imported package, a module, a type. A call on a value whose type nothing states is bound on the member name alone, and only when one type declares a member of that name. The index records which it was (`symbol_references.receiver_kind`). Each caller row says how the call is written when it carries a qualifier (`written as errors.New`), and a note counts the calls made on a value or an expression, which are the rows most likely to be a call to something else.

## Names

Every tool is `<verb>_<noun>`, with one name and no aliases; see
[naming conventions](naming-conventions.md).

## Prompts and instructions

The server sends under 900 bytes of session instructions and offers three
prompts: `plan_refactor` (argument `symbol`), `changes` (argument
`symbols`) and `get_oriented`. See [protocols](../guides/protocols.md#mcp).
