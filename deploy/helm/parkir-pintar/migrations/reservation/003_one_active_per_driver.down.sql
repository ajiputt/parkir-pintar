SET search_path TO reservation, public;

DROP INDEX IF EXISTS one_active_reservation_per_driver;
