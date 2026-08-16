-- Add a configurable Paperless-ngx tag label that, when enabled during import,
-- is applied to successfully imported documents so they can be found in
-- Paperless-ngx later.
ALTER TABLE users ADD COLUMN IF NOT EXISTS paperless_tag VARCHAR(255) DEFAULT '';