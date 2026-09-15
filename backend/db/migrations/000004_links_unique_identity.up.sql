-- M-1: the intended link identity is (user_id, type, from_txn_id, to_txn_id).
-- CreateLink previously checked for a duplicate and then inserted, which was
-- race-prone: concurrent requests could both pass the check. This unique index
-- makes the insert atomic via ON CONFLICT DO NOTHING.
--
-- Duplicate rows that predate the constraint are collapsed to the oldest row
-- so the index can be created.
DELETE FROM links a
 USING links b
 WHERE a.ctid < b.ctid
   AND a.user_id = b.user_id
   AND a.type = b.type
   AND a.from_txn_id = b.from_txn_id
   AND a.to_txn_id = b.to_txn_id;

CREATE UNIQUE INDEX links_user_type_pair_uq
    ON links (user_id, type, from_txn_id, to_txn_id);
