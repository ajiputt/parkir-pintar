CREATE SCHEMA IF NOT EXISTS notification;
SET search_path TO notification, public;

-- user_contact — embedded user data (no separate user service for now).
-- Lihat ADR-0013 untuk rationale + migration path ke user service.
CREATE TABLE IF NOT EXISTS user_contact (
    driver_id   TEXT PRIMARY KEY,
    email       TEXT NOT NULL,
    name        TEXT,
    phone       TEXT,
    opt_in      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS user_contact_email_idx ON user_contact(email);

-- notification_log — audit trail per notification dispatch.
-- event_id UNIQUE = idempotency: NATS redelivery same event_id → skip.
-- No retry: status terminal (SENT or FAILED). Failed → DLQ subject + manual replay.
CREATE TABLE IF NOT EXISTS notification_log (
    id           UUID PRIMARY KEY DEFAULT public.uuid_generate_v4(),
    driver_id    TEXT NOT NULL,
    kind         TEXT NOT NULL
                 CHECK (kind IN (
                    'RESERVATION_CONFIRMED',
                    'RESERVATION_EXPIRED',
                    'INVOICE_ISSUED',
                    'PAYMENT_SUCCEEDED',
                    'PAYMENT_FAILED'
                 )),
    channel      TEXT NOT NULL DEFAULT 'EMAIL'
                 CHECK (channel IN ('EMAIL','SMS','PUSH')),
    subject      TEXT,
    body         TEXT,
    status       TEXT NOT NULL DEFAULT 'PENDING'
                 CHECK (status IN ('PENDING','SENT','FAILED')),
    sent_at      TIMESTAMPTZ,
    last_error   TEXT,
    event_id     TEXT NOT NULL UNIQUE,  -- dedup, sumber dari NATS env.ID
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS notif_log_driver_idx ON notification_log(driver_id, created_at DESC);
CREATE INDEX IF NOT EXISTS notif_log_status_idx ON notification_log(status) WHERE status = 'FAILED';
CREATE INDEX IF NOT EXISTS notif_log_kind_idx ON notification_log(kind, created_at DESC);

-- Demo seed: contact untuk driver yang dipakai di Postman collection.
INSERT INTO user_contact (driver_id, email, name) VALUES
    ('driver-postman-1',  'ajiperdanaputra90@gmail.com', 'Aji (Postman 1)'),
    ('driver-postman-2',  'demo2@example.com',           'Driver Postman 2'),
    ('driver-idem-test',  'idem@example.com',            'Idem Tester')
ON CONFLICT (driver_id) DO UPDATE
    SET email = EXCLUDED.email,
        name = EXCLUDED.name,
        updated_at = now();
