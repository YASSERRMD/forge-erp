-- 0023_ledger_chain_guard: backstop for the hash-chained ledger (Phase 0 task 5).
-- A valid chain uses each predecessor hash exactly once per entity, so a
-- fork (two entries sharing one prev_hash) fails at insert instead of
-- corrupting VerifyChain silently. The advisory xact lock in PostEntry is
-- the primary serializer; this constraint is the structural backstop.

ALTER TABLE ferp_entries
    ADD CONSTRAINT ferp_entries_entity_prev_unique UNIQUE (entity_id, prev_hash);
