-- 0047_user_entity_locale down: manual recovery only (see platform.Migrate).
ALTER TABLE ferp_entities DROP COLUMN IF EXISTS default_locale;
ALTER TABLE ferp_users DROP COLUMN IF EXISTS locale;
