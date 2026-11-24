#!/bin/sh
# Compatibility gate: runs GraphLume cold, the way a new user would, on each
# public repository in corpus.tsv, one at a time.
#
#   scripts/compat/run.sh [-b graphlume-binary] [-w workdir] [id ...]
#
# For each repository it makes a shallow clone at the pinned tag or commit,
# runs the one-command onboarding (`graphlume init --index --indexers`, no agent
# registration, no rule injection), records the exit code, wall time, peak
# resident memory and the `status` output, then deletes the clone and its
# .graphlume directory. Raw output goes to <workdir>/raw/<id>/; report.py turns
# it into scripts/compat/out/results.json and README.md.
#
# Cold means: a throwaway HOME (so no indexer from an earlier run or from the
# developer's machine is found on PATH or under ~/.graphlume), and a PATH with
# only the language toolchains (node, go, cargo, java, maven, ruby, python) and
# none of the SCIP indexers. Downloads therefore happen on the first repository
# that needs them and are kept in that HOME for the rest of the run.
#
# COMPAT_KEEP=1 keeps each clone (for diagnosing a failure); delete it yourself.
#
# Low CPU by design: GOMAXPROCS=2 and nice -n 19; repositories run serially.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
bin=graphlume
work=${COMPAT_WORKDIR:-${TMPDIR:-/tmp}/graphlume-compat}
while getopts "b:w:" o; do
	case $o in
	b) bin=$OPTARG ;;
	w) work=$OPTARG ;;
	*) exit 2 ;;
	esac
done
shift $((OPTIND - 1))
case $bin in */*) bin=$(cd "$(dirname "$bin")" && pwd)/$(basename "$bin") ;; esac

mkdir -p "$work/raw" "$work/shim" "$work/home"
work=$(cd "$work" && pwd)
"$bin" --version >"$work/version.txt" 2>&1 || true

# Real locations of toolchain state, kept when HOME is swapped out.
real_home=$HOME
export CARGO_HOME=${CARGO_HOME:-$real_home/.cargo}
export RUSTUP_HOME=${RUSTUP_HOME:-$real_home/.rustup}
export GOMODCACHE=${GOMODCACHE:-$(go env GOMODCACHE)}
export GOCACHE=${GOCACHE:-$(go env GOCACHE)}

# Toolchains only. Indexers (scip-*, rust-analyzer) are deliberately absent.
for t in node npm npx pnpm yarn corepack cargo rustc rustup; do
	p=$(command -v "$t" 2>/dev/null || true)
	[ -n "$p" ] && ln -sf "$p" "$work/shim/$t"
done
gobin=$(dirname "$(command -v go)")
export PATH="$work/shim:$gobin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin"
export HOME="$work/home"
export GOMAXPROCS=2

pick=" $* "
total=0
while IFS='	' read -r id stack url pin why; do
	case $id in '#'* | '') continue ;; esac
	if [ $# -gt 0 ]; then case $pick in *" $id "*) ;; *) continue ;; esac; fi
	total=$((total + 1))
	raw="$work/raw/$id"
	repo="$work/clone/$id"
	rm -rf "$raw" "$repo"
	mkdir -p "$raw" "$repo"
	echo "== $id ($stack)"

	if ! (cd "$repo" && git init -q . && git remote add origin "$url" &&
		git fetch -q --depth 1 origin "$pin" && git checkout -q FETCH_HEAD) >"$raw/clone.log" 2>&1; then
		echo "clone failed" >"$raw/clone.failed"
		echo "   clone failed, see $raw/clone.log"
		rm -rf "$repo"
		continue
	fi
	(cd "$repo" && git rev-parse HEAD) >"$raw/commit.txt"
	(cd "$repo" && git ls-files | wc -l | tr -d ' ') >"$raw/tracked_files.txt"

	start=$(date +%s)
	set +e
	(cd "$repo" && nice -n 19 /usr/bin/time -l "$bin" init --index --indexers </dev/null >"$raw/init.out" 2>"$raw/init.err")
	echo $? >"$raw/exit_code.txt"
	set -e
	echo $(($(date +%s) - start)) >"$raw/wall_seconds.txt"
	# /usr/bin/time -l writes to the same stderr stream; keep its lines apart.
	grep -E 'maximum resident set size|real' "$raw/init.err" >"$raw/time.txt" || true

	(cd "$repo" && "$bin" status >"$raw/status.txt" 2>&1 || true)
	(cd "$repo" && "$bin" status --json >"$raw/status.json" 2>/dev/null || true)
	(cd "$repo" && "$bin" indexers list >"$raw/indexers_list.txt" 2>&1 || true)

	echo "   exit $(cat "$raw/exit_code.txt"), $(cat "$raw/wall_seconds.txt")s"
	if [ -z "${COMPAT_KEEP:-}" ]; then
		rm -rf "$repo/.graphlume" "$repo"
	else
		echo "   kept $repo (COMPAT_KEEP is set)"
	fi
done <"$here/corpus.tsv"

echo "ran $total repositories; raw output in $work/raw"
echo "next: python3 -I $here/report.py $work/raw"
