# payments-platform (synthetic fixture)

An invented monorepo used to test GraphLume Studio. Nothing here is real.

| Folder | What it is |
|---|---|
| `apps/billing-web` | TypeScript client; calls the capture route with PUT (a contract mismatch) |
| `services/checkout-api` | Go: `cmd`, `internal/{payments,api,worker,store,gateway}` |
| `services/ledger`, `risk-engine`, `notify` | Go services |
| `libs/payments-core` | Shared Go types, the `Gateway` interface, an in-process Kafka stand-in |
| `db/migrations` | SQL for `payments`, `ledger_entries`, `idempotency_keys`, `fraud_flags` |
| `config/prod.yml`, `openapi/` | Settings and the route list |

Planted facts: three handlers run SQL directly (layering rule), `LEDGER_FAST_PATH` is read but not declared,
`AUDIT_SAMPLE_RATE` is declared but never read, `golang.org/x/net v0.17.0` and `lodash 4.17.15` are old.
