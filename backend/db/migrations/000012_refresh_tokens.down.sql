DROP TABLE IF EXISTS refresh_tokens;
ALTER TABLE transactions DROP COLUMN IF EXISTS billing_cycle_detached;
