# Changelog

All notable changes to the GraphLume VS Code extension are recorded here.

## 1.0.0

First version, for the `graphlume` binary v1.0.0.

### Added

- Every question names one definition by its bare name, its file and the
  line it starts on. The fully qualified name is not sent, because overloads in
  one file share it.
- A name the tool matches to several definitions is offered as a list to
  choose from, and the question is asked again about the choice. The list is
  never shown as results.
- The results view shows the tool's summary unchanged, including an opening
  `UNKNOWN`, `UNRELIABLE`, `UNVERIFIED` or `INCOMPLETE`, and shows the same
  word beside the row count. The reliability the tool reports comes first in
  the view when it is less than good.
- The verdicts `unreliable`, `incomplete` and `mixed` are shown as warnings,
  as are `weak` and `empty`.
- Row counts come from the tool's own total. A list the tool returned in
  part reads `showing 50 of 195`, and the CodeLens shows the counted total
  with no `+`.
- The CodeLens marks a count the index cannot stand behind, as in
  `1 caller (unreliable)`, and reads `callers unknown` when the tool says
  nothing can be concluded or lists definitions instead of answering.
- A tool failure that names unexpected arguments says the binary may be
  older than the extension expects.
- Commands for the symbol under the cursor: **Show callers**, **Show what
  this could break**, **Show tests that reach this** and **Show
  implementations**. They call `callers`, `impact`, `tests`
  and `implementations` through `graphlume tool call`.
- A results view in the panel. Each row shows its confidence tier and opens
  the file at the reported line. The tool's own notes, truncation reason,
  omitted counts, verdict and index caveat are shown first and never hidden.
- A check that the tool answered for the definition that was asked about,
  shown as a **Check** line when it did not.
- A CodeLens with the caller count above function and type definitions. Off
  by default, resolved only for visible lenses, cached until the index is
  rebuilt, and held back while you type.
- A status bar item showing whether the workspace has an index, how old it
  is, and whether it was built from the checked-out commit.
- **Index this workspace in a terminal**, which runs `graphlume index` and
  `graphlume resolve` as visible tasks, with `graphlume init` first when
  there is no index.
- Settings: `graphlume.path`, `graphlume.codeLens.enabled` and
  `graphlume.minConfidence`.
- Messages for a missing binary, a missing index and a tool failure, each
  with one action that fixes it.
