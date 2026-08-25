-- Enforce ownership of every user-owned foreign key.
--
-- PostgreSQL foreign keys prove that a referenced row exists, not that it
-- belongs to the same user. This migration hardens the schema by:
--   1. Making user_id NOT NULL on every user-owned table.
--   2. Adding UNIQUE (id, user_id) constraints on the parent tables so they can
--      be referenced by a composite key.
--   3. Replacing the single-column business FKs with composite (fk_col, user_id)
--      FKs, so the database itself rejects cross-user references.
--
-- The handler layer still validates ownership to return friendly 4xx errors and
-- to allow NULL-clearing of optional FKs; these constraints are defense in depth.

-- 1. Clean up rows that have no owner so user_id can become NOT NULL. These
--    rows are invisible to every user (all queries are scoped by user_id), so
--    they carry no value. Children cascade on delete.
DELETE FROM links         WHERE user_id IS NULL;
DELETE FROM rules         WHERE user_id IS NULL;
DELETE FROM transactions  WHERE user_id IS NULL;
DELETE FROM categories    WHERE user_id IS NULL;
DELETE FROM payees        WHERE user_id IS NULL;
DELETE FROM billing_cycles WHERE user_id IS NULL;
DELETE FROM accounts      WHERE user_id IS NULL;

-- 2. NOT NULL on user_id for every user-owned table.
ALTER TABLE accounts       ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE categories     ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE payees         ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE transactions   ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE rules          ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE links          ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE billing_cycles ALTER COLUMN user_id SET NOT NULL;

-- 3. UNIQUE (id, user_id) on parent tables (required to reference in composite FKs).
ALTER TABLE accounts       ADD CONSTRAINT uq_accounts_id_user       UNIQUE (id, user_id);
ALTER TABLE categories     ADD CONSTRAINT uq_categories_id_user     UNIQUE (id, user_id);
ALTER TABLE payees         ADD CONSTRAINT uq_payees_id_user         UNIQUE (id, user_id);
ALTER TABLE billing_cycles ADD CONSTRAINT uq_billing_cycles_id_user UNIQUE (id, user_id);
ALTER TABLE transactions   ADD CONSTRAINT uq_transactions_id_user   UNIQUE (id, user_id);

-- 4. Drop the old single-column business FKs (replaced by composite ones below).
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_account_id_fkey;
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_category_id_fkey;
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_payee_id_fkey;
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_billing_cycle_id_fkey;
ALTER TABLE categories    DROP CONSTRAINT IF EXISTS categories_parent_id_fkey;
ALTER TABLE payees        DROP CONSTRAINT IF EXISTS payees_account_id_fkey;
ALTER TABLE rules         DROP CONSTRAINT IF EXISTS rules_category_id_fkey;
ALTER TABLE rules         DROP CONSTRAINT IF EXISTS rules_payee_id_fkey;
ALTER TABLE links         DROP CONSTRAINT IF EXISTS links_from_txn_id_fkey;
ALTER TABLE links         DROP CONSTRAINT IF EXISTS links_to_txn_id_fkey;

-- 5. Composite FKs that pair each business reference with user_id so a row can
--    only point at a record owned by the same user.
ALTER TABLE transactions
    ADD CONSTRAINT fk_transactions_account_owner
    FOREIGN KEY (account_id, user_id) REFERENCES accounts(id, user_id) ON DELETE CASCADE;
ALTER TABLE transactions
    ADD CONSTRAINT fk_transactions_category_owner
    FOREIGN KEY (category_id, user_id) REFERENCES categories(id, user_id) ON DELETE SET NULL;
ALTER TABLE transactions
    ADD CONSTRAINT fk_transactions_payee_owner
    FOREIGN KEY (payee_id, user_id) REFERENCES payees(id, user_id) ON DELETE SET NULL;
ALTER TABLE transactions
    ADD CONSTRAINT fk_transactions_billing_cycle_owner
    FOREIGN KEY (billing_cycle_id, user_id) REFERENCES billing_cycles(id, user_id) ON DELETE SET NULL;
ALTER TABLE categories
    ADD CONSTRAINT fk_categories_parent_owner
    FOREIGN KEY (parent_id, user_id) REFERENCES categories(id, user_id) ON DELETE SET NULL;
ALTER TABLE payees
    ADD CONSTRAINT fk_payees_account_owner
    FOREIGN KEY (account_id, user_id) REFERENCES accounts(id, user_id) ON DELETE SET NULL;
ALTER TABLE rules
    ADD CONSTRAINT fk_rules_category_owner
    FOREIGN KEY (category_id, user_id) REFERENCES categories(id, user_id) ON DELETE CASCADE;
ALTER TABLE rules
    ADD CONSTRAINT fk_rules_payee_owner
    FOREIGN KEY (payee_id, user_id) REFERENCES payees(id, user_id) ON DELETE SET NULL;
ALTER TABLE links
    ADD CONSTRAINT fk_links_from_txn_owner
    FOREIGN KEY (from_txn_id, user_id) REFERENCES transactions(id, user_id) ON DELETE CASCADE;
ALTER TABLE links
    ADD CONSTRAINT fk_links_to_txn_owner
    FOREIGN KEY (to_txn_id, user_id) REFERENCES transactions(id, user_id) ON DELETE CASCADE;
