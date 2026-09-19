-- Restore the original tenant-only billing-cycle FK from 000001.
--
-- The repair in the up migration is not reversible: rows whose
-- billing_cycle_id was nulled stay NULL, because the cycle a row belonged to
-- cannot be recovered from the row itself. They are re-assigned by the normal
-- cycle-assignment paths, which re-derive the cycle from the transaction date.
ALTER TABLE transactions
    DROP CONSTRAINT IF EXISTS transactions_billing_cycle_account_fkey;

ALTER TABLE transactions
    ADD CONSTRAINT transactions_billing_cycle_tenant_fkey
        FOREIGN KEY (user_id, billing_cycle_id)
        REFERENCES billing_cycles (user_id, id)
        ON DELETE SET NULL (billing_cycle_id);
