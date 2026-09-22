-- Per-owner payee name uniqueness: the invariant 000001's header describes
-- ("per-owner payee name uniqueness ((user_id, name)) instead of the
-- column-wide UNIQUE on payees.name"), the payee handlers have always assumed,
-- and no migration ever created.
--
-- POST /payees and PUT /payees/:id turn a 23505 here into 409 "a payee with this
-- name already exists"; CreateAccount/UpdateAccount turn it into "an
-- account-linked payee with this name already exists". Without the index those
-- branches were unreachable and a user could accumulate payees that are
-- indistinguishable in the picker, in payee rules and in the account<->payee
-- link.
--
-- Existing databases can already hold such duplicates, so they are merged
-- first: CREATE UNIQUE INDEX would otherwise fail and take startup down with it.
-- The survivor of a name is the account-linked row when the group has one (it is
-- the payee every account view resolves), then the earliest row, then the lowest
-- id, so the choice is deterministic.
--
-- A name group can hold more than one account-linked row only when two accounts
-- share a name (accounts are not name-unique). Those cannot be merged away —
-- deleting one would strip its account of the linked payee that the account
-- handlers never re-create — so the extra rows are disambiguated with an id
-- suffix instead of being dropped.
--
-- Corrected in place rather than superseded by a later migration, unlike every
-- other schema change in this directory: a later file cannot prevent this one
-- from failing. golang-migrate runs the versions in order and stops at the
-- first error, and this migration is the one that appends the suffix, so a name
-- that already fills VARCHAR(255) makes step 1 raise 22001, leaves the schema
-- version unadvanced, and repeats the same failure on every restart — the
-- backend never boots and a self-hosted deployment has no rollback path (the
-- application never migrates down). Both defects below are therefore fixed in
-- the statement itself: the rename is bounded to the column width, and the
-- statements that act on `ranked` are scoped to the row's own tenant.

-- 1. Disambiguate the extra account-linked rows of a duplicated name. Manual
--    duplicates keep their name so step 2 can fold them into the survivor.
--
--    The suffix is 11 characters (" (" + 8 hex + ")"), so the name is truncated
--    to 244 first: `payees.name` is VARCHAR(255) and appending to a name that
--    already fills it would raise 22001 (value too long for type character
--    varying(255)) and take startup down with it. Truncation cannot make two
--    rows of one group collide — the suffix is derived from the id, which is
--    unique within the group's (user_id) partition.
WITH ranked AS (
    SELECT id, user_id,
           row_number() OVER (
               PARTITION BY user_id, name
               ORDER BY (account_id IS NOT NULL) DESC, created_at, id
           ) AS rn
    FROM payees
)
UPDATE payees p
SET name = left(p.name, 244) || ' (' || left(p.id::text, 8) || ')',
    updated_at = NOW()
FROM ranked r
WHERE p.id = r.id
  AND p.user_id = r.user_id
  AND r.rn > 1
  AND p.account_id IS NOT NULL;

-- 2. Point every reference at the survivor before the duplicate rows go:
--    transactions.payee_id, rules.payee_id, rules.filter_payee_id and
--    recurring_series.payee_id all reference payees (user_id, id) with
--    ON DELETE SET NULL, so deleting first would silently clear them.
WITH ranked AS (
    SELECT id, user_id, account_id,
           row_number() OVER (
               PARTITION BY user_id, name
               ORDER BY (account_id IS NOT NULL) DESC, created_at, id
           ) AS rn,
           first_value(id) OVER (
               PARTITION BY user_id, name
               ORDER BY (account_id IS NOT NULL) DESC, created_at, id
           ) AS survivor
    FROM payees
),
duplicates AS (
    SELECT id AS duplicate, user_id, survivor
    FROM ranked
    WHERE rn > 1 AND account_id IS NULL
)
UPDATE transactions t
SET payee_id = d.survivor
FROM duplicates d
WHERE t.payee_id = d.duplicate AND t.user_id = d.user_id;

WITH ranked AS (
    SELECT id, user_id, account_id,
           row_number() OVER (
               PARTITION BY user_id, name
               ORDER BY (account_id IS NOT NULL) DESC, created_at, id
           ) AS rn,
           first_value(id) OVER (
               PARTITION BY user_id, name
               ORDER BY (account_id IS NOT NULL) DESC, created_at, id
           ) AS survivor
    FROM payees
),
duplicates AS (
    SELECT id AS duplicate, user_id, survivor
    FROM ranked
    WHERE rn > 1 AND account_id IS NULL
)
UPDATE rules r
SET payee_id = d.survivor
FROM duplicates d
WHERE r.payee_id = d.duplicate AND r.user_id = d.user_id;

WITH ranked AS (
    SELECT id, user_id, account_id,
           row_number() OVER (
               PARTITION BY user_id, name
               ORDER BY (account_id IS NOT NULL) DESC, created_at, id
           ) AS rn,
           first_value(id) OVER (
               PARTITION BY user_id, name
               ORDER BY (account_id IS NOT NULL) DESC, created_at, id
           ) AS survivor
    FROM payees
),
duplicates AS (
    SELECT id AS duplicate, user_id, survivor
    FROM ranked
    WHERE rn > 1 AND account_id IS NULL
)
UPDATE rules r
SET filter_payee_id = d.survivor
FROM duplicates d
WHERE r.filter_payee_id = d.duplicate AND r.user_id = d.user_id;

WITH ranked AS (
    SELECT id, user_id, account_id,
           row_number() OVER (
               PARTITION BY user_id, name
               ORDER BY (account_id IS NOT NULL) DESC, created_at, id
           ) AS rn,
           first_value(id) OVER (
               PARTITION BY user_id, name
               ORDER BY (account_id IS NOT NULL) DESC, created_at, id
           ) AS survivor
    FROM payees
),
duplicates AS (
    SELECT id AS duplicate, user_id, survivor
    FROM ranked
    WHERE rn > 1 AND account_id IS NULL
)
UPDATE recurring_series s
SET payee_id = d.survivor
FROM duplicates d
WHERE s.payee_id = d.duplicate AND s.user_id = d.user_id;

-- 3. Drop the folded duplicates. Account-linked rows are never deleted here:
--    step 1 renamed them instead.
--
--    Scoped by user_id as well as id: the identity of a payee is (user_id, id),
--    so one UUID can legitimately name one payee per user, and joining on the
--    id alone would rename — and then delete — another tenant's payee. The
--    delete cascades SET NULL onto that user's transactions, rules and
--    recurring series, so the damage would be silent.
WITH ranked AS (
    SELECT id, user_id, account_id,
           row_number() OVER (
               PARTITION BY user_id, name
               ORDER BY (account_id IS NOT NULL) DESC, created_at, id
           ) AS rn
    FROM payees
)
DELETE FROM payees p
USING ranked r
WHERE p.id = r.id AND p.user_id = r.user_id AND r.rn > 1 AND p.account_id IS NULL;

-- 4. The index itself, under the name the handlers and their tests already
--    refer to.
CREATE UNIQUE INDEX IF NOT EXISTS payees_user_name_uq
    ON payees (user_id, name);
