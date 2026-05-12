SET search_path TO billing, public;

DROP INDEX IF EXISTS invoice_overdue_driver_idx;
DROP INDEX IF EXISTS invoice_issued_age_idx;

ALTER TABLE invoice DROP COLUMN IF EXISTS overdue_at;
ALTER TABLE invoice DROP COLUMN IF EXISTS payment_mode;

ALTER TABLE invoice DROP CONSTRAINT IF EXISTS invoice_status_check;
ALTER TABLE invoice ADD CONSTRAINT invoice_status_check
    CHECK (status IN ('DRAFT','ISSUED','PAID','VOID'));
