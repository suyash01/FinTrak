-- FinTrak canonical schema (squashed).
-- Single baseline migration merging the full migration history:
--   * users, account types, accounts (incl. billing_day and closed),
--     billing cycles, category groups (replacing the legacy categories.type
--     column and the never-used categories.parent_id), categories, payees,
--     transactions, rules, links
--   * loan/EMI accounts: a closed flag and the loan_attachments junction that
--     attaches a transaction to exactly one loan account
--   * per-owner payee name uniqueness ((user_id, name)) instead of the
--     column-wide UNIQUE on payees.name
--   * foreign keys with cascade/set-null semantics: transactions.account_id ->
--     accounts, links.from_txn_id/to_txn_id -> transactions, and the
--     category/payee references on transactions/rules/payees; links also
--     reject self-links (from_txn_id = to_txn_id)
--   * rules.match_type without the never-implemented 'regex' value
--   * transaction amounts stored as integer minor units (BIGINT cents)
--   * the query and performance indexes added for the dominant
--     listing/aggregate/suggestion patterns
-- The historical orphan-cleanup and billing-cycle data-repair statements are
-- no-ops on a fresh database and are intentionally not carried over.
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Users (authentication)
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    paperless_url VARCHAR(500) DEFAULT '',
    paperless_token TEXT DEFAULT '',
    paperless_tag VARCHAR(255) DEFAULT '',
    page_size INTEGER,
    role VARCHAR(20) NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Account types (reference data, global)
-- Credit card statements typically export purchases as negative amounts and
-- payments/refunds as positive, so the default convention makes a positive
-- amount on a credit card mean a credit and a negative amount mean a debit.
-- The built-in rows are seeded by db.SeedAccountTypes on every boot (see
-- backend/db/seed.go) rather than in this migration.
CREATE TABLE IF NOT EXISTS account_types (
    id VARCHAR(30) PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    positive_txn_type VARCHAR(6) NOT NULL CHECK (positive_txn_type IN ('credit', 'debit'))
);

-- Accounts
-- billing_day configures the credit-card statement day (NULL otherwise).
-- closed marks an account as closed: transactions can no longer be added,
-- removed, or edited on it (linking remains possible).
CREATE TABLE IF NOT EXISTS accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    account_type_id VARCHAR(30) NOT NULL REFERENCES account_types(id),
    bank VARCHAR(100),
    currency VARCHAR(3) DEFAULT 'INR',
    color VARCHAR(7) DEFAULT '#06b6d4',
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    billing_day INTEGER,
    closed BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    UNIQUE (id, user_id)
);

-- Billing cycles (credit-card accounts)
-- An explicit, persisted period (start_date..end_date) that transactions are
-- attached to via transactions.billing_cycle_id. Cycles are auto-generated on
-- the 1st of each month; the assignment can be changed manually.
CREATE TABLE IF NOT EXISTS billing_cycles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    label VARCHAR(255) DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (account_id, start_date),
    UNIQUE (id, user_id)
);

-- Category groups: a first-class, user-manageable grouping concept.
-- The four immutable base groups (income, expense, transfer, cashback) are
-- seeded globally by db.SeedCategoryGroups on every boot (see
-- backend/db/seed.go) rather than in this migration. Users may add their own
-- custom groups; a NULL user_id marks a global row.
CREATE TABLE IF NOT EXISTS category_groups (
    id VARCHAR(50) PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    icon VARCHAR(50),
    color VARCHAR(7),
    is_base BOOLEAN NOT NULL DEFAULT FALSE,
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- A global/base group must be unique by id; a user's custom groups are unique
-- per (id, user_id). Postgres treats NULLs as distinct in unique indexes, so use
-- partial indexes.
CREATE UNIQUE INDEX IF NOT EXISTS category_groups_global_id_uq
    ON category_groups (id) WHERE user_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS category_groups_user_id_uq
    ON category_groups (id, user_id) WHERE user_id IS NOT NULL;

-- Categories. user_id is nullable so a global (admin-created) category can be
-- added by an admin; a NULL user_id marks a global row. Categories are flat:
-- they belong to exactly one group (group_id), with no parent/child hierarchy.
CREATE TABLE IF NOT EXISTS categories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL,
    icon VARCHAR(50),
    color VARCHAR(7),
    group_id VARCHAR(50) NOT NULL REFERENCES category_groups(id),
    user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    UNIQUE (id, user_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS categories_global_id_uq
    ON categories (id) WHERE user_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS categories_user_id_uq
    ON categories (id, user_id) WHERE user_id IS NOT NULL;

-- Payees. account_id is NULL for manually-created payees and references the
-- linked account otherwise (an account-linked payee outlives its account).
CREATE TABLE IF NOT EXISTS payees (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    UNIQUE (id, user_id)
);

-- Payee names are unique per owner: two users may each have a "Starbucks",
-- but one user cannot create two of the same name. The historical column-wide
-- UNIQUE on payees.name is intentionally absent; this index enforces the
-- per-owner scope (its name is referenced by handler code/tests — keep it).
CREATE UNIQUE INDEX IF NOT EXISTS payees_user_name_uq
    ON payees (user_id, name);

-- One account-linked payee per account (account_id is NULL for manually-created
-- payees), backing the ON CONFLICT upsert in CreateAccount.
CREATE UNIQUE INDEX IF NOT EXISTS payees_account_id_uq
    ON payees (account_id) WHERE account_id IS NOT NULL;

-- Transactions. amount is stored as integer minor units (cents) so
-- aggregation, balance, and transfer-scoring math use exact integer arithmetic.
-- category_id/payee_id references are set to NULL when the target is removed.
CREATE TABLE IF NOT EXISTS transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    date DATE NOT NULL,
    description TEXT NOT NULL,
    amount BIGINT NOT NULL,
    type VARCHAR(10) NOT NULL CHECK (type IN ('debit', 'credit')),
    category_id UUID REFERENCES categories(id) ON DELETE SET NULL,
    tags TEXT[] DEFAULT '{}',
    notes TEXT DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    payee_id UUID REFERENCES payees(id) ON DELETE SET NULL,
    billing_cycle_id UUID,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    UNIQUE (id, user_id)
);

-- Rules. category_id cascades with its category (NOT NULL); payee_id is set to
-- NULL when its payee is removed. 'regex' is intentionally not a valid
-- match_type: no code path ever implemented it.
CREATE TABLE IF NOT EXISTS rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pattern VARCHAR(500) NOT NULL,
    match_type VARCHAR(20) DEFAULT 'contains' CHECK (match_type IN ('contains', 'starts_with', 'exact')),
    category_id UUID NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    payee_id UUID REFERENCES payees(id) ON DELETE SET NULL,
    priority INT DEFAULT 0,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    UNIQUE (id, user_id)
);

-- Links. A link joins two distinct transactions; self-links are rejected.
CREATE TABLE IF NOT EXISTS links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type VARCHAR(20) NOT NULL CHECK (type IN ('transfer', 'cashback', 'refund', 'bill_payment')),
    from_txn_id UUID NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    to_txn_id UUID NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    notes TEXT DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    UNIQUE (id, user_id),
    CONSTRAINT links_no_self_check CHECK (from_txn_id <> to_txn_id)
);

-- Loan/EMI attachments. The junction attaches a transaction (an EMI payment,
-- which lives on its own account) to exactly one loan account. The UNIQUE on
-- transaction_id enforces "one transaction -> one loan account" at the
-- database level.
CREATE TABLE IF NOT EXISTS loan_attachments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    loan_account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    transaction_id UUID NOT NULL UNIQUE REFERENCES transactions(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (id, user_id)
);

-- Query indexes for the dominant access patterns.
-- The UNIQUE (id, user_id) indexes above are useless for queries that
-- predicate on user_id/account_id first:
--
--   * GET /transactions and its COUNT(*) — WHERE user_id = $1
--     ORDER BY date DESC LIMIT/OFFSET
--   * Import/validate duplicate snapshots and billing-cycle aggregates —
--     WHERE account_id = $1 AND user_id = $2
--   * the per-row is_linked EXISTS in the transaction list —
--     SELECT 1 FROM links WHERE from_txn_id = t.id OR to_txn_id = t.id
--     (two single-column indexes let Postgres combine them with a BitmapOr)
--   * FK-ownership EXISTS checks and LEFT JOINs on category_id/payee_id
--   * rules/payees/categories listing — WHERE user_id = $1
--   * GetLinks — WHERE user_id = $1 ORDER BY created_at DESC
--   * transfer/cashback duplicate suggestions and billing-cycle generation
--   * loan_attachments joins and lookups

CREATE INDEX transactions_user_date_idx
    ON transactions (user_id, date DESC);

CREATE INDEX transactions_account_user_idx
    ON transactions (account_id, user_id);

CREATE INDEX transactions_account_user_date_idx
    ON transactions (account_id, user_id, date);

CREATE INDEX transactions_user_type_amount_date_idx
    ON transactions (user_id, type, amount, date);

CREATE INDEX transactions_account_user_type_date_idx
    ON transactions (account_id, user_id, type, date);

CREATE INDEX transactions_unassigned_account_idx
    ON transactions (account_id, user_id) WHERE billing_cycle_id IS NULL;

CREATE INDEX transactions_category_idx
    ON transactions (category_id) WHERE category_id IS NOT NULL;

CREATE INDEX transactions_payee_idx
    ON transactions (payee_id) WHERE payee_id IS NOT NULL;

CREATE INDEX transactions_billing_cycle_idx
    ON transactions (billing_cycle_id) WHERE billing_cycle_id IS NOT NULL;

CREATE INDEX billing_cycles_account_user_idx
    ON billing_cycles (account_id, user_id);

CREATE INDEX links_from_txn_idx
    ON links (from_txn_id);

CREATE INDEX links_to_txn_idx
    ON links (to_txn_id);

CREATE INDEX links_user_created_idx
    ON links (user_id, created_at DESC);

CREATE INDEX rules_user_idx
    ON rules (user_id);

CREATE INDEX rules_category_idx
    ON rules (category_id);

CREATE INDEX rules_payee_idx
    ON rules (payee_id) WHERE payee_id IS NOT NULL;

CREATE INDEX payees_user_idx
    ON payees (user_id);

CREATE INDEX categories_user_idx
    ON categories (user_id) WHERE user_id IS NOT NULL;

CREATE INDEX loan_attachments_loan_idx
    ON loan_attachments (loan_account_id, user_id);

CREATE INDEX loan_attachments_txn_idx
    ON loan_attachments (transaction_id);
