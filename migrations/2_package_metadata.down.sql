BEGIN;

ALTER TABLE packages
    DROP COLUMN usd_to_rub_rate,
    DROP COLUMN calculated_at,
    DROP COLUMN carrier_id;

COMMIT;
