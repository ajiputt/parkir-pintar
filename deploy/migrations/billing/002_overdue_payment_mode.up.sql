-- Migration 002: hybrid payment + overdue handling.
-- Lihat ADR-0014.

SET search_path TO billing, public;

-- 1. Drop old check constraint, replace dengan OVERDUE included.
ALTER TABLE invoice DROP CONSTRAINT IF EXISTS invoice_status_check;
ALTER TABLE invoice ADD CONSTRAINT invoice_status_check
    CHECK (status IN ('DRAFT','ISSUED','PAID','VOID','OVERDUE'));

-- 2. Tambah payment_mode (AUTO atau MANUAL, dari reservation).
ALTER TABLE invoice
    ADD COLUMN IF NOT EXISTS payment_mode TEXT
        CHECK (payment_mode IN ('AUTO', 'MANUAL'));

-- Backfill existing → AUTO (asumsi demo).
UPDATE invoice SET payment_mode = 'AUTO' WHERE payment_mode IS NULL;
ALTER TABLE invoice ALTER COLUMN payment_mode SET NOT NULL;

-- 3. Tambah overdue_at timestamp (kapan jadi OVERDUE).
ALTER TABLE invoice ADD COLUMN IF NOT EXISTS overdue_at TIMESTAMPTZ;

-- 4. Index untuk overdue worker (scan invoice ISSUED + age > grace).
CREATE INDEX IF NOT EXISTS invoice_issued_age_idx
    ON invoice(issued_at)
    WHERE status = 'ISSUED' AND payment_mode = 'MANUAL';

-- 5. Index untuk driver blocking check (count OVERDUE per driver).
CREATE INDEX IF NOT EXISTS invoice_overdue_driver_idx
    ON invoice(driver_id)
    WHERE status = 'OVERDUE';
