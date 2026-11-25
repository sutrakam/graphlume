# The GraphLume portal: a guide

`graphlume ui` serves a local web portal over your GraphLume index. It is a
reading tool. It shows what the index holds, how sure the index is about each
fact, and what the index cannot see. It never edits your code or your index.

The portal asks the index the same questions an agent does, through the same
tools. It has no data routes of its own. Every figure on every page is a call
to the tool endpoint under `/api/v1`, so a page and an agent cannot disagree.

Contents

- [Start the portal](#start-the-portal)
- [The layout](#the-layout)
- [How to read an answer](#how-to-read-an-answer)
- [Pages](#pages)
  - [Home](#home)
  - [Architecture](#architecture): Visual Graph, Path Explorer, Deep Explorer
  - [Change and risk](#change-and-risk): PR Blast Radius, Refactor Planner
  - [Surface inventory](#surface-inventory): HTTP Endpoints, Database Tables, Event Bus and Topics
  - [Health and quality](#health-and-quality): Blindspots, Index Health, Hotspots and Churn
  - [Agents](#agents): Tool Activity, SQL Query, Agents and Setup
  - [The console](#the-console)
  - [Search results](#search-results)
  - [A symbol](#a-symbol), [a file](#a-file), [a route](#a-route),
    [a table](#a-table)
- [How the portal is designed to behave](#how-the-portal-is-designed-to-behave)
- [Troubleshooting](#troubleshooting)

---

## Start the portal

The portal reads an existing index, so build one first:

```sh
graphlume init --index
graphlume ui
```

`init --index` builds the index and resolves calls between files (for Go, with
the compiler's type checker), which is what the Map and every caller list read.

`graphlume ui` prints the address and stays in the foreground. Press Ctrl-C to
stop it.

```text
GraphLume portal: http://127.0.0.1:7777/
  index      /work/payment-platform/.graphlume/index.db (opened read-only; this portal never writes)
  binding    loopback only — not reachable from other machines.
             Requests whose Host header is not a loopback address are refused, so a
             page on the internet cannot use your browser to read this index.

Press Ctrl-C to stop.
```

Open the address in a browser. Everything is served from the binary: there is
no npm, no build step and no CDN, so it works offline.

| Flag | Effect |
|---|---|
| `--port N` | Port to listen on. `0` picks a free one. Default `7777`. |
| `--host ADDR` | Loopback address to bind: `127.0.0.1`, `::1` or `localhost`. Any other address is refused. |
| `--print-url` | Print the URL that would be served and exit, without listening. |
| `--project PATH` | Global flag. The repository whose index to serve. |
| `--json` | Global flag. Print the URL, address and index path as JSON instead of the banner. |

---

## The layout

- **Header.** The GraphLume logo, the project this index describes, the search
  box, **Refresh** (reloads the index state; to update the index itself, run
  `graphlume index` or keep `graphlume watch` running), **Doctor** (shows the
  index state in the console), the index chip (revision, the commit it was built
  from, and "HEAD moved" when the checkout has moved since), keyboard shortcuts
  (`?`), the theme toggle and **Console**.
- **Search and jump.** The search box finds functions, types, files, routes,
  tables and text, and lists the portal's own pages. Press `⌘K` (`Ctrl K`) or `/`
  to jump to it, the arrow keys to choose and Enter to open.
- **Sidebar.** Fourteen pages in five groups: Architecture, Change and risk,
  Surface inventory, Health and quality, and Agents.
- **Pages of their own.** Every function, type, file, route and table has a
  page, reached from search or from a link on another page.

| Address | Page |
|---|---|
| `#/` | Home |
| `#/map?v=&at=&sym=&path=&line=` | Visual Graph. `v` is the view; `at` is a directory (ending in `/`) or a file. |
| `#/path?from=&to=&from_path=&to_path=` | Path Explorer |
| `#/explore` | Deep Explorer |
| `#/impact?base=&head=` | PR Blast Radius |
| `#/change?base=&symbol=` | Refactor Planner |
| `#/endpoints`, `#/tables`, `#/events` | HTTP Endpoints, Database Tables, Event Bus and Topics |
| `#/blindspots`, `#/doctor`, `#/health` | Blindspots, Index Health, Hotspots and Churn |
| `#/feed`, `#/sql`, `#/agents` | Tool Activity, SQL Query, Agents and Setup |
| `#/search?q=` | Search results |
| `#/s/<name>?path=&tab=` | A symbol |
| `#/f/<path>` | A file |
| `#/r/<signature>` | A route |
| `#/t/<name>` | A table |

---

## How to read an answer

Every relationship on every page is marked with how it was established. The
mark tells you what to do with it.

| Mark | Meaning |
|---|---|
| proven | A compiler proved it. Safe to act on without checking. |
| very likely | Bound from the syntax tree and imports. It is rarely wrong. |
| uncertain | One of several candidates. Check it before relying on it. |
| a guess | A name match only. Do not act on it alone. |

Beside each answer there are two boxes.

- **What this answer cannot see.** The gaps the index reported for this answer,
  for example calls into libraries. An empty list still means nothing was
  found, never that nothing exists. Notes beyond the first are folded behind
  "Show N more notes".
- **Ask your agent the same thing.** The exact `graphlume tool call ...`
  command that returns this answer, with a Copy button. Run it in a shell, or
  have an agent call the same tool.

Lists of relationships have an "Everything" and a "Very likely or better"
switch, so you can hide the weaker rows.

---

## Pages

### Home

The first page asks "What do you want to know about <project>?", with **Search
the code** and **Plan a change**. Below that: questions that open the Visual
Graph set up to answer them, the state of this index, how to read an answer, and
links that start from a task.

### Architecture

**Visual Graph** draws the index as a picture. Every box and line is a tool
answer, and the **Agent** tab beside it lists those calls as commands an agent
can run for the same answer. The `graph` tool returns at most 200 rows, so each
level is aggregated to what is on screen, and the note under the question says
when something was cut.

| View | Question it answers | Drawn from |
|---|---|---|
| Map | How is the code wired together? Directories, then files, then the functions in one file. | `graph` |
| Pipeline, Radial, Cluster, Sequence | The same relationships, laid out by stage, by distance, by group and in call order | `graph` |
| Impact | What could break if I change this? Rings by distance, each box bordered by the weakest link on its path. | `impact` |
| Matrix | Which parts depend on which? | `graph` |
| Risk | Where does change concentrate? Area is size, colour is the hotspot score. | `hotspots`, `busfactor` |
| Flow | What does an entry point call, layer by layer? | `endpoints`, `graph` |
| Communities | Which files belong together? | `communities` |
| Trace | How does one symbol reach another? | `trace` |

Lines are coloured and dashed by how sure the index is: solid for proven and
very likely, dashed for uncertain, dotted for a guess. The confidence buttons set
the floor for every view, and the depth buttons cap Impact, Flow and unfolding.
On a dense level some boxes can overlap; select a box to read its lines, or
use the lists on the other pages, which are unaffected. The panel on the right
reads the selection: **Evidence**, **Source**, **Rename** (a dry-run plan, its
reach and its tests; it writes nothing) and **Agent**. Press `?` for every
shortcut.

**Path Explorer** asks the `trace` tool how one symbol reaches another: the
shortest routes, hop by hop, each with its file, line and confidence. When a
name matches several definitions, it lists them with their files and lines;
open the page again with `from_path` (or `to_path`) in the address to choose one.

**Deep Explorer** lists the routes the code exposes, with the function that
answers each, and the tables it uses, with how many statements read and write
each one. Every row leads to its own page.

### Change and risk

**PR Blast Radius** reviews a commit range (default `HEAD~1..HEAD`; enter any
branch or commit): how many symbols it changed, how much code depends on them,
the tests that reach them and the highest risk, followed by every row with why
it is listed. It ends with the `graphlume impact` and `graphlume report pr` commands
that puts the same analysis on a pull request.

**Refactor Planner** has two forms. **Review a branch** reviews everything from a
branch or commit up to the current HEAD. **Plan a change to one thing** lists the
edits in order (verify the baseline, modify the target, update each caller,
verify the tests) and the tests to run first, each with how it reaches the target.

### Surface inventory

- **HTTP Endpoints.** Every route the index found: method, path, the function
  that answers it and where it is declared.
- **Database Tables.** Every table the code reads or writes, with the number of
  statements that read and write it, and whether it is declared in this
  repository. **Inspect** opens the table's page.
- **Event Bus and Topics.** Every topic on Kafka, RabbitMQ, SQS, SNS, Pub/Sub or
  JMS, with the function, file and line of each producer and consumer.

### Health and quality

- **Blindspots.** Where the index cannot see: calls it could not bind, critical
  code no test reaches, routes without a handler, tables nothing uses, config
  read but never set, and files it could not parse, each with what fixes it.
- **Index Health.** Files, symbols and relationships; the commit the index was
  built from and whether HEAD has moved since; how complete its references are,
  per language. The environment checks (runtime, SQLite, the graph's invariants)
  need a shell: the page gives the `graphlume doctor --verify-index` command.
- **Hotspots and Churn.** How complete the index is, files that are both large
  and changed often (this needs git history), definitions that look unused, and
  what the index could not see.

### Agents

- **Tool Activity.** Every tool call the portal's server has answered since it
  started: calls, failures, average and slowest time, and answer sizes in
  estimated tokens per tool, plus what the tool schema costs on every request.
- **SQL Query.** A read-only SQL query against the index's tables (symbols,
  edges, files, runs), through the guarded `graph` tool.
- **Agents and Setup.** Four ways in, each with a Copy button: agents on this
  machine (`graphlume install --rules`), agents that need a URL
  (`graphlume serve --http`), anything that calls HTTP, and a shell
  (`graphlume tool call`). Then what each tool costs in context.

### The console

**Console** opens a drawer at the bottom of the page that answers short commands
through the same tools: `status`, `doctor`, `endpoints`, `tables`, `communities`,
`hotspots`, `deadcode`, `busfactor`, `blindspots`, `symbol <name>`,
`explore <name>`, `callers <name>`, `callees <name>`, `impact <name>`,
`outline <path>`, `context <task>`, `query <SQL>`, `refresh` and `clear`. Type
`help` for the list. Like the rest of the portal, it reads and never writes.

### Search results


The header search box opens `#/search?q=`. It lists matching definitions, files,
routes and tables, and files whose text matches. A text match says the words
appear. It is not a relationship.

### A symbol


One page per function, method or type. It opens with a sentence that sums up
the symbol: how many places use it directly, what a change can reach, and the
risk.

When the index cannot vouch for those numbers, the sentence says so in its
wording ("at least 45 places", "Risk is moderate or higher") and one line under
it says why in plain words, for example "Not every use is recorded, so these
are the least the numbers could be." **Why** unfolds the index's own
explanation, and each tab's "What this answer cannot see" box lists every
reason in full.

The tabs, with their counts:

- **Callers.** What calls it. Each row is a call site.
- **Calls.** What it calls, inside this repository.
- **What could break.** Everything a change to it can reach, nearest first.
- **Tests.** The tests that reach it. Run the ones found through a call first.
- **Type hierarchy.** What implements or extends it, and what it extends.



### A file


The first tab shows how the file opens, with the bodies of its functions
removed. Names defined in the file are links to their pages. The other tabs are
Changes with (files usually committed with this one), Looks unused and Routes.
The file page links to "Plan a change to one definition".

### A route


The sentence at the top names the function that answers the route, the tables
it reaches and the tests that reach it. The tabs are What it calls, Tables it
reaches and Tests. Settings the route reads and events it sends are not on this
page. The page shows `graphlume config` and `graphlume events`, which print
them for the whole repository.

### A table


The tabs are Written by, Read by, Routes that reach it and Declared in. Check
what writes to a table first, then what reads it, before you change a column.
A statement put together while the program runs may be missing from both lists.

---

## How the portal is designed to behave

- **Read-only.** The portal opens the index read-only. The tools it calls are
  read-only. It serves no route that writes. To refresh the index, run
  `graphlume index` or keep `graphlume watch` running.
- **Loopback only.** It binds to `127.0.0.1`. `--host` accepts a loopback
  address and nothing else. It refuses any request whose Host header is not a
  loopback name, so a page on the internet cannot point your browser at
  `localhost:7777` and read the structure of your code.
- **Same origin only.** Cross-origin requests are refused.
- **Strict Content-Security-Policy.** The page can load nothing from another
  origin. All assets are embedded in the binary.
- **No data of its own.** Every figure comes from a tool call to `/api/v1`, the
  same tools an MCP agent calls.

---

## Troubleshooting

**The page says there is no index.** Run `graphlume init` and
`graphlume index` in the repository, or pass `--project` to point at one.

**The port is in use.** Choose another with `--port N`, or use `--port 0` to let
the system pick a free one. The banner prints the address it chose.

**`--host` is refused.** The portal has no login and shows the structure of your
codebase, so it is served on loopback addresses only (`127.0.0.1`, `::1` or
`localhost`). To reach it from another machine, use an SSH tunnel to the
loopback port, or serve the tools to agents with `graphlume serve --http`,
which uses tokens.

**A list is empty or short.** Read "What this answer cannot see" beside it. An
empty list means nothing was found in the index, not that nothing exists. The
Index Health page shows how complete the index is.
