# Editor extension: GraphLume in VS Code

The VS Code extension shows what the local index knows about the symbol under
your cursor: its callers, what a change could break, the tests that reach it
and what implements it. It lives in [`editors/vscode/`](../../editors/vscode/).

It is a front end to the `graphlume` binary. It runs
`graphlume tool call <tool>` for each question, the same surface the
[protocols guide](protocols.md#command-line) describes, and reads the JSON
that comes back. It starts no server, opens no network connection and keeps
no index of its own.

## Contents

- [Install from source](#install-from-source)
- [Use it](#use-it)
- [What the results show](#what-the-results-show)
- [CodeLens](#codelens)
- [Settings](#settings)
- [Limits](#limits)

## Install from source

You need Node.js 22 or later, VS Code 1.125 or later, and the `graphlume`
binary on your `PATH`.

```bash
cd editors/vscode
npm install
npm run compile
npm test
npx @vscode/vsce package --no-dependencies
code --install-extension graphlume-vscode-1.0.0.vsix
```

The extension is not published to a marketplace. To try it without
installing, start VS Code with the extension loaded from the source folder:

```bash
code --extensionDevelopmentPath="$PWD" /path/to/an/indexed/repository
```

Then build an index for the repository you want to explore:

```bash
graphlume init
graphlume index
graphlume resolve
```

The command **GraphLume: Index this workspace in a terminal** runs the same
steps in a terminal you can watch.

## Use it

Put the cursor on a function, method or type. Run a command from the Command
Palette or the editor's context menu.

| Command | Tool it calls |
|---|---|
| GraphLume: Show callers | `callers` |
| GraphLume: Show what this could break | `impact` |
| GraphLume: Show tests that reach this | `tests` |
| GraphLume: Show implementations | `implementations` |

Before each question the extension calls `symbol` to learn which
definitions the index holds under that name. Answers open in the
**GraphLume** panel. Click a row to open the file at the reported line.

The status bar shows whether the workspace has an index, how old it is, and
whether it was built from the commit that is checked out. That comparison
covers commits only. Uncommitted changes are never in the index.

## What the results show

Each row shows the confidence tier the tool reported: `exact`, `high`,
`medium` or `low`. The tiers are explained in
[accuracy and provenance](../accuracy.md).

The first group in the view holds what GraphLume said about its own answer:
the truncation reason, how many results it left out, every note, its verdict
and the index caveat. The extension does not filter or shorten that group.
An empty result is a lower bound, not proof, and the view says so.

When several definitions share a name, the extension asks the language server
which one the cursor is on, or asks you. If the tool then answers for a
different definition, the view says so in a line marked **Check**.

## CodeLens

Set `graphlume.codeLens.enabled` to show the caller count above function and
type definitions. It is off by default.

A count is looked up only for a lens that is in view, waits until typing
pauses, and is cached until the index is rebuilt. At most two lookups run at
once and a file gets at most 60 lenses.

## Settings

| Setting | Default | Meaning |
|---|---|---|
| `graphlume.path` | `graphlume` | Path to the binary. A user setting only: a workspace cannot change which program runs |
| `graphlume.codeLens.enabled` | `false` | Show the caller count above definitions |
| `graphlume.minConfidence` | `low` | Lowest tier shown. Sent to the tool as `min_confidence` |

## Limits

- The extension needs a trusted workspace and files on disk.
- The CodeLens needs a language extension that provides an outline.
- It needs a `graphlume` binary whose symbol tools take `path` and `line`.
  Every question is asked with the path and line of the definition under the
  cursor; a binary that rejects them as unknown arguments is too old, and the
  extension then says so.
- A name shared by several definitions is asked about one definition at a
  time. When the tool cannot tell which one is meant it lists them, and the
  extension offers that list to choose from; nothing is counted until one is
  chosen.
- A long answer is shown one page at a time: the view holds the first page and
  says how many rows the tool counted. There is no "load more" yet.
