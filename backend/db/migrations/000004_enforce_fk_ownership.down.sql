-- Reverse 000004_enforce_fk_ownership: drop composite FKs, restore the original
-- single-column FKs, drop the UNIQUE (id, user_id) constraints, and make
-- user_id nullable again.

-- Drop composite FKs.
ALTER TABLE links          DROP CONSTRAINT IF EXISTS fk_links_to_txn_owner;
ALTER TABLE links          DROP CONSTRAINT IF EXISTS fk_links_from_txn_owner;
ALTER TABLE rules          DROP CONSTRAINT IF EXISTS fk_rules_payee_owner;
ALTER TABLE rules          DROP CONSTRAINT IF EXISTS fk_rules_category_owner;
ALTER TABLE payees         DROP CONSTRAINT IF EXISTS fk_payees_account_owner;
ALTER TABLE categories     DROP CONSTRAINT IF EXISTS fk_categories_parent_owner;
ALTER TABLE transactions   DROP CONSTRAINT IF EXISTS fk_transactions_billing_cycle_owner;
ALTER TABLE transactions   DROP CONSTRAINT IF EXISTS fk_transactions_payee_owner;
ALTER TABLE transactions   DROP CONSTRAINT IF EXISTS fk_transactions_category_owner;
ALTER TABLE transactions   DROP CONSTRAINT IF EXISTS fk_transactions_account_owner;

-- Drop UNIQUE (id, user_id) constraints.
ALTER TABLE transactions   DROP CONSTRAINT IF EXISTS uq_transactions_id_user;
ALTER TABLE billing_cycles DROP CONSTRAINT IF EXISTS uq_billing_cycles_id_user;
ALTER TABLE payees         DROP CONSTRAINT IF EXISTS uq_payees_id_user;
ALTER TABLE categories     DROP CONSTRAINT IF EXISTS uq_categories_id_user;
ALTER TABLE accounts       DROP CONSTRAINT IF EXISTS uq_accounts_id_user;

-- Make user_id nullable again.
ALTER TABLE billing_cycles ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE links          ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE rules          ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE transactions   ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE payees         ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE categories     ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE accounts       ALTER COLUMN user_id DROP NOT NULL;

-- Restore the original single-column FKs.
ALTER TABLE transactions ADD CONSTRAINT transactions_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE transactions ADD CONSTRAINT transactions_category_id_fkey FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE SET NULL;
ALTER TABLE transactions ADD CONSTRAINT transactions_payee_id_fkey FOREIGN KEY (payee_id) REFERENCES payees(id) ON DELETE SET NULL;
ALTER TABLE transactions ADD CONSTRAINT transactions_billing_cycle_id_fkey FOREIGN KEY (billing_cycle_id) REFERENCES billing_cycles(id) ON DELETE SET NULL;
ALTER TABLE categories    ADD CONSTRAINT categories_parent_id_fkey FOREIGN KEY (parent_id) REFERENCES categories(id) ON DELETE SET NULL;
ALTER TABLE payees        ADD CONSTRAINT payees_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE SET NULL;
ALTER TABLE rules         ADD CONSTRAINT rules_category_id_fkey FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE CASCADE;
ALTER TABLE rules         ADD CONSTRAINT rules_payee_id_fkey FOREIGN KEY (payee_id) REFERENCES payees(id) ON DELETE SET NULL;
ALTER TABLE links         ADD CONSTRAINT links_from_txn_id_fkey FOREIGN KEY (from_txn_id) REFERENCES transactions(id) ON DELETE CASCADE;
ALTER TABLE links         ADD CONSTRAINT links_to_txn_id_fkey FOREIGN KEY (to_txn_id) REFERENCES transactions(id) ON DELETE CASCADE;
