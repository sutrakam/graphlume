#!/usr/bin/env bash
#
# ci-local.sh: run the lint, test and build jobs of .github/workflows/ci.yml
# on this machine, in the same order and with the same commands, and stop at
# the first failure. A green run here is what a green CI run checks, so run it
# before starting CI. When you change a step in ci.yml, change it here too.
#
# Usage: scripts/ci-local.sh [--quick]
#   --quick   lint and the race-detector tests only (skips the end-to-end run,
#             the fixture benchmark, cross-compilation and govulncheck)
#
# The end-to-end steps run on a temporary worktree of HEAD, so commit first.
# Not reproduced: the optional Windows and deep jobs (fuzz, scale, soak,
# latency, real repositories). Start those from the Actions tab.
set -euo pipefail

GOLANGCI_LINT_VERSION="v2.15.0"
QUICK=0
case "${1:-}" in
  --quick) QUICK=1 ;;
  "") ;;
  *) echo "usage: $0 [--quick]" >&2; exit 2 ;;
esac

cd "$(git rev-parse --show-toplevel)"
step() { printf '\n\033[1m==> %s\033[0m\n' "$1"; }
trap 'printf "\n\033[31mFAILED\033[0m at: %s\n" "${current:-setup}" >&2' ERR

# --- lint -------------------------------------------------------------------
current="gofmt"; step "$current"
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then echo "run gofmt on:"; echo "$unformatted"; exit 1; fi

current="go vet"; step "$current"
go vet ./...
go vet -tags soak ./tests/soak/...
go vet -tags perf ./internal/context/...

current="golangci-lint"; step "$current"
if ! golangci-lint version 2>/dev/null | grep -q "version ${GOLANGCI_LINT_VERSION#v}"; then
  echo "CI uses golangci-lint $GOLANGCI_LINT_VERSION. Install it with:"
  echo "  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$GOLANGCI_LINT_VERSION"
  exit 1
fi
golangci-lint run ./...

current="go.mod is tidy"; step "$current"
# Checked in a scratch copy, so a failure here never rewrites your go.mod.
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
cp go.mod go.sum "$tmp/"
go mod tidy
if ! diff -q go.mod "$tmp/go.mod" >/dev/null || ! diff -q go.sum "$tmp/go.sum" >/dev/null; then
  cp "$tmp/go.mod" "$tmp/go.sum" .
  echo "go.mod or go.sum is not tidy: run go mod tidy and commit the result"; exit 1
fi

current="THIRD_PARTY.md matches the module graph"; step "$current"
go run ./cmd/graphlume licenses --check

# --- test -------------------------------------------------------------------
# The tests run with PATH cut down to go and git, as on a hosted runner. With
# this machine's own PATH, a test can quietly depend on a tool installed here
# (claude, node, an indexer) and pass locally while it fails in CI.
minbin="$tmp/minbin"
mkdir -p "$minbin"
for tool in go gofmt git; do ln -sf "$(command -v "$tool")" "$minbin/$tool"; done
fullpath="$PATH"
export PATH="$minbin:/usr/bin:/bin:/usr/sbin:/sbin"

current="unit and integration tests (race detector)"; step "$current"
pkgs=$(go list ./... | grep -v '/internal/cli$')
# shellcheck disable=SC2086 # one package path per word
go test -race -timeout 30m $pkgs

# CI splits internal/cli across three runners; one machine runs it whole.
current="internal/cli tests (race detector)"; step "$current"
go test -race -timeout 30m ./internal/cli/

if [ "$QUICK" = 1 ]; then printf '\n\033[32mquick checks passed\033[0m\n'; exit 0; fi

gl="$tmp/graphlume"
current="end-to-end on this repository"; step "$current"
go build -o "$gl" ./cmd/graphlume
work="$tmp/self"; git worktree add --quiet --detach "$work" HEAD
trap 'git worktree remove --force "$work" 2>/dev/null; rm -rf "$tmp"' EXIT
(
  cd "$work"
  "$gl" -q init && "$gl" -q index && "$gl" -q resolve
  "$gl" compile "Add a confidence floor to impact traversal" --json > "$tmp/contract.json"
  test -s "$tmp/contract.json"
  set +e
  "$gl" verify --contract "$tmp/contract.json" --changed internal/capability/impact/impact.go >/dev/null
  code=$?
  set -e
  [ "$code" -lt 2 ] || { echo "verify had nothing to check against (exit $code)"; exit 1; }
  "$gl" accuracy > "$tmp/accuracy.txt" 2>&1 || { cat "$tmp/accuracy.txt"; exit 1; }
  grep -q "NOT MEASURED\|Not measured" "$tmp/accuracy.txt"
  "$gl" docs --out "$tmp/docs-a" >/dev/null && "$gl" docs --out "$tmp/docs-b" >/dev/null
  diff -u "$tmp/docs-a/ARCHITECTURE.md" "$tmp/docs-b/ARCHITECTURE.md"
  diff -u "$tmp/docs-a/CAPABILITIES.md" "$tmp/docs-b/CAPABILITIES.md"
)

current="retrieval benchmark on the bundled fixture"; step "$current"
(
  cd "$work/fixtures/payment-platform"
  "$gl" -q init && "$gl" -q index && "$gl" -q resolve
  "$gl" bench ../../fixtures/bench/payment.yml --fail-on-regression
)

export PATH="$fullpath"

# --- build ------------------------------------------------------------------
current="wall-clock parser tests (without -race)"; step "$current"
go test -count=1 -run 'TestSwiftMaskRetryIsSkippedForAFileWithManyErrors|TestAnAbsurdlyNestedFileIsRecordedAsPartialQuickly|TestRetriesDoNotDisturbConcurrentParses' ./internal/parse/

current="cross-compile without CGO"; step "$current"
for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64; do
  CGO_ENABLED=0 GOOS="${target%/*}" GOARCH="${target#*/}" go build -trimpath -o /dev/null ./cmd/graphlume
done

current="govulncheck"; step "$current"
go run golang.org/x/vuln/cmd/govulncheck@latest ./...

printf '\n\033[32mall CI checks passed\033[0m\n'
