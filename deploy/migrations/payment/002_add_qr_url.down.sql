SET search_path TO payment, public;

ALTER TABLE payment DROP COLUMN IF EXISTS qr_url;
