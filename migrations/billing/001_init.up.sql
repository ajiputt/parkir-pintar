CREATE SCHEMA IF NOT EXISTS billing;
SET search_path TO billing, public;

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE invoice (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    reservation_id  UUID NOT NULL UNIQUE,
    driver_id       TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'DRAFT'
                    CHECK (status IN ('DRAFT','ISSUED','PAID','VOID')),
    total_amount    BIGINT NOT NULL DEFAULT 0,    -- in IDR
    currency        TEXT NOT NULL DEFAULT 'IDR',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    issued_at       TIMESTAMPTZ,
    paid_at         TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX invoice_driver_idx ON invoice(driver_id);
CREATE INDEX invoice_status_idx ON invoice(status);

CREATE TABLE invoice_item (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    invoice_id    UUID NOT NULL REFERENCES invoice(id) ON DELETE CASCADE,
    type          TEXT NOT NULL
                  CHECK (type IN ('BOOKING_FEE','HOURLY','OVERNIGHT','NO_SHOW_PENALTY')),
    description   TEXT NOT NULL,
    amount        BIGINT NOT NULL,
    period_start  TIMESTAMPTZ,
    period_end    TIMESTAMPTZ,
    meta          JSONB,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX invoice_item_invoice_idx ON invoice_item(invoice_id);

-- Append-only events log (event sourcing pattern).
CREATE TABLE events_log (
    seq             BIGSERIAL PRIMARY KEY,
    source_event_id TEXT NOT NULL UNIQUE,        -- envelope.id, untuk dedup
    aggregate_type  TEXT NOT NULL,
    aggregate_id    TEXT NOT NULL,
    event_type      TEXT NOT NULL,
    payload         JSONB NOT NULL,
    occurred_at     TIMESTAMPTZ NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX events_aggregate_idx ON events_log(aggregate_type, aggregate_id, seq);

-- Idempotency keys untuk operasi billing manual (IssueInvoice via API).
CREATE TABLE idempotency_keys (
    key             TEXT PRIMARY KEY,
    request_hash    TEXT NOT NULL,
    response_status INT,
    response_body   JSONB,
    status          TEXT NOT NULL CHECK (status IN ('PROCESSING','COMPLETED','FAILED')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL
);
CREATE INDEX idempotency_expires_idx ON idempotency_keys(expires_at);
