#!/usr/bin/env bash
#
# cold-index.sh — time a first index of a repository, step by step.
#
# Usage:
#   scripts/bench/cold-index.sh <graphlume-binary> <repository> [index flags...]
#
# Deletes <repository>/.graphlume, then runs `init`, `index` and `resolve`
# one at a time under /usr/bin/time -l (BSD time, as on macOS), and prints for
# each: wall, user and system seconds and peak resident memory. It ends with
# the counts `graphlume status` reports and the size of the index on disk.
# Anything after the repository is passed to `index`, for example --workers 8.
#
# The Linux kernel measurement in docs/performance.md is this script run on
#   git clone --depth 1 https://github.com/torvalds/linux
#
# A step that runs too long or grows too large is stopped rather than left to
# take the machine down, and the script then reports how far it got:
#   BENCH_MAX_SECONDS   per-step wall limit      (default 7200)
#   BENCH_MAX_RSS_MB    per-step memory limit    (default 0 = none)
# Being stopped is a result. The last progress line the step logged is printed.

set -uo pipefail

if [ "$#" -lt 2 ]; then
  sed -n '3,20p' "${BASH_SOURCE[0]}" | sed 's|^# \{0,1\}||' >&2
  exit 2
fi

bin=$(cd -- "$(dirname -- "$1")" && pwd)/$(basename -- "$1")
repo=$(cd -- "$2" && pwd)
shift 2
index_flags=("$@")
max_seconds=${BENCH_MAX_SECONDS:-7200}
max_rss_mb=${BENCH_MAX_RSS_MB:-0}

cd "$repo" || exit 1
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

# step <label> <command...> — run one command under time, watching its limits.
step() {
  local label=$1
  shift
  local out="$scratch/$label.out" err="$scratch/$label.err"
  /usr/bin/time -l "$@" >"$out" 2>"$err" &
  local timer=$! began=$SECONDS stopped=""
  while kill -0 "$timer" 2>/dev/null; do
    sleep 2
    local child rss_mb=0
    child=$(pgrep -P "$timer" | head -1)
    if [ -n "$child" ]; then
      rss_mb=$(($(ps -o rss= -p "$child" 2>/dev/null | tr -d ' ' || echo 0) / 1024))
    fi
    if [ $((SECONDS - began)) -gt "$max_seconds" ]; then
      stopped="stopped after ${max_seconds}s (BENCH_MAX_SECONDS)"
    elif [ "$max_rss_mb" -gt 0 ] && [ "$rss_mb" -gt "$max_rss_mb" ]; then
      stopped="stopped at ${rss_mb} MB resident (BENCH_MAX_RSS_MB=$max_rss_mb)"
    fi
    if [ -n "$stopped" ] && [ -n "$child" ]; then
      kill -TERM "$child" 2>/dev/null
      break
    fi
  done
  wait "$timer"
  local status=$?

  local real user sys rss
  real=$(awk '$2 == "real" { print $1 }' "$err")
  user=$(awk '$2 == "real" { print $3 }' "$err")
  sys=$(awk '$2 == "real" { print $5 }' "$err")
  rss=$(awk '/maximum resident set size/ { printf "%.0f", $1 / 1048576 }' "$err")
  printf '%-8s wall %8ss   user %8ss   sys %8ss   peak RSS %6s MB\n' \
    "$label" "${real:-?}" "${user:-?}" "${sys:-?}" "${rss:-?}"
  grep -vE 'level=INFO msg=indexing|^ +[0-9]+ +[a-z]|real +[0-9.]+ user' "$out" "$err" \
    | sed -E 's|^[^:]+:||' | sed 's/^/    /'

  if [ -n "$stopped" ] || [ "$status" -ne 0 ]; then
    echo "    RESULT: $label did not finish: ${stopped:-exit status $status}"
    echo "    last progress: $(grep -h 'progress=' "$err" "$out" | tail -1)"
    return 1
  fi
}

echo "repository: $repo"
echo "binary:     $("$bin" version)"
# BSD du spells the exclusion -I, GNU du --exclude.
tree_size=$( (du -sh -I .git -I .graphlume . 2>/dev/null || du -sh --exclude=.git --exclude=.graphlume .) | cut -f1 | tr -d ' ')
echo "tracked:    $(git ls-files | wc -l | tr -d ' ') files, $tree_size in the working tree"
echo "started:    $(date '+%Y-%m-%d %H:%M:%S %z'), load $(sysctl -n vm.loadavg)"
echo

rm -rf .graphlume
ok=1
step init "$bin" init || ok=0
if [ "$ok" -eq 1 ]; then step index "$bin" index ${index_flags[@]+"${index_flags[@]}"} || ok=0; fi
if [ "$ok" -eq 1 ]; then step resolve "$bin" resolve || ok=0; fi

echo
echo "finished:   $(date '+%Y-%m-%d %H:%M:%S %z'), load $(sysctl -n vm.loadavg)"
"$bin" -q status 2>&1 | sed 's/^/    /'
if [ -d .graphlume ]; then
  echo "index on disk:"
  for f in .graphlume/*; do
    [ -f "$f" ] && printf '    %14d bytes  %s\n' "$(wc -c <"$f")" "${f#.graphlume/}"
  done
fi
[ "$ok" -eq 1 ]
