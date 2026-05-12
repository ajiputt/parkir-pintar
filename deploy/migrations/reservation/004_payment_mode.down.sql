SET search_path TO reservation, public;
ALTER TABLE reservation DROP COLUMN IF EXISTS payment_mode;
