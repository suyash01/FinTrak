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

-- 1. Disambiguate the extra account-linked rows of a duplicated name. Manual
--    duplicates keep their name so step 2 can fold them into the survivor.
WITH ranked AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY user_id, name
               ORDER BY (account_id IS NOT NULL) DESC, created_at, id
           ) AS rn
    FROM payees
)
UPDATE payees p
SET name = p.name || ' (' || left(p.id::text, 8) || ')',
    updated_at = NOW()
FROM ranked r
WHERE p.id = r.id
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
WHERE p.id = r.id AND r.rn > 1 AND p.account_id IS NULL;

-- 4. The index itself, under the name the handlers and their tests already
--    refer to.
CREATE UNIQUE INDEX IF NOT EXISTS payees_user_name_uq
    ON payees (user_id, name);
