ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_billing_cycle_account_fk;
ALTER TABLE billing_cycles DROP CONSTRAINT IF EXISTS billing_cycles_id_account_uq;
