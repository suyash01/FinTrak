-- Roll back category groups: restore categories.type from group_id, restore
-- user_id NOT NULL, and drop the category_groups table.
ALTER TABLE categories ADD COLUMN type VARCHAR(20);

UPDATE categories SET type = g.id
FROM category_groups g
WHERE categories.group_id = g.id AND g.id IN ('income', 'expense', 'transfer');

-- Categories that lived in a custom group have no legacy type; treat them as
-- expense (closest to the old default) so the column stays valid.
UPDATE categories SET type = 'expense' WHERE type IS NULL;

ALTER TABLE categories ALTER COLUMN type SET NOT NULL;

DROP INDEX IF EXISTS categories_global_id_uq;
ALTER TABLE categories ALTER COLUMN user_id SET NOT NULL;

ALTER TABLE categories DROP COLUMN group_id;

DROP TABLE IF EXISTS category_groups;