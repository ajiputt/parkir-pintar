#!/usr/bin/env bash
# Seed parking area + 5 floors × (30 mobil + 50 motor) = 750 spots.

set -euo pipefail

DB_URL="${DB_URL:-postgres://gopark:gopark@localhost:5432/gopark?sslmode=disable}"

psql "$DB_URL" <<'SQL'
SET search_path TO reservation, public;

-- Idempotent seed: hapus dulu kalau sudah ada area "main".
DELETE FROM reservation WHERE spot_id IN (
    SELECT s.id FROM spot s
    JOIN floor f ON f.id = s.floor_id
    JOIN parking_area a ON a.id = f.area_id
    WHERE a.name = 'ParkirPintar Main'
);
DELETE FROM spot WHERE floor_id IN (
    SELECT f.id FROM floor f
    JOIN parking_area a ON a.id = f.area_id
    WHERE a.name = 'ParkirPintar Main'
);
DELETE FROM floor WHERE area_id IN (SELECT id FROM parking_area WHERE name = 'ParkirPintar Main');
DELETE FROM parking_area WHERE name = 'ParkirPintar Main';

-- Insert area.
INSERT INTO parking_area (id, name, address, timezone)
VALUES ('11111111-1111-1111-1111-111111111111',
        'ParkirPintar Main',
        'Jl. Sudirman No. 1, Jakarta',
        'Asia/Jakarta');

-- Insert 5 floors.
INSERT INTO floor (area_id, level, car_capacity, motor_capacity)
SELECT '11111111-1111-1111-1111-111111111111', g, 30, 50
FROM generate_series(1, 5) g;

-- Insert spots: 30 mobil + 50 motor per floor.
DO $$
DECLARE
  f RECORD;
  i INT;
BEGIN
  FOR f IN SELECT id, level FROM floor WHERE area_id = '11111111-1111-1111-1111-111111111111' ORDER BY level LOOP
    FOR i IN 1..30 LOOP
      INSERT INTO spot (floor_id, code, vehicle_type)
      VALUES (f.id, format('F%s-C-%s', f.level, lpad(i::text, 3, '0')), 'CAR');
    END LOOP;
    FOR i IN 1..50 LOOP
      INSERT INTO spot (floor_id, code, vehicle_type)
      VALUES (f.id, format('F%s-M-%s', f.level, lpad(i::text, 3, '0')), 'MOTOR');
    END LOOP;
  END LOOP;
END$$;

SELECT
  (SELECT count(*) FROM parking_area WHERE name='ParkirPintar Main') AS areas,
  (SELECT count(*) FROM floor WHERE area_id='11111111-1111-1111-1111-111111111111') AS floors,
  (SELECT count(*) FROM spot s JOIN floor f ON f.id=s.floor_id WHERE f.area_id='11111111-1111-1111-1111-111111111111' AND s.vehicle_type='CAR') AS car_spots,
  (SELECT count(*) FROM spot s JOIN floor f ON f.id=s.floor_id WHERE f.area_id='11111111-1111-1111-1111-111111111111' AND s.vehicle_type='MOTOR') AS motor_spots;
SQL

echo "✅ Seed selesai: 1 area × 5 floor × (30 mobil + 50 motor) = 750 spot"
