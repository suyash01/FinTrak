-- Reverts 000006_drop_regex_match_type: restores the legacy 'regex' value to
-- the match_type CHECK. Coerced rows are not restored (their original value is
-- not recoverable, and regex rules never fired anyway).
ALTER TABLE rules DROP CONSTRAINT IF EXISTS rules_match_type_check;
ALTER TABLE rules
    ADD CONSTRAINT rules_match_type_check
    CHECK (match_type IN ('contains', 'starts_with', 'regex', 'exact'));
