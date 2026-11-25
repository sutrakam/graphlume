# GraphLume benchmarks

Every number on this page comes from a result file in this repository or from a command you can run yourself, and each one names its source. Where a result is unflattering it is here too, because a benchmark page that only shows wins is not one you should trust.

**Read every time here as a ceiling, not a typical figure.** All runs were on a shared 14-core Apple M4 Pro with other work on it (load averages are in the source files). Run the commands in [Reproduce them](#reproduce-them) on your own machine before you treat any number as a target.

- [At a glance](#at-a-glance)
- [Accuracy](#accuracy)
- [Speed and memory on real repositories](#speed-and-memory-on-real-repositories)
- [Keeping up with edits](#keeping-up-with-edits)
- [What the agent pays in context](#what-the-agent-pays-in-context)
- [Against other code-graph tools](#against-other-code-graph-tools)
- [Where it is weak](#where-it-is-weak)
- [Reproduce them](#reproduce-them)

---

## At a glance

| | Result | Source |
|---|---|---|
| `high`-confidence edges that are correct | **405 / 420 (96%)**, hand-checked on 14 projects the work was not tuned on | hand-checked, 13 Nov 2026 |
| Calls found in the sampled files | **450 / 529 (85%)** | same |
| Definitions found, 14 languages | **3,081 / 3,106**, 12 spurious | same |
| Callers of one symbol in a 1.5-million-symbol index | **0.66 s** from a new process (Linux kernel) | large-repository run, 13 Nov 2026 |
| Re-index after a two-file edit, django (7,090 files) | **0.83–1.06 s** | [performance](docs/performance.md#the-update-path-on-django) |
| Re-index with nothing changed, django | **0.13–0.21 s** | same |
| MCP schema an agent loads per request | **720 tokens** (`compact`) to 12,770 (`full`) | `graphlume tool list --profile <p>` |
| Relationships in this repository proved by the Go compiler | **59,911 of 61,568 (97.3%)** | `graphlume status` |

---

## Accuracy

Correctness was judged **by hand**, one sampled edge at a time, on 14 open-source projects in 14 languages that the resolver was not tuned on. Two rounds were run with fresh samples.

| Confidence tier | What the tier promises | Measured, round 2 |
|---|---|---|
| `exact` | A compiler proved it (Go's type checker, or an imported SCIP index) | Not sampled: a compiler's answer is the ground truth the others are judged against |
| `high` | Stated types and strong inference | **405 / 420 correct (96%)**, from 73% (C++, 22/30) to 100% by project |
| `medium` | Scope and package boundaries | **249 / 259 correct (96%)** over the 9 projects that have one |
| `low` | A hint that a name exists | **184 / 280 correct (66%)**. Treat it as a lead, not an answer |

| Recall and extraction | Measured |
|---|---|
| Calls in the sampled files that were found | **450 / 529 (85%)**, from 55% (JavaScript) to 100% (TSX) |
| Definitions, the five largest files of each of 14 projects | **3,081 of 3,106** found, 12 spurious |
| Definitions in 36 further languages, three files each | **3,495 of 4,103** found (85% recall), 107 spurious (**97% precision**) |
| The first line of an answer is true of the code | **40 of 42** answers |

On your own code, with a compiler index imported, `graphlume accuracy` measures precision, recall and F1 per tier and per language against the compiler.

---

## Speed and memory on real repositories

### Three mid-size repositories

Cloned at depth 1, indexed and resolved on `darwin/arm64`. Source: [performance, three real repositories](docs/performance.md#three-real-repositories).

| Repository | Language | Files indexed | Symbols | Edges | Index | Peak RSS |
|---|---|---:|---:|---:|---:|---:|
| prometheus | Go | 1,030 | 21,429 | 35,787 | 8.8 s | 664 MB |
| nest | TypeScript | 1,929 | 13,916 | 4,064 | 3.7 s | 590 MB |
| spring-boot | Java | 9,236 | 94,304 | 119,480 | 46.4 s | 756 MB |

### Three very large repositories

Build `ac82f38`, each cloned at depth 1 and indexed from nothing. Measured on 13 November 2026.

| | VS Code | Elasticsearch | Linux kernel |
|---|---:|---:|---:|
| Files indexed | 18,757 | 42,665 | 84,603 |
| Symbols | 443,518 | 769,904 | 1,486,461 |
| Edges | 450,989 | 1,527,008 | 1,292,516 |
| Index, wall | 580 s | 942 s | 1,538 s |
| Index, peak resident | 1.50 GB | 0.99 GB | 2.20 GB |
| Resolve, wall | 227 s | 2,088 s | 281 s |
| Resolve, peak resident | 0.62 GB | 1.01 GB | 0.71 GB |

Time to answer, from a **new process** each time (a running MCP server keeps its caches warm, so an agent sees less):

| Question | VS Code | Elasticsearch | Linux |
|---|---:|---:|---:|
| Callers of one definition | 0.46 s | 0.18 s | 0.66 s |
| References to one definition | 0.47 s | 0.18 s | 0.24 s |
| Definitions by name | 0.18 s | 0.15 s | 0.18 s |
| Text search | 0.18 s | 0.15 s | 0.16 s |
| Subtypes of a widely implemented type | 1.14 s | 2.16 s | not asked |
| Dead code, first five | 2.99 s | 7.73 s | 4.63 s |
| Task context | 7.50 s | 13.96 s | 31.28 s |

An earlier build indexed **Kubernetes** (16,486 files, 274,838 symbols) in 260 s at 2.39 GB peak and resolved it in 57 s at 0.27 GB.

---

## Keeping up with edits

django at commit `a0f2e29`: 7,100 tracked files, 7,090 discovered. Each cell is every run, wall time. Source: [performance, the update path on django](docs/performance.md#the-update-path-on-django).

| `graphlume index` on django | Time | Peak RSS |
|---|---|---|
| Nothing changed | 0.21 / 0.13 / 0.14 s | 57 MB |
| Two files changed | 1.06 / 1.03 / 0.83 s | 188 MB |
| 200 files changed | 1.98 / 2.26 s | 222 MB |
| Cold index into an empty database, for scale | 17.09 s | |

Only files whose size or modification time moved are re-read and hashed, and a file whose content hash is unchanged is not re-parsed. Inside a running `graphlume serve`, files the agent edits are refreshed before the next tool call.

---

## What the agent pays in context

Every MCP tool's schema is sent to the model on every request, so the profile you pick is a standing cost. Measured with `graphlume tool list --profile <profile>` on the current build:

| Profile | Tools | Schema per request |
|---|---:|---:|
| `compact` | 1 (a front door to all of them) | **~720 tokens** |
| `minimal` (default for `serve`) | 8 | ~2,420 tokens |
| `standard` | 19 | ~6,120 tokens |
| `full` | 42 | ~12,770 tokens |

Answers are bounded and paged: a list carries at most a page of rows and says how many it left out, so a function with thousands of callers does not flood the context.

---

## Against other code-graph tools

"Who calls this function?" asked of GraphLume and two other code-graph tools on the same code, on 21 November 2026. All three ran at pinned versions in one Docker container with networking off. For each repository, 25 functions defined once were sampled deterministically, without regard to whether any tool finds callers for them.

| | GraphLume | Other tool A | Other tool B |
|---|---:|---:|---:|
| **prometheus** (`06170ca`), cold index | 42.7 s | **7.6 s** | 53.1 s |
| prometheus, median answer size | **322 bytes** | 999 bytes | 971 bytes |
| prometheus, median answer time | **86 ms** | 100 ms | 430 ms |
| prometheus, callers found by this tool alone | 0 | 4 | 0 |
| **GraphLume's own source**, cold index | 19.8 s | **8.1 s** | 50.7 s |
| GraphLume's source, median answer size | **332 bytes** | 1,009 bytes | 929 bytes |
| GraphLume's source, median answer time | **88 ms** | 102 ms | 438 ms |
| GraphLume's source, callers found by this tool alone | 0 | 8 | 1 |

Agreement on callers (mean Jaccard over the symbols both tools answered): GraphLume and tool A **0.96** on prometheus and **0.92** on GraphLume's source; GraphLume and tool B **0.92** and **0.95**.

How to read it:

- **Tool A indexes fastest**, by a factor of 3 to 6. GraphLume's timed step is `init`, `index` and `resolve`, including cross-file reference resolution, and tool B is the slowest of the three.
- **GraphLume's answers are a third the size and the fastest.** That matters to an agent, which pays for every byte of an answer in context.
- **The extra callers tool A finds are mostly not calls.** On GraphLume's own source they are places where a function is passed as a value (a handler table, a callback argument). GraphLume records those as references, not calls, and `references` and `impact` include them.
- **Agreement is not correctness.** No tool here is ground truth. For precision graded against a compiler, see [Accuracy](#accuracy) and `scripts/bench/impactaccuracy`.

---

## Where it is weak

These come from the same records as the wins above.

- **The Linux kernel is read poorly.** Kernel C is written in macros the grammar cannot see through: 35,894 of 84,603 files have syntax errors, and only 43.8% of the references that name something indexed are bound. Answers there open with `UNRELIABLE` and the percentage, but the counts are not useful.
- **Resolving Elasticsearch takes 35 minutes**, seven times as long as the kernel. Java has the most candidates per name, and the cause was not investigated.
- **Task context is slow from a new process** and grows with the index: 7.5 s, 14 s and 31 s on the three very large repositories. A running server keeps its catalogue in memory.
- **`low` edges are right two times in three.** That is why they are labelled `low` and never mixed with `exact`.
- **Blast radius against real commits.** Asked to predict the other files a commit changed, `impact` scores an F1 of 0 to 0.156 on five repositories (nest scores 0). That question understates both precision and recall, because files changed together are not all dependents. With one seed file per commit the tool beats a same-directory guess everywhere it scores at all. With every changed file taken as a seed, it is behind that guess on flask, gson and zod. Method and caveats: [performance, impact-analysis accuracy](docs/performance.md#impact-analysis-accuracy).

---

## Reproduce them

```bash
# Accuracy against a compiler index you imported, per tier and language
graphlume accuracy

# Heuristic resolver against compiler facts already in this index
graphlume doctor --resolution-precision

# Graph invariants: dangling edges, orphans, exact edges without compiler evidence
graphlume doctor --verify-index

# Context retrieval quality on hand-written cases
graphlume bench

# Schema cost per profile
graphlume tool list --profile compact

# Cold index and incremental update timings on any clone
scripts/bench/cold-index.sh  /path/to/graphlume /path/to/repo
scripts/bench/incremental.sh /path/to/graphlume /path/to/repo path/in/repo/a.py path/in/repo/b.py
```

The methods behind each number, including what each one does not measure, are in [docs/performance.md](docs/performance.md) and [docs/BENCHMARKING.md](docs/BENCHMARKING.md).
