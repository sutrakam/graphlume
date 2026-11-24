#!/usr/bin/env bash
# Git pre-push hook for GraphLume
# Install with: ln -sf ../../scripts/pre-push.sh .git/hooks/pre-push

set -euo pipefail

echo "Running pre-push verification (gofmt, go vet)..."
bad=$(gofmt -l .)
if [ -n "$bad" ]; then
    echo "gofmt found unformatted files:" >&2
    echo "$bad" >&2
    exit 1
fi

go vet ./...
echo "Pre-push verification passed."
