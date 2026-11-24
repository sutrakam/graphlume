# TOML: forms not extracted

- **Arrays of tables** (`[[endpoints]]`, `[[bin]]`, `[[package]]`) are not
  recorded. Each element repeats the header, so a name is declared as many times
  as the array has elements: a lock file written as `.toml` would hold one
  `package` symbol per dependency. The grammar node is `table_array_element`;
  a query cannot collapse repeats, so none are captured. Tables nested under an
  element (`[jobs.nightly.retry]`) are plain tables and are recorded.
- **Manifest sections.** `package`, `lib`, `workspace`, `features`,
  `build-system`, `project` (and everything under them) and every table whose
  path has a `dependencies`, `dev-dependencies` or `build-dependencies`
  segment are skipped. They are in every Cargo.toml or pyproject.toml, and the
  dependencies are `dependency` symbols from the manifest analyser
  (`internal/analyze/deps`).
- **Keys** (`port = 8080`, dotted keys `rate.window = 60`, inline-table
  entries) are never definitions here. Every key of a `.toml` file is already a
  `configuration` symbol from `internal/analyze/config`.
- **Keys before the first table** and **tables defined only by a dotted key**
  (`a.b.c = 1` with no `[a.b]` header) have no header to name.
- **White space around dots inside a quoted segment** is also collapsed
  (`["a . b"]` becomes `"a.b"`); this is rare and only alters the name.
- **Exclusions look at the text as written.** `[ package ]`-style padding
  inside a dotted header (`[ project . urls ]`) is trimmed only after the
  exclusion test, so it is still recorded.
