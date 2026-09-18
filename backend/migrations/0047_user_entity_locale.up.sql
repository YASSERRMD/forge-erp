-- 0047_user_entity_locale: stored locale preferences for the Phase 4
-- resolution chain (user preference → Accept-Language → entity default →
-- English). Empty string = unset at every level; resolution lives in
-- platform/locale (ResolveLocale), storage here. Dolibarr equivalent:
-- user->lang and entity multicompany language defaults.
ALTER TABLE ferp_users ADD COLUMN locale TEXT NOT NULL DEFAULT '';
ALTER TABLE ferp_entities ADD COLUMN default_locale TEXT NOT NULL DEFAULT '';
