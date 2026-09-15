-- H-1: a transaction's billing cycle must belong to the transaction's own
-- account. This was previously enforced only in handler code, so a crafted
-- request could attach an account A transaction to an account B cycle,
-- corrupting account B's cycle totals (which aggregate by cycle id).
--
-- The composite FK targets (id, account_id) on billing_cycles. The default
-- MATCH SIMPLE semantics exempt a row whenever any referencing column is NULL,
-- so transactions with billing_cycle_id IS NULL are untouched while a non-NULL
-- cycle must match the transaction's account. ON DELETE SET NULL (billing_cycle_id)
-- detaches only the cycle reference if a cycle is ever removed, leaving the
-- transaction in place.
--
-- Pre-existing cross-account assignments are detached first so the constraint
-- can be added to a database that predates it (a fresh database has none).
UPDATE transactions t
   SET billing_cycle_id = NULL
  FROM billing_cycles bc
 WHERE t.billing_cycle_id = bc.id
   AND t.account_id <> bc.account_id;

ALTER TABLE billing_cycles
    ADD CONSTRAINT billing_cycles_id_account_uq UNIQUE (id, account_id);

ALTER TABLE transactions
    ADD CONSTRAINT transactions_billing_cycle_account_fk
    FOREIGN KEY (billing_cycle_id, account_id)
    REFERENCES billing_cycles (id, account_id)
    ON DELETE SET NULL (billing_cycle_id);
