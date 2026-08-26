-- Category groups: replace categories.type (income/expense/transfer) with a
-- first-class, user-manageable grouping concept.
--
-- Four immutable base groups are seeded globally (income, expense, transfer,
-- cashback). Users may add their own custom groups. Categories now belong to a
-- group (group_id) instead of carrying a type string. Category user_id becomes
-- nullable so a global category can be added by an admin in the future (a NULL
-- user_id marks a global row).

-- 1. Category groups table.
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

-- 2. Seed the four immutable base groups (global, user_id NULL).
INSERT INTO category_groups (id, name, icon, color, is_base, user_id, sort_order) VALUES
    ('income',   'Income',   'wallet',            '#22c55e', TRUE, NULL, 1),
    ('expense',  'Expense',  'shopping-bag',      '#f97316', TRUE, NULL, 2),
    ('transfer', 'Transfer', 'arrow-left-right',  '#94a3b8', TRUE, NULL, 3),
    ('cashback', 'Cashback', 'badge-indian-rupee','#eab308', TRUE, NULL, 4)
ON CONFLICT (id) WHERE user_id IS NULL DO NOTHING;

-- 3. Add group_id to categories and backfill from the legacy type column.
ALTER TABLE categories ADD COLUMN group_id VARCHAR(50) REFERENCES category_groups(id);

UPDATE categories SET group_id = CASE
    WHEN LOWER(name) = 'cashback' THEN 'cashback'
    WHEN type IS NULL THEN 'expense'
    ELSE type
END
WHERE group_id IS NULL;

-- 4. Remove the now-virtual "Uncategorized" category: clear transactions that
-- referenced it and drop the rows (and any rules pointing at them).
DELETE FROM rules
WHERE category_id IN (SELECT id FROM categories WHERE LOWER(name) = 'uncategorized');

UPDATE transactions t SET category_id = NULL
WHERE t.category_id IN (SELECT id FROM categories WHERE LOWER(name) = 'uncategorized');

DELETE FROM categories WHERE LOWER(name) = 'uncategorized';

-- 5. Enforce group_id and drop the legacy type column.
ALTER TABLE categories ALTER COLUMN group_id SET NOT NULL;
ALTER TABLE categories DROP COLUMN type;

-- 6. Make categories.user_id nullable so a global (admin-created) category can
-- exist in the future. Add partial unique index for global rows; keep the
-- composite (id, user_id) unique for user rows.
ALTER TABLE categories ALTER COLUMN user_id DROP NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS categories_global_id_uq
    ON categories (id) WHERE user_id IS NULL;
