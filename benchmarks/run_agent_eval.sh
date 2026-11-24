#!/usr/bin/env bash
# ==============================================================================
# GraphLume vs grep: what an agent reads to answer "who calls X?"
#
# Usage:  benchmarks/run_agent_eval.sh [REPO] [SAMPLES]
#   REPO     a git repository to measure (default: this repository)
#   SAMPLES  how many symbols to sample (default: 25)
#
# What it measures, on a fresh clone of REPO (your checkout is not touched):
#   * cold index + resolve time
#   * for SAMPLES callable symbols that the index records callers for, chosen
#     deterministically, the bytes an agent must read and the tool calls it
#     makes to find the callers:
#       grep:        `grep -rnw NAME` over the source files, then every matching
#                    file read in full (one call each), which is what an agent
#                    without an index does to tell a call from a mention
#       graphlume:  one `graphlume --json callers NAME`
#
# What it does NOT measure: whether the answers are right. Byte counts say
# nothing about precision, and the index is not ground truth for itself. For
# precision and recall graded against a compiler (SCIP), use
# scripts/bench/impactaccuracy. Tokens are estimated as bytes / 4.
#
# No figure in the output is fixed in this script: every number is measured
# on the run. Requires git, grep, python3.
# ==============================================================================

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO="$(cd "${1:-$ROOT_DIR}" && pwd)"
SAMPLES="${2:-25}"
BIN="${BIN:-}"

WORK="$(mktemp -d "${TMPDIR:-/tmp}/graphlume-bench-XXXXXX")"
trap 'rm -rf "$WORK"' EXIT

if [[ -z "$BIN" ]]; then
    echo "==> building graphlume"
    go build -o "$WORK/graphlume" "$ROOT_DIR/cmd/graphlume"
    BIN="$WORK/graphlume"
fi

now_ms() { python3 -I -c 'import time; print(int(time.time()*1000))'; }

echo "==> cloning $REPO (shallow, local)"
git clone -q --depth 1 "file://$REPO" "$WORK/repo"
cd "$WORK/repo"

echo "==> cold index + resolve"
t0=$(now_ms)
"$BIN" init >/dev/null 2>&1
"$BIN" index >/dev/null 2>&1
"$BIN" resolve >/dev/null 2>&1
t1=$(now_ms)
INDEX_MS=$(( t1 - t0 ))
FILES=$("$BIN" --json query "SELECT COUNT(*) AS n FROM files WHERE index_run_id = :run" --limit 1 2>/dev/null |
    python3 -I -c 'import json,sys
d=json.load(sys.stdin); rows=d.get("rows") if isinstance(d,dict) else d
r=(rows or [{}])[0]; print(r.get("n") if isinstance(r,dict) else r[0])' 2>/dev/null || echo "?")

# Callable symbols with at least one CALLS edge and a name grep can find whole.
# Ordered by a hash of the name, so the sample is the same on every run of the
# same tree without being biased toward the start of the alphabet.
"$BIN" --json query "
  SELECT s.name AS name
    FROM symbols s
    JOIN edges e ON e.dst_id = s.id AND e.index_run_id = s.index_run_id AND e.kind = 'CALLS'
   WHERE s.index_run_id = :run
     AND s.kind IN ('function','method')
     AND length(s.name) >= 4
   GROUP BY s.name
   ORDER BY (unicode(substr(s.name,1,1)) * 31 + length(s.name) * 17 + unicode(substr(s.name,-1,1))) % 997, s.name
   LIMIT $SAMPLES" >"$WORK/names.json"

python3 -I - "$WORK/names.json" >"$WORK/names.txt" <<'PY'
import json, sys
doc = json.load(open(sys.argv[1]))
rows = doc.get("rows") if isinstance(doc, dict) else doc
for r in rows or []:
    name = r.get("name") if isinstance(r, dict) else r[0]
    if name:
        print(name)
PY

N=$(wc -l <"$WORK/names.txt" | tr -d ' ')
if [[ "$N" -eq 0 ]]; then
    echo "No symbol with recorded callers was found; nothing to measure." >&2
    exit 1
fi

# Source files only: the same set the index covers is not known to grep, so
# the common source extensions stand in for it.
INCLUDES=(--include='*.go' --include='*.py' --include='*.java' --include='*.kt' --include='*.ts'
          --include='*.tsx' --include='*.js' --include='*.rs' --include='*.c' --include='*.h'
          --include='*.cc' --include='*.cpp' --include='*.hpp' --include='*.cs' --include='*.rb'
          --include='*.php' --include='*.swift' --include='*.scala')

echo "name,grep_calls,grep_bytes,grep_ms,rc_calls,rc_bytes,rc_ms,rc_ok" >"$WORK/results.csv"
while IFS= read -r name; do
    t0=$(now_ms)
    matches="$(grep -rnw "${INCLUDES[@]}" --exclude-dir=.git --exclude-dir=.graphlume -- "$name" . || true)"
    files="$(printf '%s\n' "$matches" | cut -d: -f1 | sort -u | sed '/^$/d')"
    nfiles=$(printf '%s\n' "$files" | sed '/^$/d' | wc -l | tr -d ' ')
    read_bytes=0
    if [[ "$nfiles" -gt 0 ]]; then
        read_bytes=$(printf '%s\n' "$files" | tr '\n' '\0' | xargs -0 cat | wc -c | tr -d ' ')
    fi
    t1=$(now_ms)
    grep_bytes=$(( ${#matches} + read_bytes ))
    grep_calls=$(( 1 + nfiles ))

    t2=$(now_ms)
    rc_out="$("$BIN" --json callers "$name" 2>/dev/null || true)"
    t3=$(now_ms)
    # An error answer is small and would flatter the comparison: count it.
    rc_ok=1
    if [[ -z "$rc_out" || "$rc_out" == *'"error"'* ]]; then rc_ok=0; fi
    echo "$name,$grep_calls,$grep_bytes,$(( t1 - t0 )),1,${#rc_out},$(( t3 - t2 )),$rc_ok" >>"$WORK/results.csv"
done <"$WORK/names.txt"

python3 -I - "$WORK/results.csv" "$REPO" "$FILES" "$INDEX_MS" <<'PY'
import csv, statistics, sys
path, repo, files, index_ms = sys.argv[1:5]
rows = list(csv.DictReader(open(path)))
def col(k): return [int(r[k]) for r in rows]
def med(k): return statistics.median(col(k))
def tot(k): return sum(col(k))
print()
print(f"Repository : {repo}")
print(f"Files      : {files}   cold index + resolve: {int(index_ms)/1000:.1f} s")
print(f"Symbols    : {len(rows)} sampled, each with at least one recorded caller")
failed = sum(1 for r in rows if r["rc_ok"] != "1")
if failed:
    print(f"WARNING    : {failed} graphlume answer(s) were errors; their sizes are not comparable")
print()
print(f"{'':22}{'grep + read':>16}{'graphlume':>16}")
print(f"{'tool calls (median)':22}{med('grep_calls'):>16.0f}{med('rc_calls'):>16.0f}")
print(f"{'bytes read (median)':22}{med('grep_bytes'):>16,.0f}{med('rc_bytes'):>16,.0f}")
print(f"{'~tokens (median)':22}{med('grep_bytes')/4:>16,.0f}{med('rc_bytes')/4:>16,.0f}")
print(f"{'latency ms (median)':22}{med('grep_ms'):>16.0f}{med('rc_ms'):>16.0f}")
g, r = tot('grep_bytes'), tot('rc_bytes')
if g:
    print(f"\nAcross all samples graphlume returned {r:,} bytes where grep-and-read took {g:,} "
          f"({100*(1-r/g):.0f}% less).")
print("Per-symbol rows: rerun with KEEP=1 to keep the CSV.")
print("Precision is not measured here; see scripts/bench/impactaccuracy.")
PY

if [[ "${KEEP:-}" == "1" ]]; then
    cp "$WORK/results.csv" "$ROOT_DIR/benchmarks/last-run.csv"
    echo "Kept per-symbol rows in benchmarks/last-run.csv"
fi
