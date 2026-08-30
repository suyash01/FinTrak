-- Stop accounts and transactions from leaving orphaned dependents behind.
--
-- History: transactions.account_id and links.from_txn_id/to_txn_id were
-- created WITHOUT foreign keys, so deleting an account orphaned its
-- transactions (invisible in listings because GetTransactions inner-joins
-- accounts, yet still counted in dashboard COUNT/SUM aggregates) and deleting
-- a transaction orphaned its links. billing_cycles already cascades from
-- accounts, so this migration only covers the two dangling classes.
--
-- Step 1: purge any orphans that previous versions already left behind
-- (the DELETE statements are no-ops on healthy databases). They must run
-- before the constraints go in, or the ADD CONSTRAINT steps would fail.

DELETE FROM links
 WHERE from_txn_id NOT IN (SELECT id FROM transactions)
    OR to_txn_id NOT IN (SELECT id FROM transactions);

DELETE FROM transactions
 WHERE account_id NOT IN (SELECT id FROM accounts);

-- Step 2: enforce the relationships so future deletions cascade.

ALTER TABLE transactions
    ADD CONSTRAINT transactions_account_fk
    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;

ALTER TABLE links
    ADD CONSTRAINT links_from_txn_fk
    FOREIGN KEY (from_txn_id) REFERENCES transactions(id) ON DELETE CASCADE;

ALTER TABLE links
    ADD CONSTRAINT links_to_txn_fk
    FOREIGN KEY (to_txn_id) REFERENCES transactions(id) ON DELETE CASCADE;