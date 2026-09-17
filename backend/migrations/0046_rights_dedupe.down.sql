-- 0046_rights_dedupe down: manual recovery only (see platform.Migrate).
DROP INDEX IF EXISTS ferp_rights_user_grant_uidx;
DROP INDEX IF EXISTS ferp_rights_group_grant_uidx;
-- The DELETE in the up migration is not reversible (duplicate rows are gone).
