ALTER TABLE users
    DROP COLUMN IF EXISTS paperless_url,
    DROP COLUMN IF EXISTS paperless_token;