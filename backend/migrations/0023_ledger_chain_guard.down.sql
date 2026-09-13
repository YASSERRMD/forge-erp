-- 0023_ledger_chain_guard down.
ALTER TABLE ferp_entries DROP CONSTRAINT IF EXISTS ferp_entries_entity_prev_unique;
