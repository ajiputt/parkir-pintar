CREATE SCHEMA IF NOT EXISTS payment;
SET search_path TO payment, public;

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE payment (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    invoice_id    UUID NOT NULL,
    method        TEXT NOT NULL CHECK (method IN ('QRIS')),
    gateway       TEXT NOT NULL DEFAULT 'MIDTRANS',
    gateway_ref   TEXT,                            -- transaction id dari Midtrans
    qr_string     TEXT,                            -- raw QRIS payload
    amount        BIGINT NOT NULL,
    currency      TEXT NOT NULL DEFAULT 'IDR',
    status        TEXT NOT NULL DEFAULT 'PENDING'
                  CHECK (status IN ('PENDING','SUCCESS','FAILED','EXPIRED')),
    idempotency_key TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    settled_at    TIMESTAMPTZ,
    expires_at    TIMESTAMPTZ
);
CREATE INDEX payment_invoice_idx ON payment(invoice_id);
CREATE INDEX payment_status_idx ON payment(status);

CREATE TABLE webhook_log (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    payment_id   UUID,
    source       TEXT NOT NULL,
    signature    TEXT,
    raw_payload  JSONB NOT NULL,
    verified     BOOLEAN NOT NULL DEFAULT false,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX webhook_payment_idx ON webhook_log(payment_id);

CREATE TABLE idempotency_keys (
    key             TEXT PRIMARY KEY,
    request_hash    TEXT NOT NULL,
    response_status INT,
    response_body   JSONB,
    status          TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL
);
