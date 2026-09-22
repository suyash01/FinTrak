-- Only the constraint is reversible: the up migration merged duplicate payees
-- (re-pointing transactions/rules/recurring series at the survivor) and
-- disambiguated extra account-linked ones, and neither can be undone.
DROP INDEX IF EXISTS payees_user_name_uq;
