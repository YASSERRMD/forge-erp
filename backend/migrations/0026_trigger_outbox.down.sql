-- 0026_trigger_outbox rollback: drop the catalogue routing columns, restore
-- the 0001 outbox shape.
DROP INDEX IF EXISTS ferp_outbox_relay_idx;
ALTER TABLE ferp_outbox DROP COLUMN IF EXISTS attempts;
ALTER TABLE ferp_outbox DROP COLUMN IF EXISTS object_id;
ALTER TABLE ferp_outbox DROP COLUMN IF EXISTS entity_id;
ALTER TABLE ferp_outbox DROP COLUMN IF EXISTS event_name;
