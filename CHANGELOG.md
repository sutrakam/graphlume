# Changelog

All notable changes to GraphLume are recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.0] - 2026-11-24

First public release. GraphLume is a local code graph for AI coding agents,
served over MCP, a CLI, a REST API and a web portal.

### Indexing & accuracy
- Indexes a repository once into a local SQLite graph of definitions, calls, imports, HTTP routes, database tables, events and tests, and keeps it current with `watch`, git hooks and a working-tree check before every `serve` answer.
- Reads 206 languages in four tiers (typed, resolved, parsed, text only), listed by `graphlume languages`. The language of a file follows its content, shebang and `index.language_overrides`, not only its extension.
- Every edge carries a confidence tier: `exact` (a compiler proved it), `high`, `medium` or `low`. Only a compiler may claim `exact`; this is enforced in the code and the database schema.
- Go is type-checked out of the box. `graphlume indexers install` adds pinned SCIP compiler indexers for other languages, and `graphlume scip import` loads a `.scip` file; `resolve` also imports one found in the project or `.graphlume/`. A compiler's answer overrides a heuristic binding.
- Resolves calls by receiver type, lexical scope, named imports, arity and return types, binds renamed imports through the import that declares them, and leaves a reference unbound rather than guessing between same-named candidates.
- Records constructor calls as `INSTANTIATES` edges, Go and NestJS routes with the prefix they are served under, and test coverage imported from Go, LCOV, Cobertura and JaCoCo reports.
- Recovers from syntax errors in grammar gaps, records how each file was parsed (`ok`, `partial`, `failed`, `skipped`) and states how complete an index is.
- Every answer is graded against the coverage it rests on and says what it could not see. An ambiguous name returns a list of candidates with the `path` and `line` that select each.
- Secrets are redacted before text reaches the index. Indexing makes no network requests and runs under a memory budget, with streaming file discovery for very large repositories.
- Two indexes cannot corrupt each other: indexing, resolving, serving and watching coordinate through a lock, and a damaged or foreign index is named as such.
- The index file reclaims its own free space. `index`, `resolve` and `watch` give free pages back to the file system after a write once they reach 64 MiB and 20% of the file, in short steps that never block a reader, and then checkpoint the write-ahead log. `status` shows the free space and the auto-vacuum mode; `index.auto_compact: false` turns it off.
- Compiler indexers recover from the two most common failures on large services: a Node indexer that runs out of heap is run again with 8 GiB, and scip-python skips package discovery when a `pyproject.toml` without a `[project]` table breaks it. A TypeScript project whose `tsconfig` only points at a file its framework generates on first build (`.nuxt`, `.svelte-kit`) is indexed through a stand-in tsconfig GraphLume writes beside its own output, without running the project's build tooling.
- `graphlume init --all` (and `--indexers`, and `--index` at a terminal) walks you through the compiler indexers the project's languages need before it indexes, so the first resolve already uses them. For each language with no compiler proof it says what is missing, what would be downloaded, from where and about how big, then asks: `[Y/n]` for a small pinned, checksum-verified indexer, `[y/N]` for a toolchain (the .NET SDK, Node.js, a JDK, Maven, Gradle; installed with Homebrew on macOS, otherwise the official page is printed) and for generating a C/C++ `compile_commands.json` with the repository's own CMake, make or SCons build. Without a terminal it prints the plan and installs nothing; `--yes` accepts only the pinned indexers, and a repository build needs `--allow-repo-build`. Declined offers are remembered in the user config (`accuracy.indexers.declined`) and `graphlume indexers install --ask-again` forgets them. A failure is reported and that language is indexed without the compiler. npm-distributed indexers install into `~/.graphlume/npm` instead of npm's global prefix, so an unwritable or off-`PATH` global prefix no longer stops them.
- `graphlume indexers list` also lists languages whose code has no project, with a file count and what each needs: Python scripts without a `pyproject.toml`, and C or C++ without a `compile_commands.json`, with the command for the build system it finds (CMake, Meson, SCons, make). A C# project says the .NET SDK comes before `scip-dotnet`, and a Visual Studio solution with no C# code beneath it is no longer planned as C#.
- More compiler failures are worked around instead of lowering the proof: a `tsconfig` that extends a workspace package that is not installed, or a file a framework generates, and an inferred-tsconfig walk that stops on a dangling symbolic link, are indexed through the stand-in tsconfig. A language marker with no source files of that language (a `requirements.txt` in a Go repository) is reported as having nothing to read, not as a failed compiler. Identical "not installed" lines are printed once with a count, and a missing JVM build tool names the container route.
- TypeScript and JavaScript monorepo workspaces (pnpm, Yarn, npm) resolve cross-package imports without `npm install`: when packages are uninstalled, a stand-in tsconfig maps sibling packages (`@acme/ui`) and their export subpaths directly to their source entry points and folders.
- Cargo workspaces are indexed in a single `rust-analyzer scip` run from the workspace root covering every member crate; rust-analyzer runs safely by default with build scripts and procedural macros disabled and cargo offline, while `--allow-repo-build` enables them.
- C# compiler indexing pins `scip-dotnet` 0.2.16 with SHA-256 verification from NuGet, installed into `~/.graphlume/dotnet` as a default-yes offer when the .NET SDK is present.
- Ruby compiler indexing pins `scip-ruby` 0.5.1 with standalone SHA-256 verified binaries for macOS and Linux, indexing Ruby projects (`Gemfile`, `*.gemspec`, `Rakefile`) safely without executing repository code.
- Svelte component files are converted into line-aligned shadow TypeScript files so `scip-typescript` indexes their script blocks, and SCIP facts are mapped back to the original `.svelte` files; pins `svelte2tsx` 0.7.63.
- Guided setup offers `go mod download` when `go.mod` has dependencies to avoid stubbed imports, and `status` reports any stubbed imports. When Docker is present, setup offers containerized Java indexing (`[y/N]`) with improved diagnostics for JDK version mismatches, missing Gradle wrappers, and offline dependency resolution.
- Bare root `package.json` configurations where all JS/TS sources belong to subprojects or test tooling are skipped at the root, preventing empty project indexing failures.
- Non-interactive flags (`--yes` and `--allow-repo-build`) bypass interactive terminal prompts during guided setup, enabling reliable automated and headless execution.
- Increased Tree-sitter definition query depth ceiling from 1,500 to 10,000, allowing large C and C++ header files with thousands of declarations (e.g. OpenGL `qgl.h`, `glext.h`) to parse cleanly without hitting budget cuts.
- Optimized SCIP resolution memory retention when loading symbol tables in large multi-crate repositories.
- `scripts/compat` runs GraphLume cold on a pinned corpus of public repositories, one per stack, as a release gate.

- The number of parse workers follows the machine: half the CPUs, from 4 to 8, held lower when the soft memory limit cannot hold that many. `--workers` overrides it.

### MCP tools
- 42 tools with single-word names, in four profiles: `compact` (one front-door tool), `minimal` (8, the default for `serve`), `standard` (19) and `full` (42, plus `estate` when an estate is configured). `graphlume tool list --profile <name>` prints each.
- Understanding: `context` (a bounded pack for a task), `explore` (definition, callers, callees, routes and covering tests in one call), `overview`, `search`, `symbol`, `definition`, `references`, `callers`, `callees`, `implementations`, `outline`, `graph`.
- Change safety: `impact`, `tests`, `plan`, `diff`, `blindspots`, `rename`, `patch`, `changes`, `coverage`.
- Structure and health: `endpoints`, `tables`, `config`, `trace` (with waypoints), `deadcode`, `hotspots`, `busfactor`, `owners`, `communities`, `stats`.
- Answers are bounded and paged, report truncation, and carry `file:line` evidence.
- Editing tools check that the index is current before they write.
- Estate: `estate` (`full` profile, and `action: estate` on the front door) answers `links`, `callers` and `impact` across a multi-repository estate from `graphlume serve --estate FILE` or `estate:` in config: HTTP route, shared-table and Go function and method links with version warnings, every row naming its member, never `exact` across a repository boundary, members opened read-only and an unreadable one reported while the rest answer.

### CLI
- `graphlume accuracy --compare <file.csv> [--label <label>]` evaluates external predictions against compiler ground truth, reporting side-by-side precision, recall and F1 comparison tables and publishing them under `comparison` in `--json`.
- `scripts/compare/sample.py` samples $N$ random functions or call sites into a CSV template for fair, repeatable cross-tool evaluation.
- `graphlume setup` and `init` register the server with 40+ agents and write agent guidance; `uninstall` and `clean` remove it again and refuse to run from a home folder or an unindexed directory.
- Every command takes `--json`; `graphlume tool call <tool> key=value` runs any MCP tool from a shell.
- `rename <old> <new>` renames a symbol at its definition and every resolved reference, as a dry run by default, and exits 3 when occurrences of the old name remain. `move` moves a Go declaration to another file of the same package and fixes the imports.
- `tour` gives a reading order from the entry points; `communities` uses Leiden clustering and ranks central files by PageRank; `pack` writes the repository as one XML, Markdown or JSON prompt; `minimize-diff` strips noise from a diff.
- `sbom` writes CycloneDX 1.5 for declared dependencies, and `vulns --advisories FILE` checks them against OSV records you supply. Nothing is downloaded.
- `arch check` and `arch adr` check architecture rules and read decision records.
- `doctor` verifies an index, `index export` and `index pull` share an index built in CI, and `serve` stays alive on a project with no index and answers each call with what to do.

### Portal
- `graphlume ui` is a local, read-only, loopback-only web portal. It has six sections (Home, Map, Explore, Change, Health, Agents) and a page for each function, type, file, route and table.
- A Map workbench with six views: directories, files and functions; impact rings; a dependency matrix with cycles marked; a risk treemap; call flow from an entry point; and file communities. It also has Trace A to B, a rename plan gated on reach and tests, and Context for a task. An explorer tree beside the canvas and a status bar keep the reader oriented.
- The Map, the Matrix and the explorer read file rollups the index keeps (`file_edges`: edges summed per source file, target file, kind and confidence; `file_symbols`: definitions per file), so every level of a 27,000-file monorepo answers well within the graph tool's 3-second limit. `index`, `resolve`, `scip import` and `watch` keep them current, and the `graph` tool can query them too.
- Every figure is a call to the same tools an agent uses under `/api/v1`. Nothing is computed from data a tool did not return, and a cap on an answer is named on screen.
- Search doubles as a jump list, `?` lists every keyboard shortcut, and long pages open with an "On this page" row. The header shows the index revision and whether HEAD has moved since it was built. Light and dark themes use Google's palette and pass WCAG AA contrast, and the page loads nothing from another origin.
- A VS Code extension in `editors/vscode` shows callers, impact, tests and implementations for the symbol under the cursor, with each row's confidence tier and the tool's own caveats.

### Multi-repo
- `graphlume system` links many repositories into one graph: HTTP clients to the routes they call, and services to the tables they share. `system contracts` lists calls whose method or path matches no route.
- Go calls are stitched across repositories when one member imports a Go module another member defines. `system callers` and `system impact` walk dependents across members with the weakest confidence on each path. A link across repositories is never `exact`.
- Go method calls are followed across members too: a receiver declared as another module's type links at `high`; a typed package variable, a local initialised from a constructor of the provider's type, interface methods and methods promoted through embedding link at `medium`. Reassigned locals, untyped variables, chains, fields and third-package types are counted by reason, never guessed.
- `workspace detect` finds services in a monorepo or a folder of repos. `workspace sync` and `workspace links <service>` keep a persistent link database in `.graphlume/workspace.db` and mark a link stale when either side is re-indexed.
- The project registry keeps one entry per canonical root, writes atomically and prunes projects whose folder is gone.

### CI & policy
- `pr-impact` turns changed lines into symbols, callers and tests as a Markdown PR comment. `ci check` reports blast radius and architecture rules as Markdown or SARIF 2.1.0.
- `policy check` runs rules from a `policy:` block in `.graphlume.yml`: `max_blast_radius`, `no_new_cycles`, `architecture` and `contracts`, each with `severity: error|warning`. It exits 2 on an error-severity violation and writes text, JSON or SARIF. `policy explain` lists the rules.
- A composite GitHub Action (`action.yml`) runs index, SCIP import, PR impact, SARIF and a sticky comment, and can upload the index as the artifact `graphlume-index-<commit>`.
- `serve --http` offers MCP Streamable HTTP and a REST API with OpenAPI 3.1. `--tokens-file` accepts named tokens with a scope, an expiry and a role (`viewer`, `editor`, `admin`); a tool outside a token's scope is answered like one that does not exist, and an audit log names the token.
- `index pull` downloads the index CI built for a commit with your own `gh` login and installs it only after checking its manifest, checksum, schema and integrity.

### Packaging
- Static, CGO-free binaries for macOS (arm64, amd64, universal), Linux (amd64, arm64) and Windows (amd64), with `install.sh` and `install.ps1`. Optional cgo tree-sitter builds exist for Linux.
- A `go install` path and a static, non-root container image on GHCR, with packaging prepared for Homebrew, Scoop and winget. The release job renders the Homebrew formula from the release's `SHA256SUMS`.
- MCP bundles (`.mcpb`) for Claude Desktop, an MCP registry entry (`server.json`) and a Smithery configuration.
- Releases are signed with Sigstore and carry build provenance attestations and CycloneDX SBOMs; third-party CI actions are pinned to a commit.
- Documentation covers the CLI, the MCP tools, the REST protocols, the portal, accuracy and benchmark records, security and troubleshooting, with `llms.txt` for agents.
