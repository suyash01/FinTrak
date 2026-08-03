-- Users table for authentication
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Add user_id ownership to all data tables
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE categories ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE rules ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE payees ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE links ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_accounts_user_id ON accounts(user_id);
CREATE INDEX IF NOT EXISTS idx_transactions_user_id ON transactions(user_id);
CREATE INDEX IF NOT EXISTS idx_categories_user_id ON categories(user_id);
CREATE INDEX IF NOT EXISTS idx_rules_user_id ON rules(user_id);
CREATE INDEX IF NOT EXISTS idx_payees_user_id ON payees(user_id);
CREATE INDEX IF NOT EXISTS idx_links_user_id ON links(user_id);

-- Backfill existing data to the first registered user so pre-existing installs keep their data.
-- New installs have no rows here, so this is a no-op for them.
INSERT INTO users (email, password_hash)
SELECT 'legacy@localhost', '!'
WHERE EXISTS (SELECT 1 FROM accounts WHERE user_id IS NULL)
  AND NOT EXISTS (SELECT 1 FROM users);

UPDATE accounts SET user_id = (SELECT id FROM users LIMIT 1) WHERE user_id IS NULL;
UPDATE transactions SET user_id = (SELECT id FROM users LIMIT 1) WHERE user_id IS NULL;
UPDATE categories SET user_id = (SELECT id FROM users LIMIT 1) WHERE user_id IS NULL;
UPDATE rules SET user_id = (SELECT id FROM users LIMIT 1) WHERE user_id IS NULL;
UPDATE payees SET user_id = (SELECT id FROM users LIMIT 1) WHERE user_id IS NULL;
UPDATE links SET user_id = (SELECT id FROM users LIMIT 1) WHERE user_id IS NULL;
