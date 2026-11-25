#!/usr/bin/env bash
#
# incremental.sh — time a re-index after two files change.
#
# Usage:
#   scripts/bench/incremental.sh <graphlume-binary> <clone> <file-a> <file-b> [runs]
#
# <clone> must be a throwaway git clone: this script deletes its .graphlume,
# appends a line to <file-a> and <file-b> on every run, and puts both files
# back with `git checkout` when it finishes. <file-a> and <file-b> are paths
# relative to the clone. [runs] defaults to 3.
#
# What it prints, in order:
#   1. cold:   `init` + `index` into an empty database, then `resolve`
#   2. noop:   `index` with nothing changed
#   3. index:  per run, append one definition to each of the two files, then
#              `index`, then `resolve`
#   4. watch:  per run, the same edit while `graphlume watch` is running: the
#              time watch reports for the update, and the time from the edit to
#              the moment watch logged it (which includes its 300 ms debounce)
#
# Wall time and peak memory come from /usr/bin/time -l (BSD time, as on macOS).
# "printed" is the duration GraphLume reports itself, which excludes process
# start. Nothing is averaged: every run is printed.

set -euo pipefail

if [ "$#" -lt 4 ]; then
  sed -n '3,22p' "${BASH_SOURCE[0]}" | sed 's|^# \{0,1\}||' >&2
  exit 2
fi

bin=$(cd -- "$(dirname -- "$1")" && pwd)/$(basename -- "$1")
repo=$(cd -- "$2" && pwd)
file_a=$3
file_b=$4
runs=${5:-3}

cd "$repo"
for f in "$file_a" "$file_b"; do
  [ -f "$f" ] || { echo "incremental.sh: $f is not a file in $repo" >&2; exit 1; }
done
if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  echo "incremental.sh: $repo has uncommitted changes; use a clone made for this" >&2
  exit 1
fi

scratch=$(mktemp -d)
restore() {
  git checkout -q -- "$file_a" "$file_b" || true
  rm -rf "$scratch"
}
trap restore EXIT

now() { perl -MTime::HiRes=time -e 'printf "%.3f\n", time'; }

# timed <label> <command...> — run under /usr/bin/time -l and print one line.
timed() {
  local label=$1
  shift
  /usr/bin/time -l "$@" >"$scratch/out" 2>"$scratch/err" || {
    cat "$scratch/out" "$scratch/err" >&2
    echo "incremental.sh: $label failed" >&2
    exit 1
  }
  local real rss printed
  real=$(awk '$2 == "real" { print $1 }' "$scratch/err")
  rss=$(awk '/maximum resident set size/ { printf "%.0f", $1 / 1048576 }' "$scratch/err")
  printed=$(grep -Eo '(Duration: +[0-9]+ms|[0-9]+ms\))' "$scratch/out" | grep -Eo '[0-9]+' | tail -1 || true)
  printf '%-22s wall %7ss   printed %7s ms   peak RSS %5s MB\n' "$label" "$real" "${printed:--}" "$rss"
}

# edit <n> — append one new definition to each of the two files.
edit() {
  local n=$1 f
  for f in "$file_a" "$file_b"; do
    case "$f" in
      *.ts|*.tsx|*.js|*.jsx|*.mjs) printf '\nexport function benchProbe%s() { return %s; }\n' "$n" "$n" >>"$f" ;;
      *.py)                        printf '\n\ndef bench_probe_%s():\n    return %s\n' "$n" "$n" >>"$f" ;;
      *.go)                        printf '\nfunc benchProbe%s() int { return %s }\n' "$n" "$n" >>"$f" ;;
      *.rb|*.sh)                   printf '\n# bench probe %s\n' "$n" >>"$f" ;;
      *)                           printf '\n// bench probe %s\n' "$n" >>"$f" ;;
    esac
  done
}

echo "repository: $repo"
echo "binary:     $("$bin" version)"
echo "files:      $(git ls-files | wc -l | tr -d ' ') tracked"
echo "edited:     $file_a  $file_b"
echo

rm -rf .graphlume
"$bin" init -q >/dev/null
timed "cold index" "$bin" index
grep -E '^ +(Files|Symbols|References):' "$scratch/out" || true
timed "cold resolve" "$bin" resolve
timed "noop index" "$bin" index
echo

for run in $(seq 1 "$runs"); do
  edit "i$run"
  timed "run $run index" "$bin" index
  timed "run $run resolve" "$bin" resolve
done
echo

"$bin" watch >"$scratch/watch" 2>&1 &
watcher=$!
# Watch prints its banner once it is watching. Edits made before that are not
# its to see, so wait for it, however long a loaded machine takes.
deadline=$((SECONDS + 300))
until grep -q 'Watching' "$scratch/watch" 2>/dev/null; do
  if [ "$SECONDS" -ge "$deadline" ] || ! kill -0 "$watcher" 2>/dev/null; then
    echo "incremental.sh: watch did not start within 300 s; its log:" >&2
    sed 's/^/    /' "$scratch/watch" >&2
    kill -INT "$watcher" 2>/dev/null || true
    exit 1
  fi
  sleep 0.2
done
sleep 2

# modified_since <n> — files watch reported as modified in the "index updated"
# lines after its first n.
modified_since() {
  awk -v skip="$1" '
    /index updated/ {
      n++
      if (n > skip && match($0, /modified=[0-9]+/)) total += substr($0, RSTART + 9, RLENGTH - 9)
    }
    END { print total + 0 }' "$scratch/watch"
}

for run in $(seq 1 "$runs"); do
  seen=$(grep -c 'index updated' "$scratch/watch" || true)
  start=$(now)
  edit "w$run"
  # Watch may report the two files in one update or in two. Wait for both.
  deadline=$((SECONDS + 120))
  result="both files indexed"
  while [ "$(modified_since "$seen")" -lt 2 ]; do
    if [ "$SECONDS" -ge "$deadline" ]; then
      result="NOT both indexed"
      echo "incremental.sh: watch had not reported both files after 120 s; its log ends:" >&2
      tail -5 "$scratch/watch" | sed 's/^/    /' >&2
      break
    fi
    sleep 0.05
  done
  end=$(now)
  updates=$(grep 'index updated' "$scratch/watch" | tail -n +"$((seen + 1))" || true)
  printf 'run %s watch            %s %6.2fs after the edit, in %s update(s): %s\n' \
    "$run" "$result" "$(echo "$end - $start" | bc -l)" \
    "$(printf '%s\n' "$updates" | grep -c 'index updated' || true)" \
    "$(printf '%s\n' "$updates" | grep -Eo '(modified|took)=[^ ]+' | paste -sd' ' - || true)"
  sleep 2
done
kill -INT "$watcher" 2>/dev/null || true
wait "$watcher" 2>/dev/null || true
