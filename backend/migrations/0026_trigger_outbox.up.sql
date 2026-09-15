-- 0026_trigger_outbox: extend ferp_outbox for the typed trigger catalogue
-- (Kernel 3). The 0001 shape (subject, payload, claimed_at, delivered_at)
-- stays untouched; this adds the catalogue routing columns so Emit can write
-- inside the caller's transaction and Relay can claim rows with
-- FOR UPDATE SKIP LOCKED:
--   event_name  catalogue name, e.g. EXPENSE_PAID (Dolibarr trigger baseline)
--   entity_id   tenant scope (the outbox stays a global queue by design —
--               see 0024 — with the tenant ALSO riding in the payload)
--   object_id   business row id (payload "id"), for exactly-once auditing
--   attempts    delivery-attempt counter for future retry observability
ALTER TABLE ferp_outbox ADD COLUMN IF NOT EXISTS event_name TEXT NOT NULL DEFAULT '';
ALTER TABLE ferp_outbox ADD COLUMN IF NOT EXISTS entity_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE ferp_outbox ADD COLUMN IF NOT EXISTS object_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE ferp_outbox ADD COLUMN IF NOT EXISTS attempts INT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS ferp_outbox_relay_idx ON ferp_outbox (id) WHERE delivered_at IS NULL;
