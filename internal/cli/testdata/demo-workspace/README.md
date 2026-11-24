# Demo workspace test data

A small, invented monorepo, "payments-platform", used by the CLI tests. It is public and fictional: nothing in it comes from a real codebase.

## What is committed, what is generated

- `workspace/` holds the **source files**, committed here. The Go modules build and pass `go test` (the TypeScript passes `tsc` after `npm install`).
- `make-workspace.sh` **generates the git history** into a directory you name. The history is not committed, because a repository inside this one would become a submodule.

```
internal/cli/testdata/demo-workspace/make-workspace.sh <target-dir>    # target missing or empty
```

The result is the `workspace/` tree plus a `.git` with 26 commits by four invented authors (A. Rao, M. Chen, J. Okafor, L. Petrov; `*@users.noreply.example.com`), dated December 2025 to November 2026. It is deterministic: the same commit hashes every run. The script checks that the generated tree equals `workspace/` and that nothing is left uncommitted. Early commits of a few files are truncated prefixes, so only the final commit is compilable.

**Tests call the script**: create a `t.TempDir()`, run `make-workspace.sh <dir>`, then run GraphLume against `<dir>` (`graphlume init --index --project <dir>`). Never index `workspace/` in place, which would write `.graphlume/` into the repository.

## Shape

| Path | Language | Notes |
|---|---|---|
| `apps/billing-web` | TypeScript | `BillingClient.Capture` sends `PUT`; the route is `POST` (contract mismatch). `lodash 4.17.15` is old. |
| `services/checkout-api` | Go | `cmd`, `internal/{payments,api,worker,store,gateway}`. `PaymentService.Capture`, `CaptureHandler.Serve`, `RetryWorker.Run`, `Idempotency.Check`, `Notifier.Emit`. `golang.org/x/net v0.17.0` is old. |
| `services/ledger` | Go | `Ledger.Post`, `Ledger.Reverse`. One author wrote all of it (bus factor 1). |
| `services/risk-engine`, `services/notify` | Go | Writes `fraud_flags`; consumes `payment.captured`. |
| `libs/payments-core` | Go | Shared types, the `Gateway` interface (3 implementations in checkout-api), an in-process Kafka stand-in. |
| `db/migrations` | SQL | `payments`, `ledger_entries`, `idempotency_keys`, `fraud_flags`. |
| `openapi/`, `config/prod.yml` | | Route list; settings. |
| `.graphlume.yml` | | Two rules: handlers never touch the store (layering), and no new cycles. |

Planted facts a test can rely on:

- Route `POST /v1/payments/{id}/capture`; `payment.captured` has 1 producer and 2 consumers.
- 3 layering violations, all in `internal/api` (`capture.go`, `refund.go`, `admin.go`): `graphlume policy check` fails with 3 errors.
- `LEDGER_FAST_PATH` is read but not declared; `AUDIT_SAMPLE_RATE` is declared but never read.
- Tables: `payments`, `ledger_entries`, `idempotency_keys`, `fraud_flags`.
