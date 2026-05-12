-- Migration 004: hybrid payment — driver wajib pilih AUTO atau MANUAL saat booking.
-- Lihat ADR-0014.

SET search_path TO reservation, public;

ALTER TABLE reservation
    ADD COLUMN IF NOT EXISTS payment_mode TEXT
        CHECK (payment_mode IN ('AUTO', 'MANUAL'));

-- Backfill existing rows ke AUTO (assumption: existing demo data).
UPDATE reservation SET payment_mode = 'AUTO' WHERE payment_mode IS NULL;

ALTER TABLE reservation ALTER COLUMN payment_mode SET NOT NULL;
