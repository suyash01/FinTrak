-- Restore the original global name uniqueness.

DROP INDEX payees_user_name_uq;

ALTER TABLE payees ADD CONSTRAINT payees_name_key UNIQUE (name);