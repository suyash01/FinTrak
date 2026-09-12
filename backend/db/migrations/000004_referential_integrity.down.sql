-- Reverts 000004_referential_integrity: drops the constraints and supporting
-- indexes and undoes the self-link guard. Orphan cleanup performed by the up
-- migration is not reversible (the data was already invalid).
ALTER TABLE links DROP CONSTRAINT IF EXISTS links_no_self_check;

ALTER TABLE rules DROP CONSTRAINT IF EXISTS rules_payee_id_fkey;
ALTER TABLE rules DROP CONSTRAINT IF EXISTS rules_category_id_fkey;
ALTER TABLE payees DROP CONSTRAINT IF EXISTS payees_account_id_fkey;
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_payee_id_fkey;
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_category_id_fkey;

DROP INDEX IF EXISTS rules_payee_idx;
DROP INDEX IF EXISTS rules_category_idx;
