#!/usr/bin/env bash
#
# corpus-local — run the gate over the FULL ten-member corpus, including the
# private application that public CI cannot reach.
#
# This exists because a checked-in path list does not survive a
# session: entries that point into a temp directory that is
# deleted between runs rot, so a committed corpus definition would rot
# at once. Membership is defined by upstream URL (which is stable)
# plus two local checkouts resolved at run time.
#
#   script/corpus-local.sh [graphlume-binary]
#
# Env:
#   CORPUS_HOME   where the upstream clones live   (default ~/.graphlume-corpus)
#   CORPUS_EXTRA  an optional local repository to add as a tenth member (default: none)
set -euo pipefail

cd "$(dirname "$0")/.."
DEV="${1:-}"
if [ -z "$DEV" ]; then
  DEV=$(mktemp -d)/graphlume
  echo "building graphlume…"
  go build -o "$DEV" ./cmd/graphlume
fi

# A local repository to add to the corpus, if any; nothing is assumed about this machine.
export CORPUS_EXTRA="${CORPUS_EXTRA:-}"
HOME_DIR="${CORPUS_HOME:-$HOME/.graphlume-corpus}"
LIST=$(mktemp)

if [ -z "$CORPUS_EXTRA" ] || [ ! -d "$CORPUS_EXTRA" ]; then
  # Named, not silently dropped. A nine-member run reported as the corpus is a
  # coverage claim the run did not earn.
  echo "note: no CORPUS_EXTRA repository${CORPUS_EXTRA:+ at $CORPUS_EXTRA} — running the nine public members" >&2
  unset CORPUS_EXTRA
fi

script/corpus-clone.sh "$HOME_DIR" "$LIST"
echo
exec script/corpus-gate.sh "$LIST" "$DEV"
