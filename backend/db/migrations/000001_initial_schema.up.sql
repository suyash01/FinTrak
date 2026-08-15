-- FinTrak initial schema.
-- Squashed from the original migrations 000001-000005.
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- Users (authentication)
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    paperless_url VARCHAR(500) DEFAULT '',
    paperless_token TEXT DEFAULT '',
    page_size INTEGER,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Account types (reference data, global)
-- Credit card statements typically export purchases as negative amounts and
-- payments/refunds as positive, so the default convention makes a positive
-- amount on a credit card mean a credit and a negative amount mean a debit.
CREATE TABLE IF NOT EXISTS account_types (
    id VARCHAR(30) PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    positive_txn_type VARCHAR(6) NOT NULL CHECK (positive_txn_type IN ('credit', 'debit'))
);

-- Accounts
CREATE TABLE IF NOT EXISTS accounts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    account_type_id VARCHAR(30) NOT NULL REFERENCES account_types(id),
    bank VARCHAR(100),
    currency VARCHAR(3) DEFAULT 'INR',
    color VARCHAR(7) DEFAULT '#06b6d4',
    billing_day INT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    user_id UUID REFERENCES users(id) ON DELETE CASCADE
);

-- Categories
CREATE TABLE IF NOT EXISTS categories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL,
    icon VARCHAR(50),
    color VARCHAR(7),
    parent_id UUID REFERENCES categories(id) ON DELETE SET NULL,
    type VARCHAR(20) CHECK (type IN ('income', 'expense', 'transfer')),
    user_id UUID REFERENCES users(id) ON DELETE CASCADE
);

-- Payees
CREATE TABLE IF NOT EXISTS payees (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL UNIQUE,
    account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    user_id UUID REFERENCES users(id) ON DELETE CASCADE
);

-- Transactions
CREATE TABLE IF NOT EXISTS transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    date DATE NOT NULL,
    description TEXT NOT NULL,
    amount DECIMAL(15,2) NOT NULL,
    type VARCHAR(10) NOT NULL CHECK (type IN ('debit', 'credit')),
    category_id UUID REFERENCES categories(id) ON DELETE SET NULL,
    tags TEXT[] DEFAULT '{}',
    notes TEXT DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    payee_id UUID REFERENCES payees(id) ON DELETE SET NULL,
    user_id UUID REFERENCES users(id) ON DELETE CASCADE
);

-- Rules
CREATE TABLE IF NOT EXISTS rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pattern VARCHAR(500) NOT NULL,
    match_type VARCHAR(20) DEFAULT 'contains' CHECK (match_type IN ('contains', 'starts_with', 'regex', 'exact')),
    category_id UUID NOT NULL REFERENCES categories(id) ON DELETE CASCADE,
    payee_id UUID REFERENCES payees(id) ON DELETE SET NULL,
    priority INT DEFAULT 0,
    user_id UUID REFERENCES users(id) ON DELETE CASCADE
);

-- Links
CREATE TABLE IF NOT EXISTS links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type VARCHAR(20) NOT NULL CHECK (type IN ('transfer', 'cashback', 'refund')),
    from_txn_id UUID NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    to_txn_id UUID NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    notes TEXT DEFAULT '',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    user_id UUID REFERENCES users(id) ON DELETE CASCADE
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_transactions_account_id ON transactions(account_id);
CREATE INDEX IF NOT EXISTS idx_transactions_date ON transactions(date);
CREATE INDEX IF NOT EXISTS idx_transactions_category_id ON transactions(category_id);
CREATE INDEX IF NOT EXISTS idx_links_from_txn ON links(from_txn_id);
CREATE INDEX IF NOT EXISTS idx_links_to_txn ON links(to_txn_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_payees_account_id ON payees(account_id) WHERE account_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_accounts_user_id ON accounts(user_id);
CREATE INDEX IF NOT EXISTS idx_transactions_user_id ON transactions(user_id);
CREATE INDEX IF NOT EXISTS idx_categories_user_id ON categories(user_id);
CREATE INDEX IF NOT EXISTS idx_rules_user_id ON rules(user_id);
CREATE INDEX IF NOT EXISTS idx_payees_user_id ON payees(user_id);
CREATE INDEX IF NOT EXISTS idx_links_user_id ON links(user_id);

-- Seed default account types
INSERT INTO account_types (id, name, positive_txn_type) VALUES
    ('bank', 'Bank Account', 'credit'),
    ('credit_card', 'Credit Card', 'credit')
ON CONFLICT DO NOTHING;