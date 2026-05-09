-- Path A migration: drop end_at + EXCLUDE GIST constraint, ganti dengan
-- partial unique index. Lihat docs/architecture/adr/0011-anti-overlap-strategy.md
--
-- Rationale ringkas: parkir realtime (driver gak tau kapan keluar) tidak butuh
-- tsrange anti-overlap. Status atomicity (HELD/OCCUPIED) + 1 active reservation
-- per spot sudah cukup untuk parking domain.

SET search_path TO reservation, public;

-- 1. Drop EXCLUDE constraint (unnamed di migration 001 — drop dynamic by table+definition).
DO $$
DECLARE
    cname TEXT;
BEGIN
    SELECT con.conname INTO cname
    FROM pg_constraint con
    JOIN pg_class rel ON rel.oid = con.conrelid
    JOIN pg_namespace nsp ON nsp.oid = rel.relnamespace
    WHERE rel.relname = 'reservation'
      AND nsp.nspname = 'reservation'
      AND con.contype = 'x'   -- 'x' = exclusion constraint
    LIMIT 1;

    IF cname IS NOT NULL THEN
        EXECUTE format('ALTER TABLE reservation DROP CONSTRAINT %I', cname);
        RAISE NOTICE 'Dropped exclusion constraint: %', cname;
    ELSE
        RAISE NOTICE 'No exclusion constraint found on reservation (already dropped?)';
    END IF;
END $$;

-- 2. Drop end_at column.
ALTER TABLE reservation DROP COLUMN IF EXISTS end_at;

-- 3. Tambah partial unique index sebagai primary anti-overlap mechanism.
--    Hanya 1 reservation aktif (CONFIRMED atau CHECKED_IN) per spot.
--    Reservation lama (CHECKED_OUT, CANCELLED, EXPIRED) tidak masuk index → tidak block insert baru.
CREATE UNIQUE INDEX IF NOT EXISTS one_active_reservation_per_spot
    ON reservation (spot_id)
    WHERE state IN ('CONFIRMED', 'CHECKED_IN');

-- 4. Comment table dengan reference ke ADR.
COMMENT ON INDEX one_active_reservation_per_spot IS
    'Anti-overlap: 1 reservation aktif per spot. Path A — lihat ADR-0011.';
