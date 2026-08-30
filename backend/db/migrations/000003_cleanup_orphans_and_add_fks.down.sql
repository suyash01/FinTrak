-- Restore the pre-000003 state: no FK enforcement on these columns.

ALTER TABLE links DROP CONSTRAINT links_to_txn_fk;
ALTER TABLE links DROP CONSTRAINT links_from_txn_fk;
ALTER TABLE transactions DROP CONSTRAINT transactions_account_fk;