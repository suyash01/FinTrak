-- Re-add the per-account billing day (nullable; NULL means the default of the
-- 1st of each month).
ALTER TABLE accounts ADD COLUMN billing_day INT;