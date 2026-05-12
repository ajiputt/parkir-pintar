-- Rollback Path A migration. Restore end_at column + EXCLUDE GIST constraint.
--
-- WARNING: kalau ada production data tanpa end_at, restore butuh seed value.
-- Untuk demo/dev, kita pakai end_at = start_at + 24h sebagai default.

SET search_path TO reservation, public;

-- 1. Drop partial unique index.
DROP INDEX IF EXISTS one_active_reservation_per_spot;

-- 2. Restore end_at column. NOT NULL constraint butuh seed default dulu.
ALTER TABLE reservation ADD COLUMN IF NOT EXISTS end_at TIMESTAMPTZ;
UPDATE reservation SET end_at = start_at + INTERVAL '24 hours' WHERE end_at IS NULL;
ALTER TABLE reservation ALTER COLUMN end_at SET NOT NULL;

-- 3. Restore EXCLUDE GIST constraint (unnamed, sama seperti migration 001).
ALTER TABLE reservation ADD
    EXCLUDE USING gist (
        spot_id WITH =,
        tstzrange(start_at, end_at, '[)') WITH &&
    ) WHERE (state IN ('CONFIRMED','CHECKED_IN'));
