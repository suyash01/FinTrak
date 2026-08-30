-- Scope payee name uniqueness to the payee's owner.
--
-- The base schema declared payees.name UNIQUE column-wide, which put every
-- user's payees into a single shared namespace: user A creating a "Starbucks"
-- payee made it impossible for user B to create the same name, and
-- CreateAccount's account-linked payee upsert (ON CONFLICT (account_id)) let
-- an unhandled 23505 escape as a generic 500 whenever the account name
-- collided with ANY other user's payee. Names only need to be unique per
-- owner, so the column constraint becomes a (user_id, name) unique index.

ALTER TABLE payees DROP CONSTRAINT payees_name_key;

CREATE UNIQUE INDEX payees_user_name_uq ON payees (user_id, name);