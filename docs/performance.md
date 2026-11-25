# Performance

Every number in this document comes from a run on the machine described below.
Where a measurement chooses a default, the measurement is reproduced alongside,
because a default without its evidence is just an opinion.

A number that cannot be reproduced on this machine is left out, not given with a
caveat. Where the reasoning behind a decision stands without reproducible
figures, the reasoning is given on its own.

## The machine, and how to falsify any of this

| | |
|---|---|
| Host | Apple M4 Pro, 14 cores, macOS 26.5.1 |
| Toolchain | Go 1.25.13, `darwin/amd64` — an amd64 toolchain under Rosetta 2 |
| Build | `CGO_ENABLED=0 go build -o /tmp/di-docs ./cmd/graphlume` |
| Tree | commit `6a7b909` |
| Cache | cold database each trial (`.graphlume` removed, `graphlume init` re-run) |

Go's own benchmark headers on this host print `goarch: amd64` and
`cpu: VirtualApple @ 2.50GHz`, which is the emulated view, not the silicon. The
*shape* of each curve survives the difference; the absolute numbers do not.
Reproduce them on your own machine before treating any of them as a target.

### How much the emulation costs

Enough to matter, so it is measured rather than asserted. The same source built
twice — `GOARCH=amd64` and `GOARCH=arm64`, `CGO_ENABLED=0` both times — over
GraphLume's own source (315 files), best of three trials on an idle machine:

| Operation | amd64 under Rosetta | arm64 native | Native is |
|---|---|---|---|
| Full index | 1,801 ms | 1,264 ms | 1.43x faster |
| Re-index, nothing changed | 124 ms | 67 ms | 1.84x faster |
| Re-index, one file changed | 206 ms | 129 ms | 1.60x faster |

Query-side commands gain more, because they are dominated by process start and
CPU rather than by disk: on a 135,000-symbol corpus, p95 over 30 runs, startup
went 64.4 ms → 20.4 ms (3.2x), `search` 114.2 ms → 61.5 ms (1.9x), `symbol`
72.9 ms → 38.4 ms (1.9x), `callers` 76.2 ms → 41.0 ms (1.9x).

**So treat every figure below as a ceiling, roughly 1.4–3x above what a native
build delivers.** Released binaries are unaffected: CI cross-compiles
`darwin/arm64` alongside `darwin/amd64`, so an Apple Silicon user installing a
release gets the native build. This is a property of one development machine,
not of the product.

If you benchmark this project, check `go env GOARCH` against `uname -m` first.
On this machine they disagree, so a figure taken without checking is silently
emulated.

Three trials per figure, and all three are printed rather than averaged. Other
builds and test suites share the machine during some of these runs, so the
spread between trials is the honest picture of a shared developer laptop —
averaging it away would hide the variance a reader actually cares about.

## How these numbers are produced

Three corpora are used, and every figure below names which one it came from.

**1. `fixtures/payment-platform`** — in the repository. 10 files discovered, 9
indexed, 41 symbols, 55 references, 3 imports. Small enough that it measures
fixed costs (process start, migrations, transaction setup) rather than
throughput.

**2. GraphLume's own source** — `git clone` of this repository at `6a7b909` into a
temporary directory, so the working tree is untouched and the corpus is exactly
what the commit contains. 303 files discovered, 275 indexed, 28 skipped, 4,013
symbols, 18,429 references, 1,416 imports.

**3. A 5,000-file synthetic Go corpus (`go5k`)** — generated, ~20 MB of source,
100 packages of 50 files each, 40 top-level declarations per file, yielding
210,000 symbols, 240,000 references and 10,000 imports. Each file's content is a
pure function of its index, so the corpus is byte-for-byte reproducible. The
generator emits a mix of functions, methods, types and constants, plus a `Drive`
function per file that calls into the previous file of the same package, so the
reference-extraction and binding paths have real cross-file work to do:

```python
# one file of the corpus, in outline
package pkg000
type Widget3 struct { ID int; Name string }
func Compute3_0(a, b int) int { ... }          # kind rotates every 5 declarations
func (w *Widget3) Render3_1() string { ... }
const Limit3_2 = 17
type Option3_3 struct { ... }
func Normalize3_4(s string) string { ... }     # calls Compute3_0
func Drive3(total int) int { ... }             # calls into file002 of pkg000
```

Method, for every timing below:

```bash
CGO_ENABLED=0 go build -o /tmp/di-docs ./cmd/graphlume
cp -R <corpus> /tmp/work/repo && cd /tmp/work/repo && git init && git commit -am init
for trial in 1 2 3; do
  rm -rf .graphlume && /tmp/di-docs init
  /tmp/di-docs index --full          # full index into an empty database
  /tmp/di-docs index                 # re-index, nothing changed
  echo '// touched' >> <first file>
  /tmp/di-docs index                 # re-index, one file changed
  /tmp/di-docs resolve
done
```

Durations are the ones GraphLume prints itself, so they exclude process start.
Database sizes are `index.db` on disk. Peak memory is
`/usr/bin/time -l … | grep 'maximum resident set size'`. Go benchmarks are run
with the exact command quoted in each section.

**One thing the synthetic corpus cannot measure.** Its identifiers repeat across
packages by construction, so `graphlume resolve` on it reports 0 resolved and
80,000 ambiguous. Its resolve timings measure the throughput of the binding
path; they say nothing about binding *accuracy*. Every accuracy figure in this
document comes from a real corpus instead.

## Indexing

Three samples of each operation, in trial order.

### `fixtures/payment-platform` — 9 files indexed, 41 symbols

| Operation | Time |
|---|---|
| Full index into an empty database | 297 / 303 / 413 ms |
| Re-index, nothing changed | 22 / 36 / 66 ms |
| Re-index, one file changed | 94 / 102 / 112 ms |

`index.db` after the full index: 232 KiB.

At this size the run is almost entirely fixed cost, which is why a no-op re-index
of nine files still costs tens of milliseconds and why the spread across trials
is as wide as the value itself. Do not read a throughput number out of this
table; read one out of the 5,000-file table below.

### GraphLume's own source — 275 files indexed, 4,013 symbols

| Operation | Time |
|---|---|
| Full index into an empty database | 2.02 / 1.74 / 1.82 s |
| Re-index, nothing changed | 84 / 60 / 61 ms |
| Re-index, one file changed | 182 ms |

`index.db` after the full index: 8.25 MB.

### The 5,000-file synthetic corpus — 210,000 symbols

| Operation | Time |
|---|---|
| Full index into an empty database | 28.1 / 34.6 / 19.7 s |
| Re-index, nothing changed | 972 / 965 / 703 ms |
| Re-index, one file changed | 818 / 1,115 / 883 ms |

`index.db` after the full index: 102.4 MB; after `resolve`, 105.0 MB.

The 19.7–34.6 s spread on the full index is machine contention, not corpus
variance — the file set is identical across trials and the resulting symbol
counts are identical to the unit. Treat the low end as the figure for an idle
machine and the high end as what a laptop compiling something else delivers.

**The gap between a full index and a no-op is the whole point of incremental
indexing**, and getting there required one decision that is easy to get wrong.

**Diffing is by content hash, not mtime.** `git checkout` rewrites timestamps
wholesale on files whose content is identical. An mtime-based diff would report
every file as modified after a branch switch and reparse the entire repository —
turning the cheapest common case into the most expensive one. Hashing costs a
read per file: that is what the ~0.9 s no-op run over 5,000 files is paying for,
and it is roughly 180 µs per file including the walk.

**An unchanged file is not read at all.** The hash is the verdict, but a file
whose size and times match the index, and are more than two seconds older than
the previous run, is not opened; the rule and its limits are in
[the CLI reference](CLI.md#index) and `internal/acquire/fs/trust.go`. The no-op
and one-file figures in the table above read and hash every file, so on this
corpus they are an upper bound. On django, skipping the read takes a no-op from
2.5 s to 0.13 s; see [the update path on django](#the-update-path-on-django).

## Three real repositories

Every other figure in this document comes from a synthetic corpus or from
GraphLume's own 275-file source. Those are uniform in ways real code is not, and
that uniformity hides real behaviour — a generated corpus cannot show the
resolution memory bound, and a fixture that names every symbol uniquely never
exercises the candidate cap at all.

Cloned at `--depth 1`, indexed and resolved on `darwin/arm64` native:

| repository | language | files discovered | indexed | symbols | edges | index | peak RSS |
|---|---|---|---|---|---|---|---|
| prometheus | Go | 1,262 | 1,030 | 21,429 | 35,787 | 8.8 s | 664 MB |
| nest | TypeScript | 2,297 | 1,929 | 13,916 | 4,064 | 3.7 s | 590 MB |
| spring-boot | Java | 11,851 | 9,236 | 94,304 | 119,480 | 46.4 s | 756 MB |

All twelve `doctor --verify-index` invariants pass on all three. Editing six
real files in prometheus one at a time and re-indexing after each produces an
index identical to a full rebuild of the same tree — same symbols, edges,
references, bindings and config usages, and the same 35,787 edge set.

Three things only a real repository shows:

**The drain threshold sets peak memory.** With a threshold of 1 GiB a 1,262-file
Go repository reaches 837 MB, so the pool never drains and the threshold is the
peak. At 256 MiB (`DefaultDrainThreshold`) the three repositories give up
24–42% of their peak for 3.7–11% more time.

**`MaxInFlightBytes` does not bound memory.** Varying it from 4 MiB to 32 MiB
moves spring-boot's peak RSS by less than run-to-run noise (1,347 / 1,305 /
1,377 / 1,350 MB). It bounds admission; the drain threshold bounds memory.

**Retrieval has to join compound words.** `SplitIdentifier` turns
`DataSourceHealthIndicator` into [data source health indicator], and no token
equals "datasource", which is how Spring spells it in `spring.datasource.url`.
Matching whole tokens alone, "Add retry to the datasource health check" finds
no mention of the class while "data source" finds five; the catalogue therefore
also matches adjacent word pairs written as one word
(`compoundForms` in `internal/context/catalogue.go`).

Edge counts differ enormously by language for a real reason, not a bug. nest
resolves 10,361 references but produces 4,064 edges, because only 4,110 of
those references sit inside an indexed symbol: TypeScript puts most references
at module top level, and 1,929 files yielded 364 functions. A reference outside
any symbol has no source to hang an edge on, and inventing one would corrupt
caller queries.

## Figures other tools publish, measured here

Three figures other code-graph tools publish, measured with the harnesses in
`scripts/bench/` and reported as measured. None of them flatters the tool.

| | |
|---|---|
| Host | Apple M4 Pro, 14 cores (10 performance, 4 efficiency), 25,769,803,776 bytes (24 GiB), macOS 26.5.1: `sysctl -n machdep.cpu.brand_string hw.ncpu hw.memsize` |
| Binary | `CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -o graphlume ./cmd/graphlume`, so `darwin/arm64` native, built by the Go 1.26.11 `darwin/amd64` toolchain |
| Tree | commit `6fadd51` |
| Date | 2026-11-12 |
| Load | The machine is shared. Its one-minute load average on 14 cores is 10 to 33 from other builds, test suites and accuracy runs, and 35 to 55 while the kernel index below runs. An endpoint-security scanner is active throughout |

**Read every time below as a ceiling for this machine, not as what the tool
costs.** The same binary indexed and resolved nest in 16 s at a load of 20 and
takes between 31 s and 249 s to do it at 25 to 50. The lowest load behind any
figure here is 10, so there is no idle figure to put beside these, and none is
estimated.

### Incremental update after two files change

Another code-graph tool's README gives about 2.5 s for a two-file
edit in a project of about 3,000 files, which it names as django, and about 40 s
for a cold build of a repository that size.

```bash
git clone --depth 1 https://github.com/django/django /tmp/bench/django     # 5d6d1f0
scripts/bench/incremental.sh /tmp/bench/graphlume /tmp/bench/django \
    django/utils/text.py django/db/models/query.py
```

The script indexes from empty, then three times appends one function to each of
the two files and runs `graphlume index` and `graphlume resolve`. django at
that commit has 7,100 tracked files, 2,943 of them Python. GraphLume discovered
7,090, indexed 2,475 and extracted 46,390 symbols and 285,396 references.

Three runs of the script, at three loads, with a build that reads and hashes
every file and runs `git status` on every update; [the next
section](#the-update-path-on-django) measures the default, which does neither.
Wall time is `/usr/bin/time`; "printed" is the duration GraphLume reports
itself, which excludes process start.

| | Run A: load 53, the kernel index running | Run B: load 21 | Run C: load 10 |
|---|---|---|---|
| Cold `index` into an empty database, wall | 126.01 s | 30.59 s | 21.52 s |
| The same, as printed | 125,856 ms | 30,506 ms | 21,455 ms |
| Its peak RSS | 593 MB | 695 MB | 722 MB |
| Cold `resolve`, wall | 14.77 s | 9.51 s | 6.73 s |
| `index`, nothing changed, wall | 8.75 s | 6.49 s | 3.03 s |
| **`index` after a two-file edit**, three runs, wall | 23.83 / 10.93 / 8.42 s | 5.21 / 6.04 / 5.15 s | **4.14 / 5.45 / 4.84 s** |
| The same, as printed | 18,264 / 10,199 / 8,106 ms | 5,161 / 5,987 / 5,113 ms | 4,098 / 5,396 / 4,799 ms |
| Its peak RSS | 182 / 178 / 191 MB | 199 / 183 / 191 MB | 198 / 192 / 193 MB |
| `resolve` after each, wall | 7.55 / 4.69 / 2.04 s | 0.68 / 0.71 / 0.66 s | 0.71 / 0.76 / 0.64 s |
| The same, as printed | 2,037 / 1,834 / 1,805 ms | 630 / 671 / 631 ms | 669 / 727 / 607 ms |
| `watch`: edit to logged update | see [`graphlume watch` on django](#graphlume-watch-on-django) | 3.51 / 4.17 / 6.97 s | 4.09 / 2.38 / 2.49 s |

Run C is the quietest of these measurements, and it is still a shared
machine. On it a two-file update is 4.14 s to 5.45 s, or 4.85 s to 6.21 s with the `resolve`
that makes the new edges queryable, against the published 2.5 s. The cold build,
`index` plus `resolve`, is 28.25 s against the published 40 s.

**Most of an update is the pass over the whole tree.** With nothing changed,
`index` takes 3.03 s in run C and 6.49 s in run B to find that out about 7,090
files. Two changed files take 1.1 s to 2.4 s more than that in run C and no more
at all in run B. nest, at 2,756 files and a load of 20, has the same shape:
1.58 s with nothing changed and 1.97 / 1.83 / 1.61 s after a two-file edit,
after a cold index of 13.58 s. So the floor under an update follows the size of
the repository, and `graphlume index --help` says so. What that pass costs per
file on an idle machine is not measured here.

#### The update path on django

The same machine and clone (`a0f2e29`, 7,100 tracked files, 7,090 discovered),
both binaries built the same way. "Every file read" is the build of the table
above, which reads and hashes every file and runs `git status`; "Default" skips
both. The load average is 6 to 8 for the first and 8 to 10 for the second, so
these are again ceilings and not idle figures. Each cell is every run, wall time
from `/usr/bin/time`.

| `graphlume index` on django | Every file read | Default |
|---|---|---|
| Nothing changed | 2.54 / 2.68 / 2.67 s | 0.21 / 0.13 / 0.14 s |
| Two files changed (one function appended to each) | 3.25 / 3.39 / 3.31 s | 1.06 / 1.03 / 0.83 s |
| 200 files changed (one comment line appended to each) | 4.26 / 4.35 s | 1.98 / 2.26 s |
| Peak RSS, nothing changed / two files / 200 files | 58 / 190 / 236 MB | 57 / 188 / 222 MB |
| Cold `index` into an empty database, for scale | 18.82 s | 17.09 s |

Two things set the floor of an update that reads every file, found with `pprof`
on the unchanged-tree path:

| Where an update that changes nothing spends its time, reading every file | Share |
|---|---|
| Opening, reading and hashing every discovered file (`fs.hashFile`) | about half |
| `git status` over every tracked file, to stamp the commit (`Repo.Info` computing a dirty flag nothing read) | about half |
| Reading the previous run's file list, the SQLite open and the schema check | under 50 ms |

The default pays neither. The commit is read from the ref. A file is hashed only
when it might have changed, by the rule in [the CLI reference](CLI.md#index):
same size, same modification time, and both that and the inode change time more
than two seconds older than the previous run. The hash is still the verdict for
every file that fails that test, and `graphlume index --rehash` hashes
everything (0.55 s on this tree, with no status scan).

What still follows the size of the repository and not the edit, for a two-file
change, from the same profile: one `lstat` per file (0.1 s of wall, on 14
cores), and the reference resolution that runs after any change and passes over
every file's references (about 0.35 s for django's 285,396). So the published
2.5 s for two files is met, at 0.8 s to 1.1 s, and the help text says the cost
depends on repository size, because it does.

`graphlume watch` has no one-shot mode, so the script starts it, waits for its
"Watching" line, makes the same two-file edit and times how long watch takes to
log the update. In run C that takes 4.09 s, 2.38 s and 2.49 s, of which watch
reports 3.69 s, 1.97 s and 2.08 s as its own update and 0.3 s is its debounce.
In run B it takes 3.51 s, 4.17 s and 6.97 s.

#### `graphlume watch` on django

The same machine, clone and build method as the update figures above. "Every
file watched" is a watcher built on fsnotify's kqueue backend, which on macOS
opens every file of every watched directory, one descriptor and one `open` at a
time; "Default" is this build's watcher. The load average is 12 to 18 for the
first and 14 to 20 for the second. Three starts each, every run shown. Each
start makes one edit 0.25 s after launch, one in-place append once watching,
and one save by temporary file and rename.

| `graphlume watch` on django, 7,100 files in 3,273 directories | Every file watched | Default |
|---|---|---|
| Start to the "Watching" line | 5.40 / 9.38 / 25.03 s | 1.63 / 1.71 / 1.63 s, and 2.93 / 2.51 / 1.52 s in a second set |
| Open descriptors once watching (`lsof -p` lines) | 10,399 | 3,298 |
| An edit made 0.25 s into start-up | picked up in 2 of 3 starts; never reported in the third | picked up before the banner, 6 of 6 |
| Start to the "Watching" line at a load of 35 to 55 | not printed after 900 s | not measured at that load |
| Ctrl-C while still registering | 2 s in: exits 4.42 s later. 3 s in: 0.89 s later | 0.5 s in: 0.15 s later. 1.0 s in: 0.08 s later |
| Ctrl-C once watching | 0.30 to 0.41 s | 0.07 to 0.26 s |
| In-place append to logged update | 1.80 / 7.34 / 3.04 s | 1.03 / 1.42 / 2.24 s |
| Save by rename to logged update | 3.51 / 6.47 / 2.37 s | 1.00 / 0.79 / 0.71 s |

How the default watcher works, in the order it matters:

- **No file is opened.** On macOS the watcher holds one descriptor per
  directory (kqueue on the directory, which reports entries created, deleted
  and renamed) and finds a file rewritten in place by a sweep of sizes and
  modification times. fsnotify's kqueue backend opens every file, and has no
  option not to. On Linux and Windows the watcher holds directories only.
- **The sweep is the price**, and it is why an in-place write is not instant on
  macOS: it runs every two seconds, or less often when a sweep takes long, so
  that it costs at most about a twentieth of one core. A sweep that lists every
  directory takes 7.3 to 8.9 s to report an in-place append on this loaded
  machine; the sweep is one `lstat` per remembered file instead (the kernel
  already reports changes to a listing), which gives the 1.0 to 2.2 s in the
  table.
- **Registration takes a context and reports progress**, every two seconds on
  standard error. None is printed here because no start takes two seconds to
  register.
- **The reconcile runs after registration**, so an edit made during start-up
  is in the index before the banner.
- **Hidden tool directories are not watched**, by the same rule that keeps
  them out of the index.

What a descriptor per directory implies: the bound on a repository that can be
watched on macOS is its directory count against the open-file limit
(`kern.maxfilesperproc` is 92,160 on this machine and 10,240 by default on
older systems), not directories plus files. `watch` stops at 8,192
directories with a named error before the kernel refuses.

Not measured: an idle machine, the sweep's standing cost in CPU over a long
session, and Linux or Windows, where the kernel reports changes through
directory watches and there is no sweep.

### Linux kernel, cold index

Other code-graph tools publish 45 s, 3 minutes, and under 12 minutes on
2 cores.

```bash
git clone --depth 1 https://github.com/torvalds/linux /tmp/bench/linux     # 9d66f26, 2026-11-11
scripts/bench/cold-index.sh /tmp/bench/graphlume /tmp/bench/linux
```

One run, with a build that bounds each parse by a five-second wall-clock
timeout and refuses files over 256 KiB; [the parse budget](#the-parse-budget)
describes how this build bounds a parse instead, and this build has not indexed
the kernel. The run takes over two hours, so there is no quieter second
figure. The script runs with a 90-minute limit per step; the `index` step is
allowed to run past it, and `resolve` is timed separately with the same
`/usr/bin/time -l` wrapper.

| Step | Wall | User CPU | System CPU | Peak RSS |
|---|---|---|---|---|
| `graphlume init` | 0.07 s | 0.04 s | 0.02 s | 37 MB |
| `graphlume index` | 7,621.05 s (2 h 7 min) | 51,979.46 s | 3,053.76 s | 9,636 MB |
| `graphlume resolve` | 232.66 s (3 min 53 s) | 172.33 s | 31.20 s | 253 MB |

`/usr/bin/time -l` also reports a peak memory footprint of 32,683,990,704 bytes
for the index step, on a machine with 24 GB. Resident memory moves between 2 GB
and 10 GB while it runs.

| | |
|---|---|
| Tracked files | 97,362, 1.8 GB |
| Files discovered | 97,348. The volume is case-insensitive, and 13 pairs of kernel file names differ only in case, so 13 files are missing from the checkout |
| Files indexed | 72,041: 59,155 parsed cleanly, 12,886 with syntax errors the parser recovered from |
| Files skipped | 23,702: 17,193 in a language with no tags query (YAML, reStructuredText, Makefiles, assembly), 6,024 with no grammar, 280 over the 256 KiB parse limit, 58 over the 2 MiB read limit, 106 symlinks and 41 others |
| **Parse failures** | **1,605, all C, all `parse exceeded 5s`** |
| Symbols | 2,835,035 reported by `index`; `status` counts 2,992,164 |
| References | 4,815,340, of which 2,433,528 name something the index defines |
| References resolved | 971,304 (39.9%) |
| Relationships | 882,603. The resolved references are 615,468 high, 304,551 medium and 51,285 low |
| `index.db` | 3,177,115,648 bytes after `index`, 3,540,942,848 bytes after `resolve` |

Progress through the tree, from the indexer's own log:

| Files done | 10% | 20% | 30% | 40% | 50% | 60% | 70% | 80% | 90% | 100% |
|---|---|---|---|---|---|---|---|---|---|---|
| Elapsed | 2:12 | 2:42 | 3:51 | 17:45 | 31:01 | 56:06 | 1:38:57 | 1:50:41 | 2:02:56 | 2:06:22 |

The first 31% of the files are `Documentation/` and `arch/`. `drivers/` is the
next 39%, 38,199 files, and takes about an hour and a half.

**This is between 11 and 175 times the published figures, and the load on the
machine does not account for it.** The load average is 19 when the run starts
and between 35 and 55 for most of it, with accuracy runs, a second index and a
test suite overlapping. But the run itself is the largest consumer on the
machine: it uses 6.8 cores on average with a worker pool of four. Three causes
are measured; [the parse budget](#the-parse-budget) and [the two error-recovery
routes](#two-error-recovery-routes) are how this build answers each.

**1. A table after a macro call parses in at least quadratic time.** A C file with one
macro call at file scope followed by an array of *n* literals, indexed alone:

| *n* | 32 | 64 | 128 | 256 |
|---|---|---|---|---|
| With `MODULE_LICENSE("GPL");` above the array | 0.15 s | 0.33 s | 1.39 s | over 5 s, abandoned |
| The array alone | | | | 0.14 s |

```bash
mkdir /tmp/table && cd /tmp/table
{ echo 'MODULE_LICENSE("GPL");'; echo; echo 'const unsigned char table[256] = {'
  for i in $(seq 0 255); do printf '0x%02x, ' "$i"; done; echo; echo '};'; } > table.c
graphlume init && graphlume index
graphlume query "SELECT parse_status, parse_error FROM files WHERE index_run_id = :run"
# failed  parse exceeded 5s: context deadline exceeded
```

That is a 1.6 KB file. The kernel's own `lib/bitrev.c`, 1,893 bytes and a
256-entry table, fails the same way with 4.85 s of CPU. The reproduction above
fails at a load of 9 too, with 5.10 s of CPU, so this one is not the load. A
driver is a handful of `MODULE_*` macros and its tables, and 1,373 of the 1,605
failures are under `drivers/`. Two samples of the running process, 80 minutes
apart, both find its time inside the parser's ambiguity handling
(`gotreesitter.(*gssMainPreflight).canReach`,
`(*Parser).rawStackEntryErrorCost`), not in SQLite.

**2. A parse that times out is abandoned, not stopped.** That build returns
after five seconds and leaves the parse running, because the parser it uses has
no cancellation hook. The worker takes the next file while the
abandoned parse keeps its core and its memory. That is consistent with a pool of
four using 6.8 cores, and it is the likely source of the 32 GB footprint. An
abandoned parse also takes CPU from the live ones, which pushes them towards
their own timeout.

**3. The timeout is wall-clock, so the index depends on what else the machine is
doing.** `drivers/staging/nvec/nvec-keytable.h`, 2,945 bytes, is one of the
1,605 failures in this run. Indexed alone on a quieter machine it parses in
2.3 s, with errors the parser recovers from. A file that fails has no symbols,
so two runs over the same tree at different loads produce different indexes.

Away from those files the parser is not unusually slow on C. Eleven kernel
sources of 27 KB to 237 KB, each indexed alone on the loaded machine, take 0.8 s
to 3.0 s, which is 7 to 28 ms per KB including process start. Seven Python, Go,
Java and TypeScript files of 45 KB to 126 KB measured the same way take 5 to
14 ms per KB.

### Impact-analysis accuracy

Another code-graph tool publishes F1 0.693. The measurement here asks a different and
harder question, so the two are not comparable. The method, its bias, the full
tables and what the run found are in `docs/BENCHMARKING.md`, section 6.

```bash
git clone --depth 400 https://github.com/pallets/flask /tmp/bench/flask
go run -tags bench ./scripts/bench/impactaccuracy \
    -repo /tmp/bench/flask -bin /tmp/bench/graphlume -n 1000 -budget 20m
```

For each commit that changed at least two source files, the harness indexes the
parent, asks `impact` about the symbols the commit changed in one file,
and compares the files in the answer with the other files the commit changed.
One non-test seed file per commit, the default confidence floor, every row
returned:

| Repository | Language | Commits | Precision | Recall | F1 | Same-directory rule, F1 |
|---|---|---|---|---|---|---|
| GraphLume | Go | 14 | 10.4% | 16.0% | 0.126 | 0.034 |
| flask | Python | 74 | 15.9% | 15.4% | 0.156 | 0.149 |
| gson | Java | 57 | 8.9% | 15.2% | 0.112 | 0.065 |
| nest | TypeScript | 12 | 0.0% | 0.0% | 0.000 | 0.000 |
| zod | TypeScript | 10 | 25.0% | 5.0% | 0.083 | 0.055 |

The truth is what a developer changed, not what depends on what, so both
precision and recall are understated: files that change together are not all
dependents, and a commit does not touch every dependent. The last column is the
control for that: predict the other source files in the seed's directory, with
no index at all. The tool is ahead of it in this table, except on nest where
both score nothing. It is behind it on flask, gson and zod when every changed
file is taken as a seed in turn. Between 43% and 83% of the seeds on the four
public repositories predicted no file at all.

## Indexing the foreign keys that reference `symbols(id)`

The schema (`internal/store/migrations/0001_schema.sql`) indexes every column that
references `symbols(id)`. It is the largest single performance decision in the
schema, and the trade it makes is deliberate: a little more disk and a slower full
build, in exchange for an incremental re-index that does not degrade with
repository size.

Measured on a 5,000-file / 200,000-symbol synthetic Go corpus, `CGO_ENABLED=0`,
3 trials, with the same `darwin/amd64`-under-Rosetta binary on both sides — so
the ratio is like-for-like even though the absolute seconds are emulated:

| | Without the indexes | With them | Change |
|---|---|---|---|
| One-file re-index | 5.10 s | 0.80 s | **6.3x faster** |
| Database size | 105.9 MB | 112.4 MB | +6.13% |
| Full index | 28.9 s | 32.1 s | +11% |
| Symbols indexed | 200,000 | 200,000 | identical |

The identical symbol count is the part that makes the rest trustworthy: the
indexes change how rows are found, not which rows exist.

The asymmetry is the point. A full index is something a developer runs rarely and
knowingly; a one-file re-index is what `graphlume watch` runs on every save. Paying
11% on the rare operation to take 84% off the common one is the right side of that
trade, and 6% more disk is the cheapest currency available.

## A full re-index costs about what a first index costs

Four consecutive `graphlume index --full` runs over GraphLume's own source, on one
database, with no source changes between them:

| Run | Duration | `index.db` after |
|---|---|---|
| 1 — into an empty database | 1.83 s | 8.26 MB |
| 2 | 2.11 s | 16.23 MB |
| 3 | 2.12 s | 17.75 MB |
| 4 | 2.16 s | 18.03 MB |

Retiring the previous revision costs about 15% of a run. It is a cascade delete,
and the foreign-key indexes above give it indexes to use instead of table scans,
so `index --full` is safe to run from a loop or a hook.

`graphlume init --force` followed by `graphlume index` costs 1.75 s against
2.11 s for `graphlume index --full` on the same tree. It is a different
*operation* — it discards everything already indexed, so it is a rebuild rather
than a refresh — not a faster one.

**The file settles at about twice one revision.** A full index writes the new
revision beside the old one and deletes the old one when it publishes, so the
old revision's pages become free. On an index this small they stay in the file,
because there are too few to be worth returning (below), and the next run
reuses them, which is why the size above stops growing after the second run.

### Automatic compaction

An index this build creates returns free space to the file system by itself.
Nothing has to be run, and there is no flag for it.

- **How.** `graphlume init` creates `index.db` with `auto_vacuum = INCREMENTAL`
  (it has to be chosen before the file's first page is written, so it is in the
  connection string ahead of `journal_mode = WAL`). After each write that can
  free pages — a published full index, an incremental update by `index` or
  `watch`, and `resolve` — `store.Compact` checks the free list.
- **When.** Only when the free pages are at least **64 MiB and at least 20% of
  the file** (`store.CompactMinFreeBytes`, `store.CompactMinFreeFraction`).
  Below that they are cheaper to leave for the next write to reuse.
- **In steps.** `PRAGMA incremental_vacuum` frees 2,048 pages per statement, each
  its own short write transaction, for at most 3 seconds per write. What is left
  is reclaimed after a later write, so a `watch` session is never held up for
  long.
- **Readers keep reading.** The index is in WAL mode, so the MCP server and the
  portal read their snapshot throughout. The write-ahead log is then
  checkpointed with `TRUNCATE` when no reader is in the way, and with `PASSIVE`
  otherwise; the checkpoint never waits for a reader.
- **Reported.** `graphlume status` prints `Free space: 12.3 MiB (reclaimed
  automatically)` and `Auto-vacuum: incremental`. `index` and `resolve` print
  one line, `Reclaimed 340.2 MiB of free space in the index.`, when they gave
  space back and nothing when they did not; with `--json` they carry
  `reclaimed_bytes` instead. `watch` logs it.
- **Opting out.** `index.auto_compact: false` in `.graphlume.yml` leaves free
  pages in the file.
- **Other indexes.** An index made without auto-vacuum (by hand, or by another
  tool) keeps working and is left alone; `status` says so, and
  `graphlume doctor --vacuum` compacts it.

Measured on the Go toolchain's own source (`src/` of Go 1.26.11, 7,271 files
discovered, 7,111 indexed), three consecutive `graphlume index --full` runs on
one database, `GOMAXPROCS=2 nice -n 19`, sizes from `stat`:

| Run | `auto_compact: false` | Default | Reclaimed by the run |
|---|---|---|---|
| 1 — into an empty database | 352.1 MiB | 352.4 MiB | nothing (no free pages) |
| 2 | 711.5 MiB | 364.4 MiB | 349.1 MiB |
| 3 | 728.7 MiB | 408.2 MiB | 320.0 MiB, then the 3 s budget runs out |
| `status` after run 3 | `Free space: 363.6 MiB` | `Free space: 43.6 MiB (reclaimed automatically)` | |

The `-wal` file is empty after every run in both columns. Run 3 stops at its
time budget with 43.6 MiB still free, which is below the 64 MiB threshold, so
those pages stay for the next run to reuse. Run times are 176–498 s without
compaction and 264–288 s with it, on a machine running test suites alongside,
so they say nothing about the cost of the reclaim beyond its 3 s bound.

## Resolution

**The resolver itself: 842 ns per reference.** From `BenchmarkResolveFile` in
`internal/resolve/infer`, which binds 500 references against a 5,000-symbol
index:

```bash
go test -run '^$' -bench BenchmarkResolveFile -benchmem -count=5 ./internal/resolve/infer/
```

Five runs: 427.4 / 420.1 / 420.8 / 421.6 / 416.6 µs per operation, 785 KB and
2,003 allocations per operation. Divided by the 500 references in the fixture,
that is 833–855 ns per reference.

**Do not read that as the cost of resolving a repository.** `graphlume resolve`
over GraphLume's own source — 18,429 references, 7,708 edges written, plus
data-access and config binding — takes 627 / 507 / 518 ms over three samples, each
after a fresh full index. That is **28 µs per reference end to end, roughly 33x
the 842 ns inside the resolver.** The wall-clock cost is dominated by reading
references out of SQLite and writing edges back, not by the binding logic.
Optimising the resolver further would not move it. Any statement of the form
"N references resolve in T seconds" derived from the microbenchmark alone is
wrong by that factor.

The design the microbenchmark measures is the result of refusing two obvious
alternatives — per-reference SQL would be one query per reference, and a nested
loop would be the product of the two counts. References stream file by file, so
nothing accumulates, and edges flush in batches of `DefaultResolveBatch = 2000`
rather than being collected.

### Memory

Resolution's peak memory is flat in repository size. Loading the run's entire
symbol index into one map before anything is bound would make memory track the
repository rather than the work; the first column measures that design, the
second this build.

Synthetic Go corpus, ~15 references per file, `graphlume resolve` alone, peak RSS:

| files | symbols | one map | this build |
|---|---|---|---|
| 1,000 | 29,000 | 71 MB | 68 MB |
| 5,000 | 145,000 | 205 MB | 168 MB |
| 25,000 | 725,000 | 500 MB | 205 MB |
| 50,000 | 1,450,000 | 819 MB | 208 MB |

Doubling the tree from 25,000 to 50,000 files costs 3 MB. Go heap alone is
25 MB at 25,000 files against 132 MB for the single map; what remains in RSS is
the binary image and SQLite's page cache, both constant. Resolution time is the
same for both (1.01x–1.08x) and the edges and references produced are
byte-identical at every size.

Three bounds do it, and each removes a term that grew with the repository:
`registry.CandidateCache` reads candidates per page behind an LRU bounded by
candidate rows; reads stop at `infer.MaxCandidates + 1` rows per name, because
the resolver abandons an over-subscribed name on the count alone; and the file
list is paged by key rather than materialised.

### On superlinearity

Resolution outcomes are written in key order, and resolution scales linearly.
Measured on this machine, native `darwin/arm64`, `graphlume resolve` on
prebuilt indexes:

| corpus | 25x input costs | exponent |
|---|---|---|
| ~15 refs/file (29k → 725k symbols) | 22.7x | 0.97 |
| ~100 refs/file (100k → 2.5M references) | 25.1x | 1.00 |
| ~100 refs/file, page cache cut to 8 MB | 28.3x | 1.04 |

The third row is the interesting one. The mechanism that would make resolution
superlinear is index B-trees outgrowing the 64 MB page cache, so starving the
cache eightfold should exaggerate it; it moves the exponent from 1.00 to 1.04.
On every corpus measured here, up to 2.5 million references, resolution is
linear. `TestResolveScalesLinearly` reports 1.27x growth in per-reference cost
for 4x the references against a 2.5x bound.

### What it actually resolves

On GraphLume's own source, identically across all three trials:

```
  References:  18429
  Resolved:    7880 (42.8%)
  Ambiguous:   903
  Unresolved:  9646  (no definition of that name is indexed: code outside this
               repository, or code in it that the index did not extract)
  Edges:       7708
  Data access: 329 statement(s), 320 bound to a declared table, 320 edges
```

On the payment fixture, `graphlume resolve` reconstructs the callback flow from
source — `handleCallback -> updateStatus -> {save, findById}` — in 25 / 36 / 38 ms:

```
Resolved pay2 (revision 1)
  References:  55
  Resolved:    16 (29.1%)
  Ambiguous:   10
  Unresolved:  29  (no definition of that name is indexed: code outside this
               repository, or code in it that the index did not extract)
  Edges:       16
    high:    16
  Data access: 7 statement(s), 7 bound to a declared table, 7 edges
```

Those 29 references are genuinely external — Spring, JDBC, test frameworks — and
are recorded as unresolved rather than hidden. "How much of this repository can we
actually resolve" is a question the product has to answer honestly, so the
unresolved count is a published fact, not a swept-under number.

The `Data access` line is not decoration: `resolve` is where staged table accesses
and config reads are bound too, so its duration covers more than reference
binding.

## Importing a compiler-backed index

Measured on GraphLume's own source with `scip-go` 0.2.7, whose output on this tree
is a 7.2 MB index:

```
scip-go 0.2.7: 288 document(s), 251 matched
symbols: 4071 matched, 13729 with no counterpart in the index
edges:   14758 new, 0 upgraded from heuristic, 918 already known
         67787 reference(s) point outside the indexed repository
         3050 reference(s) the indexer attributed to a file rather than a definition were skipped
duration: 600ms
```

Three trials: 600 / 575 / 871 ms. The 87% document match rate is stable; the
unmatched 37 are paths under Go's build cache, which the index never saw.

`0 upgraded from heuristic` is the expected result, not a failure — SCIP records
that one symbol references another, not that it *calls* it, so compiler facts
arrive as `REFERENCES` edges alongside the resolver's `CALLS` edges rather than
replacing them.

## Parallelism and peak memory

Measured on the 5,000-file synthetic corpus, `/usr/bin/time -l` around
`graphlume index --full --workers N`, one run each:

| Workers | Peak RSS | Elapsed |
|---|---|---|
| 1 | 255 MB | 56.3 s |
| 2 | 255 MB | 45.9 s |
| 4 | 292 MB | 33.8 s |
| 8 | 310 MB | 31.9 s |
| 10 | 349 MB | 27.2 s |
| 14 | 386 MB | 30.3 s |

On uniformly small files memory moves little with the worker count — at most
about 40 MB of peak RSS per extra worker — and throughput keeps improving to 10
workers. So the default worker count scales with the machine, inside a memory
bound (`pipeline.WorkersFor`):

```
by CPU:     half the CPUs, at least 4 (or every CPU, when there are fewer), at most 8
by memory:  (soft memory limit − 256 MiB) / 64 MiB, at least 1
workers  =  the smaller of the two
```

The soft memory limit is the one the run uses: 768 MiB by default, or
`--memory-limit`, `GRAPHLUME_MEMORY_LIMIT` or `GOMEMLIMIT`; `--memory-limit off`
leaves only the CPU bound. 256 MiB is the one-worker peak in the table above,
and 64 MiB per worker is a conservative budget against the 40 MB measured here,
because a real repository mixes in large files. At the default limit the memory
bound is 8, so:

| CPUs | Default workers | At `--memory-limit 512MiB` | At `--memory-limit 400MiB` |
|---|---|---|---|
| 2 | 2 | 2 | 2 |
| 4 | 4 | 4 | 2 |
| 8 | 4 | 4 | 2 |
| 10 | 5 | 4 | 2 |
| 14 | 7 | 4 | 2 |
| 16 or more | 8 | 4 | 2 |

`--workers N` overrides the default.

**Peak memory tracks worker count and file size, not repository size.** A worker
count bounds concurrency; it does not bound memory, because file sizes vary by
orders of magnitude. Eight workers that each happen to pick up a megabyte of
generated code allocate gigabytes between them, and the *scheduler* decides
when that happens — which makes peak memory a property of file ordering. That is
the shape a uniform corpus cannot reproduce, and it is why the default stops at
8 and stays inside the memory limit rather than following the CPU count.

To go further on a machine with memory to spare, raise the memory limit or pass
`--workers`, and measure your own tree — the knee moves with average file size.

### What bounds memory instead of the worker count

`pipeline.memBudget`. Workers acquire budget proportional to the bytes they are
about to parse, *before* reading the file:

- Small files run wide.
- A large file waits until the budget frees up, then effectively runs alone.
- A file larger than the entire budget is clamped and admitted alone rather than
  deadlocking. A file that can never be indexed is worse than a brief spike.

`DefaultInFlightBytes = 32 MiB` (`internal/pipeline/pipeline.go`).
That is 32 MiB of *source*, not of memory — tree-sitter trees run one to two
orders of magnitude larger than their input. Enough to keep every worker busy on
ordinary code; low enough that a directory of generated files cannot exhaust the
machine. Throughput on ordinary code is unchanged, because ordinary files are
small.

### The size limit

There is one size limit, **2 MiB** (`config.DefaultMaxFileBytes`): a file over it
is not read, parsed or text-indexed. A repository changes it with
`index.max_file_bytes` in `.graphlume.yml`, between 1 KiB and 8 MiB.

The same limit applies to reading and to parsing. A lower parse cap would drop
hand-written files at the centre of real programs: in redis nine files are over
256 KiB, among them `src/server.c` (371 KB), `src/server.h`, `src/module.c`
(693 KB), `src/redis-cli.c`, `src/t_stream.c`, `src/t_hash.c` and
`src/cluster_legacy.c`.

What a large file costs, with gotreesitter v0.57.0, one file per process, on the
machine described at the top of this document (`/usr/bin/time -l`, peak
resident memory of the whole process):

| File | Size | Parse time | Peak RSS |
|---|---|---|---|
| redis `src/module.c` | 693 KB | 0.33 s | not measured alone; 131 MB allocated |
| five redis C sources concatenated | 1,647 KB | 0.89 s | 170 MB |
| `src/module.c` three times over | 2,080 KB | 0.87 s | 298 MB |
| a 316 KB C# test file six times over | 1,897 KB | 4.20 s | 269 MB |
| Newtonsoft.Json `large.json` | 6,902 KB | 1.52 s | 357 MB |

So a file at the limit costs about 300 MB while it is parsed, roughly 150 bytes
of process memory per source byte. To keep that from being paid once per worker,
a file over 512 KiB reserves eight times its size from the in-flight budget
(`pipeline.admissionWeight`): against the default 32 MiB that admits two 2 MiB
files together and one 4 MiB file alone, while ordinary files still run side by
side.

The concatenated files are synthetic. No repository with many real 1 to 2 MiB
sources is measured here, and a grammar that allocates more per byte than C or C#
would cost more than the table shows.

A file over the limit is recorded in the `files` table with `parse_status =
'skipped'` and `parse_error = 'too_large: N bytes, over the L byte limit
(index.max_file_bytes)'`. `graphlume index` and `graphlume status` count such
files and name the ten largest.

### The parse budget

A parse is bounded by work, not by time. gotreesitter counts its own iterations,
nodes, stack depth and arena bytes against limits derived from the length of the
source (30 iterations and 52 nodes per byte, with floors of 10,000 and 300,000;
512 MiB of arena or 512 bytes per source byte, whichever is larger) and stops
itself when one is reached. The parser returns the tree it had built, the
definitions in it are indexed, and the file is recorded as `partial` with
`budget_exceeded:` and the byte and line it stopped at. Because the limits count
work, the same file gets the same verdict at any load. Ordinary source is
nowhere near them: of eight large redis files probed, the closest is
`src/crc16_slottable.h` at 82,000 iterations against a limit of 3.4 million.

A wall-clock ceiling of 60 s (`parse.DefaultParseCeiling`) remains as a last
resort, for an input that stays inside every work limit and still does not
finish. It is polled by the parser, so a parse it stops does stop. A file it
stops is recorded the same way, and its text says the limit is the clock,
because that verdict can differ between machines. The slowest file measured
takes 4.2 s.

A clock alone is not a sufficient bound. With five seconds of wall-clock time,
enforced by returning from the call and leaving the parse running, two
consequences are measured: the files that fail depend on the load (six, then
eight of redis's C files; 40, then 39 of Newtonsoft.Json's), and the abandoned
parses keep their cores and their memory.

**What is still unbounded.** The work limits do not count time spent inside one
parser step, which is where gotreesitter v0.50.1 spends eight seconds on a 1.6 KB
file; only the clock catches a case like that, and the clock is the part that
depends on the machine. The clock and the cancellation flag are polled by the
parser between steps and nothing preempts it, so a step that runs long delays
the stop. In the cases measured the delay is small: a parse given a 1 ms
ceiling returns after 75 ms on an 800 KB file, and v0.50.1 given a 1 s timeout
on its quadratic case returns after 1.05 s. And the queries that run over the
finished tree have a per-pattern work budget inside the library but no clock.
Both are bounded in practice by the size limit.

### Two error-recovery routes

gotreesitter recovers from input a grammar does not accept in one of two ways: a
port of the C runtime's cost-based recovery, which is the default for the
grammars certified for it, and a resynchronising recovery. They fail on
different files:

| gotreesitter | `MODULE_LICENSE` then a 256-entry table (1.6 KB C) | redis `src/bitops.c` (115 KB) | Newtonsoft.Json `JsonSchemaGenerator.cs` (21.7 KB) |
|---|---|---|---|
| v0.50.1 | 7.7 s | 1.9 s, read to the end | not finished when a 30 s timeout stopped it |
| v0.52.0 to v0.55.1 | 0.05 s | 1.1 to 1.3 s, read to the end | not finished when a 20 s timeout stopped it |
| v0.56.0, v0.57.0 | 0.04 to 0.05 s | stops at byte 1,870 with `invariant_violation` | 0.36 to 0.45 s |

No release reads all three. This build uses v0.57.0, and when the default route
stops before the end of a file for any reason but the clock, it parses the file
again by the other route and keeps the attempt that read further. The second
route is selected by loading a second copy of the grammar with its
`CRecoveryCostCompetitionEnabledByDefault` field cleared, so nothing global is
changed and the two routes cannot affect each other. On redis the default route
stops early on 11 of 1,025 C files and the second route reads all 11 to the end.
A second attempt costs one more parse of that file and a second copy of that
language's parse tables in memory.

### redis and Newtonsoft.Json: a parse clock against a parse budget

Shallow clones of redis (`b83c9c1`, 1,958 files discovered) and
JamesNK/Newtonsoft.Json (`52fa3ae`, 1,176 files), cold `graphlume index` under
`/usr/bin/time -l`, arm64 binaries, four workers. "Clock" is a build with
gotreesitter v0.50.1, a five-second parse clock and a 256 KiB parse cap;
"Budget" is this build's parser, work budget and 2 MiB limit. The machine is
shared and its one-minute load average is 13 to 20 throughout, so read the wall
times as a comparison on one machine in one quarter of an hour, not as figures
for either build. Each build runs twice, alternating.

| redis | Clock | Budget |
|---|---|---|
| Wall time | 27.6 s, 27.9 s | 19.7 s, 17.1 s |
| User CPU | 110 s, 116 s | 27.1 s, 26.8 s |
| Peak RSS | 3.12 GB, 3.35 GB | 0.73 GB, 0.76 GB |
| Files parsed | 1,008 | 1,025 |
| Parse failures | 8, all `parse exceeded 5s` (6 in a run at a load of 7) | 0 |
| Skipped for size | 9, not named | 0 |
| Searchable as text only | 0 of 834 files in languages with no definition query | 834 |
| Parsed with syntax errors | not recorded | 408, each with a count and a line |
| Symbols, as `index` reports them | 11,609 | 15,287 |
| Symbols, as `status` counts them | 25,279 | 28,843 |

| Newtonsoft.Json | Clock | Budget |
|---|---|---|
| Wall time | 115.0 s (120.9 s at a load of 7) | 6.5 s, 6.4 s |
| User CPU | 679 s | 16.1 s, 16.4 s |
| Peak RSS | 2.87 GB | 0.63 GB, 0.61 GB |
| Files parsed | 916 | 956 |
| Parse failures | 39, all `parse exceeded 5s` (40 in the other run) | 0 |
| Skipped for size | 2: a 316 KB test file over the parse cap, and `large.json` | 1: `large.json` (6.6 MiB), named |
| Symbols, as `index` reports them | 6,634 | 10,221 |
| Symbols, as `status` counts them | 6,947 | 10,534 |

The two "Budget" runs of each repository record the same counts, and
`graphlume index --full --digest` prints the same registry digest twice on
Newtonsoft.Json and with one worker and with eight on redis. The two "Clock"
runs of each disagree on which files fail.

Where the time goes: with the parser library alone at v0.57.0, redis indexes in
8.5 s at the same load. The other nine to eleven seconds are the 834 text-only
files written to the full-text index (5.9 MB of JSON and Tcl among them) and the
nine large C files that are parsed. That is the price of being able to find a
name in them.

This build has not indexed the Linux kernel; the kernel figures above are from
the clock build.

## Two more allocation controls

### Arena draining

`gotreesitter` pools the arenas it allocates for parse trees, and its own
documentation is explicit that pooled arenas are strong Go references that are
not collected until explicitly drained. A batch scan over a repository is exactly
the case that warning describes: left undrained, the arena for every file ever
parsed stays reachable, so peak memory becomes the sum of every parse rather than
the largest few.

The pipeline drains every `parse.DrainInterval = 128` files — but only when the
heap warrants it. `Runner.maybeDrain` samples `runtime.MemStats` and returns
immediately unless `HeapInuse` exceeds `DefaultDrainThreshold = 256 MiB`
(`internal/parse/arena.go` and `internal/pipeline/pipeline.go`).

Sampling the heap first makes the cost proportional to the problem: on a
repository that never grows large, nothing is ever drained and there is no cost at
all. `ReadMemStats` stops the world briefly, which is why it runs once per 128
files rather than per file. There is no unconditional periodic drain, and no
reproducible measurement of one is given here.

There is also a final unconditional drain when a run ends. Without it, the
indexer's peak footprint stays resident for the lifetime of a long-running
`graphlume watch`.

### Tagger pools

Draining the library's pools is not sufficient. A pooled `Tagger` owns a live
`Parser`, so its arena is **not** in `gotreesitter`'s pool and is not reachable by
`DrainArenaPools`. `registry.DrainPools` (via the `parse.Drainable` interface)
drains both. The pool is a channel rather than a `sync.Pool` specifically because
it must be evictable on demand — `sync.Pool` gives the GC the decision, and the GC
will not free anything, because the pool holds strong references.

A related guard: `parse.PoolReturnLimit = 128 KiB`
(`internal/parse/generic.go:27`). A Tagger that has parsed a large file has grown
an arena that never shrinks, so it is dropped rather than returned to the pool.

## Graph traversal

From `internal/graph/bench_test.go` on its 100,000-symbol / 500,000-edge fixture:

```bash
go test -run '^$' -bench BenchmarkTraverse -benchtime 30x ./internal/graph/
```

p95 wall time for a depth-3 walk from 100 roots, by node cap:

| `MaxNodes` | p95 | Nodes reached |
|---|---|---|
| 1,000 | 3.0 ms | 1,000 |
| 10,000 | 27.1 ms | 10,000 |
| 50,000 | 78.0 ms | 28,193 |

A single-root depth-3 walk is 0.53 ms p95, reaching 199 nodes.

The traversal is level-synchronous: each BFS frontier becomes one indexed
`src_id IN (...)` query, chunked to stay inside SQLite's variable limit, so a
depth-3 walk costs 3 round trips rather than one per node discovered.

**`MaxNodes`, not depth, is what keeps this interactive.** The one shape that
ignores the bound proves it: `Direction=Both`, depth 3, no filters, no effective
node cap reaches 99,821 of the 100,000 symbols in **1.62 s p95**. That is a
whole-graph load wearing a traversal's clothes, which is why `MaxNodes` defaults
to a small value rather than to unlimited.

## Git operations

Measured on the synthetic 500-commit, 200-file repository built by
`internal/acquire/git/bench_test.go`, go-git v5.19.2, three runs each:

```bash
go test -run '^$' -bench . -benchtime 1x -count=3 ./internal/acquire/git/
```

`-benchtime 1x` is deliberate: several of these take seconds per operation, so the
default 1-second budget runs them once anyway, and pinning it to one iteration
makes the cheap and expensive rows comparable. The column below is the median of
the three.

| Operation | Time (median of 3) |
|---|---|
| `Open` | 1.3 ms |
| `Info` (includes worktree scan) | 14.9 ms |
| `Status` | 12.6 ms |
| `ChangedSince` HEAD~1..HEAD | 18.5 ms |
| `ChangedSince` HEAD~200..HEAD | 72.1 ms |
| `ChangedSince` from empty | 7.8 ms |
| `RecentCommitsMeta(50)` | 15.0 ms |
| `RecentCommits(50)` with paths | 698 ms |
| `RecentCommits(500)` with paths | 7.3 s |
| `FileLastModified`, recent file | 3.9 ms |
| `FileLastModified`, absent path | 594 ms (full 500-commit walk) |
| go-git `Blame`, one file | 2.05 s |

The number governing all of these is the tree diff, CPU-bound in go-git's tree
decoding rather than I/O-bound. That is why `RecentCommitsMeta` exists — 15 ms
against 698 ms for the same 50 commits, a factor of **47** here — and why callers
needing per-commit paths for more than a few dozen commits should persist them
rather than recompute.

Blame is deliberately **not exposed**. Two seconds for one file on a 500-commit
repository is not a per-file cost an indexer can pay, and it grows with history.
`BenchmarkGoGitBlame` stays in the tree as the evidence so the decision can be
revisited against measurements rather than impressions.

## Rename and edit

`graphlume rename` does no indexing of its own: it reads the committed index,
then locates each site on its line, splices bottom-up, and writes each file once
atomically. The two costs worth knowing are measured on the machine above
(`darwin/amd64` under Rosetta 2, so the absolute numbers are the emulated view):

```bash
go test -run '^$' -bench . -benchmem -count=3 ./internal/edit/
```

| Benchmark | Cost |
|---|---|
| `BenchmarkLocate` — find one identifier on line 2,001 of a 2,000-line file | 39.1-41.8 µs/op, 21 B, 2 allocations |
| `BenchmarkLocateAndSplice1MiB2000Sites` — locate and splice 2,000 sites in a 1 MiB file | 3.51-3.60 ms/op (295-303 MB/s), 1.30 MB, 4,004 allocations |

The second shape is every site the command's default `--max-sites 2000` allows,
in a single 1 MiB file; the locate-and-splice is designed to be linear in file
size and site count. Writing adds one hash check, one
temporary file and one rename per changed file.

A dry run end to end, on a copy of `fixtures/payment-platform` that is already
indexed and resolved (`graphlume rename updateStatus applyStatus`, five runs):
wall time 0.09-0.11 s, most of it process start and opening the index. A dry
run reads the database only.

## Other measured costs

```bash
go test -run '^$' -bench . -benchmem ./internal/observe/ ./internal/errs/
```

| Thing | Cost |
|---|---|
| `StartSpan`, telemetry disabled | 2.7 ns/op, zero allocations |
| `StartSpan`, disabled, with attributes | 76.9 ns/op, zero allocations |
| `SpanSetAttr`, disabled | 4.5 ns/op, zero allocations |
| `SpanRecordError`, disabled | 2.3 ns/op, zero allocations |
| `MetricRecord`, disabled | 7.1 ns/op, zero allocations |
| The safe attribute constructors | 32.4 ns/op, zero allocations |
| `StartSpan`, telemetry **enabled** | 8,345 ns/op, 4,395 B, 10 allocations |
| `MetricRecord`, telemetry **enabled** | 203 ns/op, 40 B, 2 allocations |
| `errs.KindOf`, classified error | 540 ns/op, zero allocations |
| `errs.KindOf`, unclassified error | 633 ns/op, zero allocations |
| `errs.IsRetryable`, classified error | 415 ns/op, zero allocations |

The zero-allocation property on the disabled path is what these benchmarks exist
to hold, and it survives; the nanosecond figures move with the machine and with
emulation. Note the three-orders-of-magnitude gap between a disabled and an
enabled span — telemetry being off by default is not a stylistic choice about
noise.

`registry.DefaultBatchSize = 1000` rows per write transaction
(`internal/registry/registry.go:73`). It trades SQLite's per-transaction commit
overhead, which wants large batches, against how long a write transaction blocks
concurrent readers on a single connection, which wants small ones. No benchmark
in the tree sweeps this value, so this document states the trade and not a
number.

Two other constants a reader may be looking for: `parse.DefaultParseCeiling =
60 s`, the wall-clock safety net described under [the parse
budget](#the-parse-budget), and `watch.DefaultMaxWatchDirs = 8192`
(`internal/watch/watch.go:62`), verified in the tree rather than measured.

## Tuning for a large monorepo

The defaults size themselves: the worker count follows the machine and the
memory limit (above), and the index file gives free space back after each write
([Automatic compaction](#automatic-compaction)). There is nothing to run to
keep an index small or fast.

**Exclude generated trees.** Almost always the most effective change. Repository
excludes are additive to the defaults:

```yaml
index:
  exclude:
    - "**/generated/**"
    - "**/*.pb.go"
    - "**/vendor/**"
```

An excluded file is never read, never hashed, never parsed and not reported. A
file over the size limit is not read either, but it is recorded and named, so
excluding a generated tree is also what keeps the size report about files that
matter.

**Use `graphlume watch` rather than repeated full indexes.** On the 5,000-file
corpus a full index is 20–35 s while an incremental pass over an unchanged tree is
under a second, and a one-file change is around 0.8 s — a reparse of what
actually changed, with unchanged files not even opened.

**Use `graphlume index pull` rather than a first local index.** CI builds the
index once per commit; `index pull` downloads it with your own `gh` login, and
`graphlume index` (or `index pull --update`) brings it up to date incrementally.
A pulled index keeps its auto-vacuum mode, so it compacts itself too.

**Give a dedicated indexing machine more memory, not more flags.** A higher
limit lets the default use up to 8 workers:

```bash
graphlume index --full --memory-limit 2GiB
```

`--workers N` goes past 8 when you have measured that your tree gains from it.
Watch peak RSS: a repository of small files tolerates more workers than one full
of large generated ones.

**Raise `index.max_file_bytes` only for source you want indexed.** It is set per
repository in `.graphlume.yml`, up to 8 MiB, and it has a memory price: about
150 bytes of process memory per source byte while the file is parsed. Before
raising it, check what you would gain: `graphlume status` names the largest
files over the limit, and the `files` table records the size of every one.

### Caveats worth knowing before you tune

- `--workers` and `--accuracy` take effect only on the **full** index path.
  Without `--full`, `graphlume index` takes the incremental path and neither is
  applied. They are, however, **validated on every path** — a typo fails
  immediately rather than being silently discarded:

  ```
  $ graphlume index --accuracy nonsense
  error: --accuracy "nonsense": want fast, auto or exact     # exit 1
  ```

- The default memory limit is applied only to a run that discovers at most
  64 MiB of source (`DefaultMemoryLimitMaxSource`); the worker count is still
  sized for it, which on a larger repository errs on the side of fewer workers.
- `fs.DefaultMaxFileSize` and `parse.DefaultMaxSourceBytes` are the same number,
  2 MiB, taken from `config.DefaultMaxFileBytes`, so a file that is read is also
  parsed.
- The discovery walker uses `runtime.NumCPU()` workers (`fs.WithWorkers`), not
  the parse worker count. Walking is I/O-bound and allocates little.
- Compaction waits for its thresholds: an index with less than 64 MiB free, or
  less than 20% of its file free, keeps those pages for reuse and does not
  shrink. A reader holding a long snapshot during a write leaves the checkpoint
  `PASSIVE`, so the `-wal` file keeps its size until a later write truncates it.

## Context assembly at scale

Spec §39 sets a P95 target of 2 s for context assembly, measured on a synthetic
94,000-symbol repository by `TestP95LatencyAt94kSymbols` in
`internal/context/latency_test.go`, behind the `perf` build tag.

The gate builds, runs, and passes:

```bash
go test -tags perf -timeout 25m -v -run 'TestP95|TestCatalogueCacheRemoves' ./internal/context/
```

```
94,000 symbols
  cold (builds the catalogue): 948ms
  warm p50=55ms p95=57ms p99=71ms max=71ms over 60 runs
  cache 60 hits / 1 misses, heap 75 MB
--- PASS: TestP95LatencyAt94kSymbols (13.06s)
--- PASS: TestCatalogueCacheRemovesTheDominantCost (0.93s)
    10,000 symbols: cold 91ms, warm 10ms
```

Three things in that output carry the weight:

- **p95 is 57 ms against a 2 s spec ceiling**, and against the tighter 200 ms
  regression gate the test also asserts. The tighter gate is the useful one: the
  ceiling has 35x headroom and would not notice a large regression.
- **60 hits / 1 miss** is machine-independent proof that the cached path is
  actually being exercised. 0 hits / 0 misses would prove the opposite.
- **Cold 948 ms against warm 55 ms** is the cost the cache exists to remove. At
  10,000 symbols the same comparison is 91 ms cold against 10 ms warm.

The cost being removed is the in-memory catalogue — two table scans plus enclosure
resolution and index building — which without the cache is rebuilt on every
question. An agent asking three questions in a session would pay for it three
times, and the cost would scale with the repository rather than with the
question.

**Keying the cache on the run id alone would be wrong.** A committed run is not
immutable: incremental indexing updates it IN PLACE, so the same id describes
different content minute to minute while a developer edits. A cache keyed on the
id would serve pre-edit answers for as long as the process lives — the exact
staleness this product exists to remove.

The key is a content fingerprint instead: file count, symbol count, and the
maximum id of each. Four indexed aggregates, checked on every request, one SQL
statement in `registry.CatalogueFingerprint`. It detects every mutation because
ids are AUTOINCREMENT — an insert always advances the maximum even when a
delete-then-insert leaves the count unchanged, which is exactly what `ReplaceFile`
does on every incremental update. A delete shows up in the count. The combination
that would fool it — removing rows and adding exactly as many while the maximum
stays put — cannot happen, because an id is never reissued.

Both directions are proven by tests that perform a real in-place update and a real
delete, and assert the rebuilt catalogue reflects them.

The gates skip under `-race` and sit behind the `perf` tag rather than in the
default suite, because the detector dominates the measurement: asserting a time
budget against instrumented code measures the detector rather than the product.
Correctness still runs under `-race`.
