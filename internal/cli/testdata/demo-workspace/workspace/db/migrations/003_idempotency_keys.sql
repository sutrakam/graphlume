CREATE TABLE idempotency_keys (
    key         text PRIMARY KEY,
    payment_id  text NOT NULL REFERENCES payments (id),
    created_at  timestamptz NOT NULL DEFAULT now()
);
