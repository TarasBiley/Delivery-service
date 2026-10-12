BEGIN;

ALTER TABLE packages
    ADD COLUMN carrier_id BIGINT NULL CHECK (carrier_id > 0),
    ADD COLUMN calculated_at TIMESTAMPTZ NULL,
    ADD COLUMN usd_to_rub_rate NUMERIC NULL;

COMMIT;
