#!/usr/bin/env bash
# make-workspace.sh: build the synthetic "payments-platform" workspace, with
# its git history, in a directory of your choice.
#
#   make-workspace.sh <target-dir>
#
# The source files live in ./workspace next to this script and are committed to
# the GraphLume repository. The .git history is NOT committed (a repository
# nested in a repository would become a submodule); this script generates it:
# 26 commits by four invented authors (AR, MC, JO, LP) dated across a year,
# with AR owning the ledger alone. Every run produces the same commit hashes.
#
# Some files are committed in two or three steps, the early steps being a
# truncated prefix of the final file, so that blame and "changes per file"
# have something to show. Early commits are therefore not always compilable;
# the final commit is, and equals ./workspace exactly (the script checks).
#
# <target-dir> must be missing or empty. Needs only bash, git and awk.

set -euo pipefail

if [ $# -ne 1 ]; then
  echo "usage: $0 <target-dir>" >&2
  exit 2
fi

SRC="$(cd "$(dirname "$0")" && pwd)/workspace"
mkdir -p "$1"
T="$(cd "$1" && pwd)"
if [ -n "$(ls -A "$T")" ]; then
  echo "make-workspace: $T is not empty" >&2
  exit 1
fi

# Keep the run independent of the machine's git configuration.
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null
git init -q -b main "$T"
git -C "$T" config commit.gpgsign false
git -C "$T" config core.hooksPath /dev/null
git -C "$T" config core.autocrlf false

# put path[:N]  copies a file, or only its first N lines (cut back to the last
# line that is exactly "}") when :N is given.
put() {
  local spec=$1 path n=""
  path=${spec%%:*}
  case $spec in *:*) n=${spec##*:} ;; esac
  mkdir -p "$T/$(dirname "$path")"
  if [ -n "$n" ]; then
    awk -v n="$n" '
      NR <= n { l[NR] = $0; if ($0 == "}") last = NR }
      END { if (!last) last = (n < NR ? n : NR); for (i = 1; i <= last; i++) print l[i] }
    ' "$SRC/$path" > "$T/$path"
  else
    cp -p "$SRC/$path" "$T/$path"
  fi
}

# commit AUTHOR DATE MESSAGE file...
commit() {
  local who=$1 when=$2 msg=$3 name email
  shift 3
  case $who in
    AR) name="A. Rao" ;;
    MC) name="M. Chen" ;;
    JO) name="J. Okafor" ;;
    LP) name="L. Petrov" ;;
    *) echo "unknown author $who" >&2; exit 1 ;;
  esac
  email="$(echo "$who" | tr 'A-Z' 'a-z')@users.noreply.example.com"
  local f
  for f in "$@"; do put "$f"; done
  git -C "$T" add -A
  GIT_AUTHOR_NAME=$name GIT_AUTHOR_EMAIL=$email GIT_AUTHOR_DATE="$when" \
  GIT_COMMITTER_NAME=$name GIT_COMMITTER_EMAIL=$email GIT_COMMITTER_DATE="$when" \
    git -C "$T" commit -q -m "$msg"
}

CA=services/checkout-api
LG=services/ledger
RK=services/risk-engine
NT=services/notify
PC=libs/payments-core

commit AR 2025-12-03T09:12:00+0000 "Start the payments platform" \
  README.md $PC/go.mod $PC/core.go
commit MC 2025-12-10T14:40:00+0000 "payments-core: Gateway interface and an in-process kafka" \
  $PC/gateway/gateway.go $PC/kafka/kafka.go:30 $PC/core_test.go
commit AR 2025-12-18T11:05:00+0000 "ledger: post double entries" \
  $LG/go.mod $LG/ledger/post.go:33
commit MC 2026-01-08T16:20:00+0000 "db: payments and ledger_entries" \
  db/migrations/001_payments.sql db/migrations/002_ledger_entries.sql
commit AR 2026-01-15T10:30:00+0000 "ledger: test that a post writes both sides" \
  $LG/ledger/post_test.go
commit MC 2026-01-27T13:00:00+0000 "checkout-api: module and store" \
  $CA/go.mod $CA/go.sum $CA/internal/store/store.go:60
commit AR 2026-02-09T09:45:00+0000 "checkout-api: PaymentService.Capture" \
  $CA/internal/payments/service.go:83 $CA/internal/payments/idempotency.go
commit MC 2026-02-16T15:10:00+0000 "db: idempotency_keys" \
  db/migrations/003_idempotency_keys.sql
commit MC 2026-02-24T12:25:00+0000 "store: idempotency keys and pending payments" \
  $CA/internal/store/store.go
commit LP 2026-03-05T10:00:00+0000 "gateways: stripe, adyen and a sandbox" \
  $CA/internal/gateway/gateways.go
commit AR 2026-03-12T17:35:00+0000 "capture: tell the world with payment.captured, and test it" \
  $CA/internal/payments/notifier.go $CA/internal/payments/service_test.go
commit JO 2026-03-30T11:50:00+0000 "api: routes, create, get and capture handlers" \
  $CA/internal/api/json.go $CA/internal/api/routes.go $CA/internal/api/create.go \
  $CA/internal/api/capture.go $CA/internal/store/raw.go
commit JO 2026-04-14T14:15:00+0000 "api: refund and admin handlers; refund in the service" \
  $CA/internal/api/refund.go $CA/internal/api/admin.go $CA/internal/api/capture_test.go \
  $CA/internal/payments/service.go
commit MC 2026-04-22T09:20:00+0000 "worker: retry pending captures" \
  $CA/internal/worker/retry.go $CA/internal/worker/retry_test.go
commit AR 2026-05-06T10:40:00+0000 "ledger: reverse refunded payments unless flagged" \
  $LG/ledger/post.go $LG/ledger/frozen.go
commit AR 2026-05-19T13:30:00+0000 "ledger: consume payment.captured, serve entries" \
  $LG/ledger/consumer.go:41 $LG/ledger/http.go $LG/cmd/ledger/main.go
commit LP 2026-06-02T11:15:00+0000 "risk-engine: scoring, flags, fraud_flags table" \
  $RK/go.mod $RK/risk/score.go $RK/risk/flag.go $RK/risk/score_test.go \
  db/migrations/004_fraud_flags.sql
commit LP 2026-06-16T15:45:00+0000 "risk-engine: HTTP service" \
  $RK/cmd/risk-engine/main.go
commit LP 2026-07-07T10:10:00+0000 "notify: payment receipts" \
  $NT/go.mod $NT/notify/consume.go $NT/notify/consume_test.go $NT/cmd/notify/main.go
commit JO 2026-07-21T12:00:00+0000 "billing-web: payments client and checkout page" \
  apps/billing-web/package.json apps/billing-web/tsconfig.json apps/billing-web/src/types.ts \
  apps/billing-web/src/clients/payments.ts apps/billing-web/src/pages/checkout.ts
commit JO 2026-08-11T09:55:00+0000 "openapi: list the checkout-api routes" \
  openapi/checkout-api.yaml
commit LP 2026-08-25T16:30:00+0000 "config: production settings" \
  config/prod.yml
commit MC 2026-09-15T10:05:00+0000 "checkout-api: main with h2c and the retry worker" \
  $CA/cmd/checkout-api/main.go
commit MC 2026-10-06T14:50:00+0000 "payments-core: kafka stand-in drops for slow readers" \
  $PC/kafka/kafka.go
commit AR 2026-10-20T11:20:00+0000 "rules: handlers stay out of the database; no new cycles" \
  .graphlume.yml
commit AR 2026-11-21T09:30:00+0000 "ledger: reconcile logs a short entry count" \
  $LG/ledger/consumer.go

# The generated tree must equal the committed source, and be fully committed.
if ! diff -r -x .git "$SRC" "$T" > /dev/null; then
  echo "make-workspace: generated tree differs from $SRC" >&2
  diff -r -x .git "$SRC" "$T" >&2 || true
  exit 1
fi
if [ -n "$(git -C "$T" status --porcelain)" ]; then
  echo "make-workspace: uncommitted files left over" >&2
  exit 1
fi
echo "make-workspace: $(git -C "$T" rev-list --count HEAD) commits in $T"
