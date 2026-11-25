# Release checklist: the user journey

Unit and race tests prove the code. They do not prove what a person sees when
they install GraphLume and run it. Every case below is a journey a user takes;
each has the behaviour a release must show and the test that holds it there.

- **Unit**: a Go test in the package, run by CI on every run.
- **Journey**: a scenario in [`scripts/first-run.py`](../../scripts/first-run.py),
  run against the release binary with `HOME` pointed at a throwaway directory.

A release is tagged only when CI is green **and** `python3 scripts/first-run.py` passes
on the build being released.

## A. Where it is run

| ID | Case | Expected | Test |
|---|---|---|---|
| A1 | `init --all` in the home directory | Refused before anything is read or written, with what to do instead | Unit, Journey |
| A2 | A filesystem root, or the folder that holds home directories | Refused the same way | Unit, Journey |
| A3 | A subfolder of a git repository | Indexes that folder, and says which repository it is part of and how to index all of it | Journey |
| A4 | A folder that is not a git repository | Works; says which answers need git history (owners, hotspots, bus factor, impact over commits) | Journey |
| A5 | An empty folder | Works; says no source files were found | Journey |
| A6 | A monorepo of services in several languages | One question per language or tool for the whole folder; each phase shows progress | Journey |
| A7 | A folder the user cannot write to | A clear error naming the path; nothing half-written | Journey |
| A8 | A path with spaces or non-ASCII characters | Works | Journey |

## B. Running it again

| ID | Case | Expected | Test |
|---|---|---|---|
| B1 | `init --all` on a project that already has an index | Reuses the index, updates it incrementally, refreshes registrations; exit 0 | Journey |
| B2 | `init` on a project that already has an index | Says it is already set up and what to run next; exit 0 | Unit, Journey |
| B3 | A run stopped with Ctrl-C while indexing | The next run recovers; the previous index stays readable | Unit (atomic swap), Journey |
| B4 | A run after `clean` | Behaves like a first run | Journey |

## C. Questions

| ID | Case | Expected | Test |
|---|---|---|---|
| C1 | The same tool is needed by several projects in the folder | Asked once; the answer applies to every project | Unit, Journey |
| C2 | A second run after the user answered | Not asked again; declined offers stay declined, accepted ones are already present | Unit, Journey |
| C6 | Answering yes to every offer (the default) | The indexers install and run, with a progress line at least every 15 seconds | Journey |
| C3 | No terminal and no `--yes` | Never waits for input; takes the safe default and says so | Journey |
| C4 | `--yes` | Installs only the small pinned, checksum-verified indexers; never a toolchain or a build | Unit |
| C5 | Ctrl-C at a question | Exits cleanly, nothing half-installed | Journey |

## D. Output

| ID | Case | Expected | Test |
|---|---|---|---|
| D1 | Every phase | Announced when it starts; no silence longer than 20 seconds | Journey |
| D2 | Work repeated over many items | One progress line naming the current item, never the same line again and again | Unit, Journey |
| D3 | The end of a run | One summary: what was done, how to see every file written outside the project (`graphlume install --list`), what stays graded, the next command | Journey |
| D4 | `--quiet`, `--json`, `NO_COLOR`, no terminal | Respected; no escape codes in a log | Journey |
| D5 | The same information | Shown once per run, not in two places | Journey |
| D6 | Internal logs | Never mixed into normal output; `--verbose` shows them | Journey |
| D7 | Files GraphLume writes itself (`AGENTS.md`, `.mcp.json`) | Never reported back to the user as their uncommitted changes | Journey |
| D8 | Progress and failures | Each progress mark and each failure is reported once | Journey |

## E. Downloads and network

| ID | Case | Expected | Test |
|---|---|---|---|
| E1 | Any download | Announced first: what it is, where from, how large; asked unless `--yes` covers it | Unit, Journey |
| E2 | A Go module that asks for a newer Go | No Go release is downloaded: setup runs the installed Go (`GOTOOLCHAIN=local`) and the built-in type checker runs no go command | Unit, Journey |
| E4 | Go read by the built-in type checker | No Go download is announced, because none happens | Journey |
| E3 | No network | Indexing completes without the compiler indexers, and says which ones were skipped | Journey |

## F. Files and privacy

| ID | Case | Expected | Test |
|---|---|---|---|
| F1 | Files outside the project | Never read; symbolic links are not followed out of it | Unit |
| F2 | Writes outside the project | Only agent configs and GraphLume's own settings, each listed in the summary | Journey |
| F3 | macOS privacy prompts (Music, Photos, Documents) | Never triggered by a run in a project | Journey (A1, A2) |

## G. Agent registration

| ID | Case | Expected | Test |
|---|---|---|---|
| G1 | Two projects set up one after the other | Each agent session uses the index of the project it is working in; setting up the second does not redirect the first | Unit, Journey |
| G2 | Running setup twice | No duplicate entries; other servers in the same config untouched | Unit |
| G3 | `clean --agents` | Removes GraphLume's entries only | Journey |

## R. Index health and repair

| ID | Case | Expected | Test |
|---|---|---|---|
| R1 | Files changed since the last index | `init --all`, `status` and the agent all say so; the next index is incremental | Journey |
| R2 | Nothing keeps the index current (no hooks, no watch) | `status` and `doctor` say how the index is kept current and what to run | Journey |
| R3 | A corrupted index file | `doctor` names it; one command rebuilds it, keeping the damaged file as a backup | Unit, Journey |
| R4 | An index from an older schema | Upgraded in place, or a clear instruction | Unit |
| R5 | An index left locked by a killed run | The next run recovers without manual steps | Unit, Journey |

## P. Platforms

| ID | Case | Expected | Test |
|---|---|---|---|
| P1 | macOS on Apple silicon | The installer fetches the arm64 binary | Journey |
| P2 | Linux x86-64 and ARM64 | CI and the release container | CI |
| P4 | The container image | `init --index`, `status` and `serve` work as the installation guide runs them | Journey (`GRAPHLUME_IMAGE=<image>`) |
| P3 | Windows | Paths, the PowerShell installer, no shell-only steps | CI (Windows job) |
