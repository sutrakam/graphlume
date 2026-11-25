CREATE TABLE payments (
    id            text PRIMARY KEY,
    amount_cents  bigint NOT NULL,
    currency      char(3) NOT NULL,
    status        text NOT NULL,
    reference     text,
    captured_at   timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX payments_status_idx ON payments (status);
