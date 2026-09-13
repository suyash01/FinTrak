-- Drops the legacy 'regex' match type.
--
-- rules.match_type still allowed 'regex', but no code path ever implemented it:
-- matchRule and ruleMatchSQL both no-op for it, so such rules were silently
-- skipped. Coerce any stray rows to 'contains' so the tightened constraint can
-- be applied, then replace the CHECK.
UPDATE rules SET match_type = 'contains' WHERE match_type = 'regex';

ALTER TABLE rules DROP CONSTRAINT IF EXISTS rules_match_type_check;
ALTER TABLE rules
    ADD CONSTRAINT rules_match_type_check
    CHECK (match_type IN ('contains', 'starts_with', 'exact'));
