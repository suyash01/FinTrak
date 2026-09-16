-- FinTrak canonical schema (squashed).
-- Single baseline migration merging the full migration history:
--   * users, account types, accounts (incl. billing_day and closed),
--     billing cycles, category groups (replacing the legacy categories.type
--     column and the never-used categories.parent_id), categories, payees,
--     transactions, rules, links
--   * loan/EMI accounts: a closed flag and the loan_attachments junction that
--     attaches a transaction to exactly one loan account; a trigger rejects
--     writes that would place a transaction on a loan account
--   * per-owner payee name uniqueness ((user_id, name)) instead of the
--     column-wide UNIQUE on payees.name
--   * foreign keys with cascade/set-null semantics: transactions.account_id ->
--     accounts, links.from_txn_id/to_txn_id -> transactions, and the
--     category/payee references on transactions/rules/payees; links also
--     reject self-links (from_txn_id = to_txn_id) and duplicate identities
--   * a transaction's billing cycle must belong to the transaction's own
--     account (composite FK to billing_cycles (id, account_id))
--   * rules.match_type without the never-implemented 'regex' value
--   * transaction amounts stored as integer minor units (BIGINT cents)
--   * recurring/subscription tracking: recurring_series plus the
--     recurring_attachments junction and the recurring_series_terms
--     effective-dated amount/account range history
--   * the query and performance indexes added for the dominant
--     listing/aggregate/suggestion patterns
-- The historical orphan-cleanup, duplicate-collapse, and backfill statements
-- are no-ops on a fresh database and are intentionally not carried over.
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
-- The composite UNIQUE (id, account_id) backs the transactions composite FK
-- that keeps a transaction's cycle and account in agreement.
CREATE TABLE IF NOT EXISTS billing_cycles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    label VARCHAR(255) DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (account_id, start_date),
    UNIQUE (id, user_id),
    CONSTRAINT billing_cycles_id_account_uq UNIQUE (id, account_id)
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
-- The composite billing-cycle FK targets billing_cycles (id, account_id): the
-- default MATCH SIMPLE semantics leave rows with a NULL cycle untouched, while
-- a non-NULL cycle must belong to the transaction's own account.
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
    UNIQUE (id, user_id),
    CONSTRAINT transactions_billing_cycle_account_fk
        FOREIGN KEY (billing_cycle_id, account_id)
        REFERENCES billing_cycles (id, account_id)
        ON DELETE SET NULL (billing_cycle_id)
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
-- The identity of a link is (user_id, type, from_txn_id, to_txn_id); the unique
-- index below makes CreateLink atomic via ON CONFLICT DO NOTHING.
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

CREATE UNIQUE INDEX IF NOT EXISTS links_user_type_pair_uq
    ON links (user_id, type, from_txn_id, to_txn_id);

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

-- Loan/EMI accounts hold no transactions of their own; EMI payments live on
-- another account and are attached via loan_attachments. This trigger backstops
-- every write path so the invariant cannot be bypassed by a handler omission.
CREATE OR REPLACE FUNCTION enforce_transaction_not_loan_account() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM accounts a
         WHERE a.id = NEW.account_id
           AND a.account_type_id = 'loan'
    ) THEN
        RAISE EXCEPTION 'transactions cannot belong to a loan account'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS transactions_reject_loan_account ON transactions;
CREATE TRIGGER transactions_reject_loan_account
    BEFORE INSERT OR UPDATE OF account_id ON transactions
    FOR EACH ROW EXECUTE FUNCTION enforce_transaction_not_loan_account();

-- Recurring series & subscription tracking.
--
-- A recurring_series row is a user-defined *expectation*: a repeating charge or
-- income the user wants to track (rent, salary, a subscription). It is a
-- template only -- FinTrak never auto-creates transactions from it and never
-- auto-links transactions to it. Instead the backend forecasts its schedule and
-- suggests matching transactions; the user confirms each link explicitly via
-- recurring_attachments.
CREATE TABLE IF NOT EXISTS recurring_series (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The account the series is expected to post on. Deleting the account
    -- removes the series (its forecast/matches no longer make sense).
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    description TEXT DEFAULT '',
    -- Expected amount in integer minor units (cents), like transactions.amount.
    amount BIGINT NOT NULL,
    type VARCHAR(10) NOT NULL CHECK (type IN ('debit', 'credit')),
    frequency VARCHAR(10) NOT NULL CHECK (frequency IN ('daily', 'weekly', 'monthly', 'yearly')),
    -- Every `interval` days/weeks/months/years.
    interval INTEGER NOT NULL DEFAULT 1 CHECK (interval >= 1),
    start_date DATE NOT NULL,
    -- Optional final occurrence date; NULL means the series never ends.
    end_date DATE,
    category_id UUID REFERENCES categories(id) ON DELETE SET NULL,
    payee_id UUID REFERENCES payees(id) ON DELETE SET NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    notes TEXT DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (id, user_id)
);

-- Recurring attachments. The junction links a real transaction to the recurring
-- series it satisfies. The UNIQUE on transaction_id enforces "one transaction
-- belongs to at most one recurring series" at the database level (mirroring
-- loan_attachments).
CREATE TABLE IF NOT EXISTS recurring_attachments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    series_id UUID NOT NULL REFERENCES recurring_series(id) ON DELETE CASCADE,
    transaction_id UUID NOT NULL UNIQUE REFERENCES transactions(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (id, user_id)
);

-- Recurring series terms (piecewise amount/account history).
--
-- A subscription's amount and account can change over time (a price rise, a
-- card switch). recurring_series keeps the *current* amount/account for cheap
-- reads, and this table records the effective-dated history as explicit ranges:
-- a term applies over [start_date, end_date), and a NULL end_date is
-- open-ended. Handlers reject overlapping ranges; gaps are allowed (a
-- transaction that falls in a gap matches no term). recurring_series.amount/
-- account_id mirror the term with the greatest start_date (the invariant
-- handlers maintain).
CREATE TABLE IF NOT EXISTS recurring_series_terms (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    series_id UUID NOT NULL REFERENCES recurring_series(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    start_date DATE NOT NULL,
    end_date DATE,
    -- Expected amount in integer minor units (cents), like transactions.amount.
    amount BIGINT NOT NULL,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (id, user_id),
    -- One term per start date per series.
    UNIQUE (series_id, start_date)
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

-- Recurring query indexes:
--   * GET /recurring -- WHERE user_id = $1
--   * forecast/matching by account -- WHERE account_id = $1 AND user_id = $2
--   * attachment/term counts and deletes -- WHERE series_id = $1
--   * "is this transaction already linked" checks -- WHERE transaction_id = $1
CREATE INDEX recurring_series_user_idx
    ON recurring_series (user_id);

CREATE INDEX recurring_series_account_user_idx
    ON recurring_series (account_id, user_id);

CREATE INDEX recurring_attachments_series_idx
    ON recurring_attachments (series_id, user_id);

CREATE INDEX recurring_attachments_txn_idx
    ON recurring_attachments (transaction_id);

CREATE INDEX recurring_series_terms_series_idx
    ON recurring_series_terms (series_id, start_date);

CREATE INDEX recurring_series_terms_account_user_idx
    ON recurring_series_terms (account_id, user_id);
