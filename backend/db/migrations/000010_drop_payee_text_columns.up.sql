-- Payee name is sourced from the payees table via payee_id; the denormalized text columns are unused.
ALTER TABLE transactions DROP COLUMN IF EXISTS payee;
ALTER TABLE rules DROP COLUMN IF EXISTS payee;
