CREATE TABLE fraud_flags (
    id          bigserial PRIMARY KEY,
    payment_id  text NOT NULL REFERENCES payments (id),
    reason      text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    cleared_at  timestamptz
);
CREATE INDEX fraud_flags_open_idx ON fraud_flags (payment_id) WHERE cleared_at IS NULL;
