-- Per-user Paperless-ngx integration settings.
-- Stored against the user (not docker env) so each account can configure its
-- own Paperless instance. Both are empty/NULL until the user fills them in on
-- the Settings page; the Paperless import UI is hidden until both are set.
ALTER TABLE users
    ADD COLUMN paperless_url VARCHAR(500) DEFAULT '',
    ADD COLUMN paperless_token TEXT DEFAULT '';