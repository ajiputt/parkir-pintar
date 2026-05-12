-- ParkirPintar — Reservation schema
-- Defense-in-depth: btree_gist untuk EXCLUDE constraint cegah overlap.

CREATE SCHEMA IF NOT EXISTS reservation;
SET search_path TO reservation, public;

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "btree_gist";

-- Parking area: untuk demo kita pakai 1 area saja, tapi tabel disiapkan multi-area-ready.
CREATE TABLE parking_area (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name        TEXT NOT NULL,
    address     TEXT,
    geo         JSONB,
    timezone    TEXT NOT NULL DEFAULT 'Asia/Jakarta',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE floor (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    area_id         UUID NOT NULL REFERENCES parking_area(id) ON DELETE CASCADE,
    level           INT NOT NULL,
    car_capacity    INT NOT NULL DEFAULT 30,
    motor_capacity  INT NOT NULL DEFAULT 50,
    UNIQUE(area_id, level)
);

CREATE TABLE spot (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    floor_id     UUID NOT NULL REFERENCES floor(id) ON DELETE CASCADE,
    code         TEXT NOT NULL,                  -- "F2-C-007"
    vehicle_type TEXT NOT NULL CHECK (vehicle_type IN ('CAR','MOTOR')),
    status       TEXT NOT NULL DEFAULT 'AVAILABLE'
                 CHECK (status IN ('AVAILABLE','HELD','OCCUPIED','OUT_OF_SERVICE')),
    version      INT NOT NULL DEFAULT 0,         -- optimistic lock counter
    UNIQUE(floor_id, code)
);
CREATE INDEX spot_status_idx ON spot(status, vehicle_type);

CREATE TABLE reservation (
    id               UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    driver_id        TEXT NOT NULL,
    spot_id          UUID NOT NULL REFERENCES spot(id),
    plate_no         TEXT NOT NULL,
    vehicle_type     TEXT NOT NULL CHECK (vehicle_type IN ('CAR','MOTOR')),
    state            TEXT NOT NULL DEFAULT 'CONFIRMED'
                     CHECK (state IN ('CONFIRMED','CHECKED_IN','CHECKED_OUT','CANCELLED','EXPIRED')),
    start_at         TIMESTAMPTZ NOT NULL,
    end_at           TIMESTAMPTZ NOT NULL,
    expires_at       TIMESTAMPTZ NOT NULL,        -- hold deadline (now + 1h)
    checkin_at       TIMESTAMPTZ,
    checkout_at      TIMESTAMPTZ,
    assignment_mode  TEXT NOT NULL CHECK (assignment_mode IN ('SYSTEM','USER')),
    idempotency_key  TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- DEFENSE IN DEPTH: cegah overlap reservation aktif untuk spot sama.
    EXCLUDE USING gist (
        spot_id WITH =,
        tstzrange(start_at, end_at, '[)') WITH &&
    ) WHERE (state IN ('CONFIRMED','CHECKED_IN'))
);
CREATE INDEX reservation_driver_idx ON reservation(driver_id, state);
CREATE INDEX reservation_state_expires_idx ON reservation(state, expires_at) WHERE state = 'CONFIRMED';

-- Idempotency keys (untuk CreateReservation, CheckIn, CheckOut, dll).
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

-- Local audit / outbox bila perlu (opsional pattern transactional outbox).
CREATE TABLE outbox (
    id          BIGSERIAL PRIMARY KEY,
    aggregate_type TEXT NOT NULL,
    aggregate_id   UUID NOT NULL,
    event_type     TEXT NOT NULL,
    payload        JSONB NOT NULL,
    occurred_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at   TIMESTAMPTZ,
    attempts       INT NOT NULL DEFAULT 0
);
CREATE INDEX outbox_pending_idx ON outbox(published_at) WHERE published_at IS NULL;

-- Trigger: auto update updated_at.
CREATE OR REPLACE FUNCTION reservation.set_updated_at() RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at := now();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER reservation_updated_at_trigger
BEFORE UPDATE ON reservation
FOR EACH ROW EXECUTE FUNCTION reservation.set_updated_at();
