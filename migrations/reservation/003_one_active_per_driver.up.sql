-- Migration 003: 1 driver = 1 active reservation
--
-- Rationale: domain rule baru — driver tidak bisa booking spot kedua sebelum
-- check-out atau cancel reservation aktif. Defense-in-depth dengan application
-- pre-check (lihat services/reservation/internal/usecase/create_reservation.go).
--
-- Pattern sama dengan one_active_reservation_per_spot (migration 002) — partial
-- unique index hanya cover state aktif, jadi driver bisa punya banyak history
-- reservation lama (CHECKED_OUT, CANCELLED, EXPIRED).

SET search_path TO reservation, public;

CREATE UNIQUE INDEX IF NOT EXISTS one_active_reservation_per_driver
    ON reservation (driver_id)
    WHERE state IN ('CONFIRMED', 'CHECKED_IN');

COMMENT ON INDEX one_active_reservation_per_driver IS
    'Domain rule: 1 driver = 1 active reservation. Lihat ADR-0011.';
