-- Data repair: the date-based backfill in ensureBillingCycles previously
-- joined billing_cycles WITHOUT scoping the cycle to the transaction's own
-- account, so imports on billing-day accounts could be attached to ANOTHER
-- account's cycle. Symptoms: the real account's cycles show the transactions
-- as unassigned, while the other account's cycle totals are inflated.
--
-- Step 1: detach every transaction that points at a cycle of a different
-- account or user. Step 2: re-attach every unassigned transaction to its own
-- account's cycle whose date range contains its transaction date (the same
-- rule the fixed ensureBillingCycles applies on every request).
--
-- Idempotent: safe on fresh installs (no rows) and on environments where the
-- bug never occurred (no mismatches, nothing to re-attach).

UPDATE transactions t SET billing_cycle_id = NULL
FROM billing_cycles bc
WHERE t.billing_cycle_id = bc.id
  AND (bc.account_id IS DISTINCT FROM t.account_id
       OR bc.user_id IS DISTINCT FROM t.user_id);

UPDATE transactions t SET billing_cycle_id = bc.id
FROM billing_cycles bc
WHERE t.billing_cycle_id IS NULL
  AND bc.account_id = t.account_id
  AND bc.user_id = t.user_id
  AND t.date >= bc.start_date
  AND t.date <= bc.end_date;