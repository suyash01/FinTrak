-- A transaction's billing cycle must belong to the transaction's own account.
--
-- 000001 states that rule but only carried the tenant FK
-- (user_id, billing_cycle_id) -> billing_cycles (user_id, id), which accepts a
-- cycle of any other account of the same user. Every handler path re-checks the
-- pairing (create/update, the bulk update predicate, the import assignment
-- predicate), but the database allowed a write that bypassed them, and
-- listBillingCycles aggregates transactions WHERE billing_cycle_id = bc.id
-- without re-checking the account, so such a row was counted into the wrong
-- cycle's totals.
--
-- billing_cycles already carries UNIQUE (user_id, account_id, id), which backs
-- the composite FK added below. Existing violations must be cleared first or
-- the constraint cannot be created; the cycle reference is derived from the
-- transaction date, so NULL is the repair (the normal cycle-assignment paths
-- re-derive it) rather than a guess at which account the row belonged to.
UPDATE transactions t
   SET billing_cycle_id = NULL
 WHERE t.billing_cycle_id IS NOT NULL
   AND NOT EXISTS (
       SELECT 1 FROM billing_cycles bc
        WHERE bc.id = t.billing_cycle_id
          AND bc.user_id = t.user_id
          AND bc.account_id = t.account_id);

-- The narrower tenant FK is subsumed by the composite one; keeping it would
-- only add a redundant index probe per write.
ALTER TABLE transactions
    DROP CONSTRAINT IF EXISTS transactions_billing_cycle_tenant_fkey;

-- MATCH SIMPLE leaves rows with a NULL cycle untouched, while a non-NULL cycle
-- must now match the transaction's own account. Deleting a cycle still clears
-- only the cycle column, since account_id and user_id stay NOT NULL.
ALTER TABLE transactions
    ADD CONSTRAINT transactions_billing_cycle_account_fkey
        FOREIGN KEY (user_id, account_id, billing_cycle_id)
        REFERENCES billing_cycles (user_id, account_id, id)
        ON DELETE SET NULL (billing_cycle_id);
