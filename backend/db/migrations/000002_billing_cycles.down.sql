DROP INDEX IF EXISTS idx_transactions_billing_cycle_id;
ALTER TABLE transactions DROP COLUMN IF EXISTS billing_cycle_id;
DROP TABLE IF EXISTS billing_cycles;