CREATE TABLE ledger_entries (
    id            bigserial PRIMARY KEY,
    payment_id    text NOT NULL REFERENCES payments (id),
    account       text NOT NULL,
    direction     text NOT NULL CHECK (direction IN ('debit', 'credit')),
    amount_cents  bigint NOT NULL,
    currency      char(3) NOT NULL,
    posted_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ledger_entries_payment_idx ON ledger_entries (payment_id);
