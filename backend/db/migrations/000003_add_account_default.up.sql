-- Add a per-user default account flag. When set, the account is used to
-- pre-fill account filters across the app (except the import screen).
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS is_default BOOLEAN NOT NULL DEFAULT FALSE;