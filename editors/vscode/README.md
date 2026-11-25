# GraphLume for VS Code

Shows what the local GraphLume index knows about the symbol under your
cursor: who calls it, what a change to it could break, which tests reach it,
and what implements it. Every row carries the confidence tier GraphLume
reports, and the view always shows what GraphLume said it could not see.

The extension is a front end to the `graphlume` binary. It starts no server
and has no index of its own.

## Requirements

- The `graphlume` binary, on your `PATH` or at the path set in
  `graphlume.path`. See the [installation guide](https://github.com/sutrakam/graphlume/blob/main/docs/getting-started/installation.md).
  It must be a build whose symbol tools take `path` and `line`. A binary
  that refuses the four questions with "unexpected additional properties" is
  too old, and the extension then says so.
- An index for the workspace, in `.graphlume/`. Build one with the command
  **GraphLume: Index this workspace in a terminal**, or yourself:

  ```bash
  graphlume init
  graphlume index
  graphlume resolve
  ```

- A trusted workspace. The extension runs a program against the folder, so
  it stays off in Restricted Mode.

## Commands

Put the cursor on a function, method or type, then run a command from the
Command Palette or the editor's context menu.

| Command | Asks GraphLume | Tool |
|---|---|---|
| **GraphLume: Show callers** | Which functions call this | `callers` |
| **GraphLume: Show what this could break** | Direct and indirect dependents, with a risk level. The level reads `unknown`, not `low`, when the callers could not be determined | `impact` |
| **GraphLume: Show tests that reach this** | Tests that reach the symbol through the call graph | `tests` |
| **GraphLume: Show implementations** | What implements or extends a type, and what it implements or extends | `implementations` |
| **GraphLume: Index this workspace in a terminal** | Runs `graphlume index`, then `graphlume resolve` | |
| **GraphLume: Show the full tool report** | Opens the exact text the tool returned | |
| **GraphLume: Refresh index status** / **Show index status** | Re-reads the index state | |

Answers appear in the **GraphLume** panel. Click a row to open the file at
the line the tool reported.

## Reading the results

The line under the view's title is the tool's own summary, unchanged. When
the index cannot stand behind the answer, the summary opens with the reason:

| First word | Meaning |
|---|---|
| `UNKNOWN` | Nothing can be concluded. Do not use the answer |
| `UNRELIABLE` | Most references in the symbol's language are unbound. Treat the rows as leads |
| `UNVERIFIED` | Some rows are probably bound to the wrong definition |
| `INCOMPLETE` | The rows are sound and the list is known to be short |
| `AMBIGUOUS` | The name has several definitions and the tool answered for none. The extension offers them as a choice and never shows them as callers, tests or impact |

The same word, in lower case, is shown beside the row count, as in
`unreliable · 1 row`. A verdict of `mixed` reads `mixed confidence` there.

The row count is the tool's count of the rows that exist. When it returned
fewer, the view says `showing 50 of 195`. The extension reads the first page
only, and a line marked **Shown** says so.

The first group in the view is **What GraphLume says about this answer**. It
holds, in the tool's own words:

- how far the index supports the answer, first, when that is anything less
  than good;
- whether the index was built from the commit that is checked out;
- whether the answer was truncated, and why;
- how many results were left out to fit the response;
- every note the tool attached, such as how many references in the
  repository are unresolved;
- the tool's verdict on its own answer and its suggested next step.

Nothing in that group is filtered or shortened by the extension. A sentence
the tool gave twice is shown once. A line carries a warning icon when it is
one a reader must not miss; the verdicts `unreliable`, `incomplete`, `mixed`,
`weak` and `empty` are among those. Click any line to read the full report.

Each result row shows its confidence tier:

| Tier | Meaning |
|---|---|
| `exact` | A compiler proved the binding when the index was built |
| `high` | Inferred from strong evidence. It may still be wrong |
| `medium` | Inferred from partial evidence. Treat it as a lead |
| `low` | A weak inference. Confirm it before acting on it |

An empty result is not proof. GraphLume reports a lower bound: a caller it
could not resolve does not appear. The view says so each time.

### When a name is shared

Several definitions can share one name. The extension asks the language
server where the symbol under the cursor is defined, then asks GraphLume
about that definition, naming it by its file and the line it starts on. If
it cannot tell which one you mean, it asks you.

The tool answers for one definition or for none. If the file and line do not
come down to one, it lists the definitions instead of answering. The
extension then offers you that list and asks again about the one you choose.
The list is never shown as callers, tests or impact. If you close it, the
view says that nothing was analysed.

If the tool answers for a different definition than the one you chose, the
view says so in a line marked **Check**. Lines marked **Check** and **Shown**
are written by the extension, not by GraphLume.

## CodeLens

Set `graphlume.codeLens.enabled` to show the caller count above function
and type definitions. It is off by default.

The lens does not run a process as you type:

- lens positions come from the editor's outline, computed once per document
  version and capped at 60 per file;
- a count is looked up only for a lens that is scrolled into view;
- a count is cached until the index is rebuilt;
- a lookup waits until the document has been quiet for about a second;
- at most two lookups run at a time.

A lens needs a language extension that provides an outline for the file. A
count of zero reads "0 callers found", never "unused".

What a lens can read:

| Lens | Meaning |
|---|---|
| `2 callers, 3 call sites` | Two functions call this, at three places |
| `195 call sites` | The tool counted 195 and returned part of the list, so the callers cannot be counted apart |
| `1 caller (unreliable)` | The tool's own word for a count the index cannot stand behind. Also `unverified`, `incomplete`, `mixed confidence` |
| `callers unknown` | The tool said nothing can be concluded |
| `callers unknown (name matches several definitions)` | The tool listed definitions instead of answering. Click to choose one |
| `50+ callers` | An older binary cut the list and gave no count |

A name the file defines more than once is looked up with the line of each
definition. The line comes from the editor, so after edits it can miss until
the index is rebuilt.

## Status bar

The status bar item shows whether the workspace has an index and how old it
is, and warns when the index was built from a different commit than `HEAD`.
That comparison covers commits only: uncommitted changes are never in the
index. Click the item to index the workspace or refresh the status.

## Settings

| Setting | Default | Meaning |
|---|---|---|
| `graphlume.path` | `graphlume` | Path to the binary, or a command name on `PATH`. A user setting only: a workspace cannot change which program runs |
| `graphlume.codeLens.enabled` | `false` | Show the caller count above definitions |
| `graphlume.minConfidence` | `low` | Lowest tier shown: `low`, `medium`, `high` or `exact`. Sent to the tool as `min_confidence`, so the tool reports how many results it left out |

## Privacy

Everything stays on your machine.

- The extension runs the local `graphlume` binary and reads what it prints.
  It opens no network connection and has no telemetry.
- The binary is started directly with an argument list, never through a
  shell. Text from the editor reaches it only as a JSON value.
- Every run has a timeout and stops when you cancel it.

## Troubleshooting

| Message | Cause | Fix |
|---|---|---|
| The graphlume binary was not found | The binary is not on the `PATH` VS Code sees | Set `graphlume.path` to the full path, for example `/usr/local/bin/graphlume` |
| This workspace has no index to answer from | No `.graphlume/` index | Run **GraphLume: Index this workspace in a terminal** |
| The tool reported a failure | The tool rejected the question | Open the report from the message to read the tool's own text |
| ... unexpected additional properties ["line"] | The installed binary is from before `path` and `line` selected a definition | Update `graphlume` |
| "..." matches several definitions, so nothing was analysed | The tool could not single out one definition | Run the command again and choose from the list |
| No tool named ... | The installed binary is older than this extension | Update `graphlume` |
| No CodeLens appears | The file has no outline, or the index is absent | Install the language's extension; check the status bar |

## Build from source

Needs Node.js 22 or later.

```bash
cd editors/vscode
npm install
npm run compile
npm test
npx @vscode/vsce package --no-dependencies
code --install-extension graphlume-vscode-1.0.0.vsix
```

To run the tests against a real binary and index as well:

```bash
GRAPHLUME_BIN=/path/to/graphlume GRAPHLUME_PROJECT=/path/to/indexed/repo npm test
```

## Licence

Apache-2.0, the same as GraphLume. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
