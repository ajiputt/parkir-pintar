-- Add qr_url column untuk store Midtrans QR image URL.
-- Source: response field actions[].url where name = 'generate-qr-code'.
-- Pre-existing payments (qr_url NULL) backward compatible — kolom optional.

SET search_path TO payment, public;

ALTER TABLE payment ADD COLUMN IF NOT EXISTS qr_url TEXT;
