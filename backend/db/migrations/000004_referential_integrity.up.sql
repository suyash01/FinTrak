-- Referential integrity for category/payee references.
--
-- Until now transactions.category_id, transactions.payee_id, payees.account_id,
-- rules.category_id, and rules.payee_id were plain UUID columns with no foreign
-- keys, so orphaned references were prevented only by handler logic. This
-- migration cleans up any existing orphans and then enforces the relationships
-- at the database layer.
--
-- Delete semantics mirror the handlers:
--   * transactions.category_id / payee_id -> ON DELETE SET NULL (a deleted
--     category/payee simply un-categorizes the transaction)
--   * payees.account_id -> ON DELETE SET NULL (an account-linked payee outlives
--     its account)
--   * rules.category_id -> ON DELETE CASCADE (NOT NULL; the handler already
--     deletes referencing rules when a category is removed)
--   * rules.payee_id -> ON DELETE SET NULL
--
-- Cross-tenant references (a row pointing at another user's category/payee)
-- remain enforced in application code: categories and payees may be global
-- (user_id IS NULL), so a plain composite (id, user_id) FK would reject
-- legitimate global references.
--
-- Also rejects self-links (from_txn_id = to_txn_id), which corrupt transfer and
-- cashback semantics.

-- 1. Clean up any pre-existing orphaned references so the constraints apply.
UPDATE transactions SET category_id = NULL
WHERE category_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM categories c WHERE c.id = transactions.category_id);

UPDATE transactions SET payee_id = NULL
WHERE payee_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM payees p WHERE p.id = transactions.payee_id);

UPDATE payees SET account_id = NULL
WHERE account_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM accounts a WHERE a.id = payees.account_id);

UPDATE rules SET payee_id = NULL
WHERE payee_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM payees p WHERE p.id = rules.payee_id);

DELETE FROM rules
WHERE NOT EXISTS (SELECT 1 FROM categories c WHERE c.id = rules.category_id);

DELETE FROM links WHERE from_txn_id = to_txn_id;

-- 2. Enforce the relationships.
ALTER TABLE transactions
    ADD CONSTRAINT transactions_category_id_fkey
    FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE SET NULL;

ALTER TABLE transactions
    ADD CONSTRAINT transactions_payee_id_fkey
    FOREIGN KEY (payee_id) REFERENCES payees(id) ON DELETE SET NULL;

ALTER TABLE payees
    ADD CONSTRAINT payees_account_id_fkey
    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE SET NULL;

ALTER TABLE rules
    ADD CONSTRAINT rules_category_id_fkey
    FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE CASCADE;

ALTER TABLE rules
    ADD CONSTRAINT rules_payee_id_fkey
    FOREIGN KEY (payee_id) REFERENCES payees(id) ON DELETE SET NULL;

ALTER TABLE links
    ADD CONSTRAINT links_no_self_check CHECK (from_txn_id <> to_txn_id);

-- Indexes to support the new FK lookups (Postgres does not create these
-- automatically for the referencing side).
CREATE INDEX rules_category_idx ON rules (category_id);
CREATE INDEX rules_payee_idx ON rules (payee_id) WHERE payee_id IS NOT NULL;
