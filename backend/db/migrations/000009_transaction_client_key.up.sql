-- Idempotent creates: a client may send a key with POST /transactions, and a
-- repeat of that key returns the transaction it already created instead of
-- inserting a second row.
--
-- The offline outbox depends on it. An entry recorded while the API was
-- unreachable is replayed on reconnect, and a create whose response was lost (a
-- timeout, a closed tab, a killed process) is retried — neither may post twice.
--
-- Nullable because every existing client sends no key, and the index is partial
-- so the many NULL rows do not collide with one another. Scoped by user_id
-- because the key is client-generated and only needs to be unique per ledger.
ALTER TABLE transactions
    ADD COLUMN IF NOT EXISTS client_key TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS transactions_user_client_key
    ON transactions (user_id, client_key)
    WHERE client_key IS NOT NULL;
