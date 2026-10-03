-- FinTrak canonical baseline schema.
--
-- This file is the whole schema: it is applied to an empty database in one step
-- and there is nothing after it yet. Every later schema change is a new
-- NNNNNN_*.up.sql migration, as before.
--
--   * composite tenant keys: accounts and payees are keyed by (user_id, id),
--     and every FK that targets them carries the user_id so a row can never
--     reference another user's account/payee
--   * users, account types, accounts (including billing_day and closed),
--     billing cycles, category groups, categories, payees, transactions,
--     rules, and links
--   * server-recorded refresh sessions: refresh_tokens holds one row per issued
--     refresh token (SHA-256 only), family_id groups one session's rotation
--     chain, revoked_at marks a token spent, replaced_by points at its successor
--   * loan/EMI accounts: a closed flag and the loan_attachments junction that
--     attaches a transaction to exactly one loan account; a trigger rejects
--     writes that would place a transaction on a loan account
--   * the amortization side of a loan: loan_schedules records its terms,
--     loan_transfers one row per balance transfer/refinance, and
--     loan_disbursements the bank credit its disbursement is reconciled against
--   * foreign keys with cascade/set-null semantics: transactions.account_id ->
--     accounts, links.from_txn_id/to_txn_id -> transactions, and the
--     category/payee references on transactions/rules/payees; links also
--     reject self-links (from_txn_id = to_txn_id) and duplicate identities
--   * a transaction's billing cycle must belong to the transaction's own
--     account: the composite FK to billing_cycles (id, account_id) is the only
--     cycle constraint, and MATCH SIMPLE leaves rows with a NULL cycle untouched
--   * rules.match_type without the never-implemented 'regex' value
--   * transaction amounts stored as integer minor units (BIGINT cents)
--   * per-owner payee-name uniqueness, so two payees in one ledger can never be
--     indistinguishable in the picker, in payee rules or in the account<->payee
--     link
--   * client-key idempotency on transaction creates, which is what makes an
--     offline create replayable rather than a second money row
--   * transactions.billing_cycle_detached, the per-row record that a cleared
--     cycle was cleared deliberately, so the read-side back-fill does not
--     re-derive it
--   * transactions.tags is NOT NULL: a NULL breaks `unnest(tags || ...)` and so
--     silently stores nothing on a bulk tag add
--   * the baseline recurring_series and recurring_series_terms tables, whose
--     effective-dated terms carry the piecewise amount/account history
--   * the query and performance indexes added for the dominant
--     listing/aggregate/suggestion patterns, including the list endpoint's own
--     (user_id, date DESC, credit-before-debit, id) ordering
--
-- This baseline was squashed from the incremental history that preceded the
-- first release. The repairs that history performed -- clearing cross-account
-- cycle references, backfilling transfer principal, merging duplicate payees,
-- nulling legacy NULL tags -- are no-ops on an empty database and are not
-- carried over; only the invariant each of them left behind is, and it is
-- stated in this file as a constraint.
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

-- Server-side refresh sessions: rotation, reuse detection and revocation.
--
-- Only the SHA-256 of the token is stored (the token itself is never
-- persisted), family_id groups one session's rotation chain, revoked_at marks a
-- token as spent and replaced_by points at its successor. Presenting a revoked
-- token is proof the value leaked -- the only legitimate holder was handed the
-- successor when the cookie was replaced -- and revokes the family.
CREATE TABLE IF NOT EXISTS refresh_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    family_id UUID NOT NULL,
    issued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    replaced_by UUID
);

-- Lookups are by hash (unique constraint above) and revocations are by family.
-- Expired and revoked rows are retained so reuse detection can distinguish a
-- spent token from an unknown one. The application does not currently run an
-- automatic cleanup job; any future pruning must preserve that retention
-- window and be operated as a reviewed maintenance task.
CREATE INDEX IF NOT EXISTS refresh_tokens_family_id_idx ON refresh_tokens (family_id);
CREATE INDEX IF NOT EXISTS refresh_tokens_user_id_idx ON refresh_tokens (user_id);

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
    user_id UUID REFERENCES users(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS categories_global_id_uq
    ON categories (id) WHERE user_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS categories_user_id_uq
    ON categories (id, user_id) WHERE user_id IS NOT NULL;

-- Accounts
-- billing_day configures the statement period for any account type when set.
-- closed marks an account as closed: transactions can no longer be added,
-- removed, or edited on it (linking remains possible).
CREATE TABLE IF NOT EXISTS accounts (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
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
    PRIMARY KEY (user_id, id)
);

-- Billing cycles (accounts with a configured billing day)
-- An explicit, persisted period (start_date..end_date) that transactions are
-- attached to via transactions.billing_cycle_id. Cycles are generated from the
-- account's configured billing day; the assignment can be changed manually.
-- UNIQUE (user_id, account_id, id) is what backs the composite FK from
-- transactions that keeps a transaction's cycle and account in agreement.
CREATE TABLE IF NOT EXISTS billing_cycles (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    label VARCHAR(255) DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (user_id, id),
    UNIQUE (user_id, account_id, id),
    UNIQUE (user_id, account_id, start_date),
    CONSTRAINT billing_cycles_account_tenant_fkey
        FOREIGN KEY (user_id, account_id)
        REFERENCES accounts (user_id, id) ON DELETE CASCADE
);

-- Payees. account_id is NULL for manually-created payees and references the
-- linked account otherwise (an account-linked payee outlives its account).
CREATE TABLE IF NOT EXISTS payees (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    account_id UUID,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, id),
    CONSTRAINT payees_account_tenant_fkey
        FOREIGN KEY (user_id, account_id)
        REFERENCES accounts (user_id, id) ON DELETE SET NULL (account_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS payees_account_id_tenant_uq
    ON payees (user_id, account_id) WHERE account_id IS NOT NULL;

-- A payee name is unique per owner, not column-wide, so the same name can exist
-- in two ledgers. POST /payees and PUT /payees/:id turn a 23505 here into 409
-- "a payee with this name already exists"; the account handlers turn it into
-- "an account-linked payee with this name already exists".
CREATE UNIQUE INDEX IF NOT EXISTS payees_user_name_uq
    ON payees (user_id, name);

-- Transactions. amount is stored as integer minor units (cents) so
-- aggregation, balance, and transfer-scoring math use exact integer arithmetic.
-- category_id/payee_id references are set to NULL when the target is removed.
-- tags is NOT NULL: the write edges always bind an array, and a NULL would
-- break `unnest(tags || $n::text[])` so a bulk tag add would store nothing.
-- client_key is the client-generated idempotency key for creates -- nullable,
-- because a client need not send one, and indexed partially so the many NULL
-- rows do not collide. billing_cycle_detached records that the user cleared the
-- cycle on purpose, which is otherwise indistinguishable from never-assigned.
--
-- The composite billing-cycle FK targets billing_cycles (id, account_id): the
-- default MATCH SIMPLE semantics leave rows with a NULL cycle untouched, while
-- a non-NULL cycle must belong to the transaction's own account. Every handler
-- path re-checks the pairing, and listBillingCycles aggregates transactions
-- WHERE billing_cycle_id = bc.id without re-checking the account, so this is
-- the constraint that stops a row being counted into the wrong cycle's totals.
CREATE TABLE IF NOT EXISTS transactions (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL,
    date DATE NOT NULL,
    description TEXT NOT NULL,
    amount BIGINT NOT NULL,
    type VARCHAR(10) NOT NULL CHECK (type IN ('debit', 'credit')),
    category_id UUID REFERENCES categories(id) ON DELETE SET NULL,
    tags TEXT[] NOT NULL DEFAULT '{}',
    notes TEXT DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    payee_id UUID,
    billing_cycle_id UUID,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_key TEXT,
    billing_cycle_detached BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (user_id, id),
    CONSTRAINT transactions_account_tenant_fkey
        FOREIGN KEY (user_id, account_id)
        REFERENCES accounts (user_id, id) ON DELETE CASCADE,
    CONSTRAINT transactions_payee_tenant_fkey
        FOREIGN KEY (user_id, payee_id)
        REFERENCES payees (user_id, id) ON DELETE SET NULL (payee_id),
    CONSTRAINT transactions_billing_cycle_account_fkey
        FOREIGN KEY (user_id, account_id, billing_cycle_id)
        REFERENCES billing_cycles (user_id, account_id, id)
        ON DELETE SET NULL (billing_cycle_id)
);

CREATE INDEX IF NOT EXISTS transactions_tenant_account_id
    ON transactions (user_id, account_id);
CREATE INDEX IF NOT EXISTS transactions_tenant_category_id
    ON transactions (user_id, category_id) WHERE category_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS transactions_tenant_payee_id
    ON transactions (user_id, payee_id) WHERE payee_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS transactions_tenant_billing_cycle_id
    ON transactions (user_id, billing_cycle_id) WHERE billing_cycle_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS transactions_tenant_type
    ON transactions (user_id, type);

-- The transaction list's default ordering, which is txnOrderByDate
-- (handlers/transaction.go) exactly and in the same direction: `date DESC`,
-- then credits before debits within a day, then the id as the final tiebreak.
-- Postgres can therefore satisfy the ORDER BY from the index and stop after
-- LIMIT rows instead of sorting the user's whole prefix, and the count query
-- shares the `user_id = ...` prefix. DESC matches the endpoint's default; an
-- ascending list keeps sorting, which is not worth a second index on the
-- app's hottest table.
CREATE INDEX IF NOT EXISTS transactions_tenant_date
    ON transactions (user_id, date DESC, (CASE WHEN type = 'credit' THEN 0 ELSE 1 END), id);

-- A repeat of a client key returns the transaction it already created instead
-- of inserting a second row. The offline outbox depends on it: an entry
-- recorded while the API was unreachable is replayed on reconnect, and neither
-- a create whose response was lost nor a replayed one may post twice. Scoped by
-- user_id because the key is client-generated and only needs to be unique per
-- ledger.
CREATE UNIQUE INDEX IF NOT EXISTS transactions_user_client_key
    ON transactions (user_id, client_key)
    WHERE client_key IS NOT NULL;

-- Links. A link joins two distinct transactions; self-links are rejected.
-- The identity of a link is (user_id, type, from_txn_id, to_txn_id); the unique
-- constraint below makes CreateLink atomic via ON CONFLICT DO NOTHING.
CREATE TABLE IF NOT EXISTS links (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    type VARCHAR(20) NOT NULL CHECK (type IN ('transfer', 'cashback', 'refund', 'bill_payment')),
    from_txn_id UUID NOT NULL,
    to_txn_id UUID NOT NULL,
    notes TEXT DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, id),
    UNIQUE (user_id, type, from_txn_id, to_txn_id),
    CONSTRAINT links_no_self_check CHECK (from_txn_id <> to_txn_id),
    CONSTRAINT links_transaction_tenant_fkey
        FOREIGN KEY (user_id, from_txn_id)
        REFERENCES transactions (user_id, id) ON DELETE CASCADE,
    CONSTRAINT links_to_transaction_tenant_fkey
        FOREIGN KEY (user_id, to_txn_id)
        REFERENCES transactions (user_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS links_tenant_from_txn_id
    ON links (user_id, from_txn_id);
CREATE INDEX IF NOT EXISTS links_tenant_to_txn_id
    ON links (user_id, to_txn_id);

-- Loan/EMI attachments. The junction attaches a transaction (an EMI payment,
-- which lives on its own account) to exactly one loan account. The UNIQUE on
-- transaction_id enforces "one transaction -> one loan account" at the
-- database level.
CREATE TABLE IF NOT EXISTS loan_attachments (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    loan_account_id UUID NOT NULL,
    transaction_id UUID NOT NULL UNIQUE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (user_id, id),
    CONSTRAINT loan_attachments_account_tenant_fkey
        FOREIGN KEY (user_id, loan_account_id)
        REFERENCES accounts (user_id, id) ON DELETE CASCADE,
    CONSTRAINT loan_attachments_transaction_tenant_fkey
        FOREIGN KEY (user_id, transaction_id)
        REFERENCES transactions (user_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS loan_attachments_tenant_loan_account_id
    ON loan_attachments (user_id, loan_account_id);
CREATE INDEX IF NOT EXISTS loan_attachments_tenant_transaction_id
    ON loan_attachments (user_id, transaction_id);

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

-- Optional amortization schedule for a Loan / EMI account. A loan account holds
-- no transactions of its own and an attached EMI payment carries no principal/
-- interest split -- the account's "repaid" balance is just the sum of its
-- attachments. A schedule records the loan's terms so the amortization table can
-- be generated (each installment decomposed into principal and interest, the
-- final one absorbing rounding so the loan repays exactly) and matched against
-- the attached EMI payments in date order.
--
-- At most one schedule per loan account. Money is stored as integer minor units
-- (cents) and the rate as integer basis points (950 = 9.50% p.a.), so no money
-- arithmetic ever runs in float64.
--
-- processing_fee is what the lender charged, in minor units like every other
-- money column. It is reference data: the amortization table is generated over
-- the whole principal and the outstanding balance runs against that, so
-- recording the fee never moves a single installment. A lender that finances
-- its charges instead has them entered in the principal.
--
-- disbursal_date is when the money was actually released. The first installment
-- covers disbursal -> first due date; when that period is not a whole anchored
-- month (disbursed on the 20th, first EMI on the 5th) the handler charges
-- day-count interest for the broken period, so installment 1 splits differently
-- from every later one. NULL means the first period is exactly one month.
CREATE TABLE IF NOT EXISTS loan_schedules (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    loan_account_id UUID NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    principal BIGINT NOT NULL CHECK (principal > 0),
    annual_rate_bps INTEGER NOT NULL CHECK (annual_rate_bps >= 0),
    tenure_months INTEGER NOT NULL CHECK (tenure_months BETWEEN 1 AND 600),
    start_date DATE NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    processing_fee BIGINT NOT NULL DEFAULT 0,
    disbursal_date DATE,
    PRIMARY KEY (user_id, id),
    UNIQUE (user_id, loan_account_id),
    CONSTRAINT loan_schedules_account_tenant_fkey
        FOREIGN KEY (user_id, loan_account_id)
        REFERENCES accounts (user_id, id) ON DELETE CASCADE,
    CONSTRAINT loan_schedules_processing_fee_nonnegative
        CHECK (processing_fee >= 0)
);

-- One row per balance transfer / refinance. The source loan is settled at its
-- outstanding balance on transfer_date and the target loan absorbs that amount,
-- recasting its remaining installments. Both loans' tables are derived from
-- these rows, so deleting one reverts both sides rather than leaving
-- half-applied balances behind.
--
-- UNIQUE (user_id, from_loan_account_id) encodes "a loan can be transferred out
-- at most once" in the database: once settled it has no remaining balance to
-- move again, and the constraint turns a concurrent double-transfer into a
-- constraint violation the handler maps to a 409 instead of two recasts. It
-- also indexes the source-side lookup; the target side gets its own index.
--
-- mode names the three shapes a transfer can have on the receiving loan:
--   recast   -- the target's installments due after the transfer absorb the
--               amount, so it is an addition to the balance it still repays
--   opens    -- the transfer created the target's schedule; the amount is its
--               principal and must not be added on top of it a second time
--   takeover -- the target keeps amortizing its own principal and the amount is
--               paid out of the target's disbursement to settle the source, so
--               it reduces the cash the target released rather than increasing
--               what it owes
--
-- principal is what the settled transfer moved as principal, which is what the
-- source actually owed; the rest of amount is the interest time cost, and
-- keeping the two apart is the only way to tell a large payoff from a large
-- principal. 0 <= principal <= amount.
CREATE TABLE IF NOT EXISTS loan_transfers (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    from_loan_account_id UUID NOT NULL,
    to_loan_account_id UUID NOT NULL,
    amount BIGINT NOT NULL CHECK (amount > 0),
    transfer_date DATE NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    mode TEXT NOT NULL DEFAULT 'recast',
    principal BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, id),
    UNIQUE (user_id, from_loan_account_id),
    CONSTRAINT loan_transfers_distinct_accounts
        CHECK (from_loan_account_id <> to_loan_account_id),
    CONSTRAINT loan_transfers_mode_check
        CHECK (mode IN ('recast', 'opens', 'takeover')),
    CONSTRAINT loan_transfers_principal_check
        CHECK (principal >= 0 AND principal <= amount),
    CONSTRAINT loan_transfers_from_account_tenant_fkey
        FOREIGN KEY (user_id, from_loan_account_id)
        REFERENCES accounts (user_id, id) ON DELETE CASCADE,
    CONSTRAINT loan_transfers_to_account_tenant_fkey
        FOREIGN KEY (user_id, to_loan_account_id)
        REFERENCES accounts (user_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS loan_transfers_tenant_to_loan_account_id
    ON loan_transfers (user_id, to_loan_account_id);

-- The bank credit that released a loan, so the disbursement the schedule implies
-- (principal - processing fee - takeovers it funded) can be reconciled against
-- what actually landed in the borrower's account -- the one number a sanction
-- letter and a bank statement have to agree on.
--
-- One credit per loan and one loan per credit: a transaction that is already an
-- EMI payment, or another loan's disbursement, cannot be this one's.
CREATE TABLE IF NOT EXISTS loan_disbursements (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    loan_account_id UUID NOT NULL,
    transaction_id UUID NOT NULL UNIQUE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (user_id, id),
    UNIQUE (user_id, loan_account_id),
    CONSTRAINT loan_disbursements_account_tenant_fkey
        FOREIGN KEY (user_id, loan_account_id)
        REFERENCES accounts (user_id, id) ON DELETE CASCADE,
    CONSTRAINT loan_disbursements_transaction_tenant_fkey
        FOREIGN KEY (user_id, transaction_id)
        REFERENCES transactions (user_id, id) ON DELETE CASCADE
);

-- A recurring_series row is a user-defined *expectation*: a repeating charge or
-- income the user wants to track (rent, salary, a subscription). It is a
-- template only -- FinTrak never auto-creates transactions from it and never
-- auto-links transactions to it. Instead the backend forecasts its schedule and
-- suggests matching transactions; the user confirms each link explicitly via
-- recurring_attachments.
CREATE TABLE IF NOT EXISTS recurring_series (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(255) NOT NULL,
    description TEXT DEFAULT '',
    type VARCHAR(10) NOT NULL CHECK (type IN ('debit', 'credit')),
    frequency VARCHAR(10) NOT NULL CHECK (frequency IN ('daily', 'weekly', 'monthly', 'yearly')),
    interval INTEGER NOT NULL DEFAULT 1 CHECK (interval >= 1),
    category_id UUID REFERENCES categories(id) ON DELETE SET NULL,
    payee_id UUID,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    notes TEXT DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (user_id, id),
    CONSTRAINT recurring_series_payee_tenant_fkey
        FOREIGN KEY (user_id, payee_id)
        REFERENCES payees (user_id, id) ON DELETE SET NULL (payee_id)
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
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    series_id UUID NOT NULL,
    user_id UUID NOT NULL,
    start_date DATE NOT NULL,
    end_date DATE,
    amount BIGINT NOT NULL,
    account_id UUID NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (user_id, id),
    UNIQUE (user_id, series_id, start_date),
    CONSTRAINT recurring_series_terms_account_tenant_fkey
        FOREIGN KEY (user_id, account_id)
        REFERENCES accounts (user_id, id) ON DELETE CASCADE,
    CONSTRAINT recurring_series_terms_recurring_series_tenant_fkey
        FOREIGN KEY (user_id, series_id)
        REFERENCES recurring_series (user_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS recurring_series_terms_tenant_series_id
    ON recurring_series_terms (user_id, series_id);

-- Recurring attachments. The junction links a real transaction to the recurring
-- series it satisfies. The UNIQUE on transaction_id enforces "one transaction
-- belongs to at most one recurring series" at the database level (mirroring
-- loan_attachments).
CREATE TABLE IF NOT EXISTS recurring_attachments (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    series_id UUID NOT NULL,
    transaction_id UUID NOT NULL UNIQUE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (user_id, id),
    CONSTRAINT recurring_attachments_series_tenant_fkey
        FOREIGN KEY (user_id, series_id)
        REFERENCES recurring_series (user_id, id) ON DELETE CASCADE,
    CONSTRAINT recurring_attachments_transaction_tenant_fkey
        FOREIGN KEY (user_id, transaction_id)
        REFERENCES transactions (user_id, id) ON DELETE CASCADE
);

-- The series column leads the index after user_id, matching the predicates that
-- read this junction: the attached-transaction list and fetch filter
-- ra.user_id/ra.series_id, the series list runs a correlated
-- SELECT COUNT(*) ... WHERE ra.series_id = rs.id once per series row, and the
-- ON DELETE CASCADE from recurring_series has to locate its referencing rows.
CREATE INDEX IF NOT EXISTS recurring_attachments_tenant_series_id
    ON recurring_attachments (user_id, series_id);

-- Rules.
--
-- A rule matches a description and assigns a category (the action, NOT NULL,
-- which also keeps the "first matching rule wins" guard in ApplyRules
-- (category_id IS NULL) intact) and optionally a payee.
--
-- The optional conditions are all ANDed; every one is nullable, and for the two
-- booleans NULL means "either". The two actions beyond category/payee are
-- add_tags (tags to union onto the transaction) and notes (appended to any
-- existing notes).
--
-- 'regex' is intentionally not a valid match_type: no code path ever
-- implemented it.
--
-- The condition references are tenant-scoped except filter_category_id, which
-- may reference a global (admin) category and so is a plain FK mirroring
-- rules.category_id.
CREATE TABLE IF NOT EXISTS rules (
    id UUID NOT NULL DEFAULT gen_random_uuid(),
    pattern VARCHAR(500) NOT NULL,
    match_type VARCHAR(20) DEFAULT 'contains' CHECK (match_type IN ('contains', 'starts_with', 'exact')),
    category_id UUID NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    payee_id UUID,
    priority INT DEFAULT 0,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    account_id UUID,
    filter_category_id UUID,
    filter_payee_id UUID,
    min_amount BIGINT,
    max_amount BIGINT,
    txn_type VARCHAR(10) CHECK (txn_type IN ('debit', 'credit')),
    date_from DATE,
    date_to DATE,
    is_linked BOOLEAN,
    is_recurring BOOLEAN,
    add_tags TEXT[] DEFAULT '{}',
    notes TEXT DEFAULT '',
    PRIMARY KEY (user_id, id),
    CONSTRAINT rules_payee_tenant_fkey
        FOREIGN KEY (user_id, payee_id)
        REFERENCES payees (user_id, id) ON DELETE SET NULL (payee_id),
    CONSTRAINT rules_account_tenant_fkey
        FOREIGN KEY (user_id, account_id)
        REFERENCES accounts (user_id, id) ON DELETE SET NULL (account_id),
    CONSTRAINT rules_filter_category_fkey
        FOREIGN KEY (filter_category_id)
        REFERENCES categories (id) ON DELETE SET NULL,
    CONSTRAINT rules_filter_payee_tenant_fkey
        FOREIGN KEY (user_id, filter_payee_id)
        REFERENCES payees (user_id, id) ON DELETE SET NULL (filter_payee_id)
);