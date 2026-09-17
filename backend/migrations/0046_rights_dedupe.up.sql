-- 0046_rights_dedupe: enforce idempotent rights seeding.
-- ferp_rights carries UNIQUE (entity_id, user_id, group_id, module, entity,
-- action), but the CHECK forces exactly one of user_id/group_id to NULL and
-- PostgreSQL treats NULLs as distinct — so SeedRights' ON CONFLICT DO NOTHING
-- never fired and re-activation duplicated every grant (20 -> 40). Dedupe
-- surviving rows, then enforce with partial unique indexes (one per grant
-- kind). The original constraint stays: harmless, still documents intent.

-- 1. Collapse exact duplicates (keep the lowest id per grant tuple).
DELETE FROM ferp_rights a USING ferp_rights b
WHERE a.id > b.id
  AND a.entity_id IS NOT DISTINCT FROM b.entity_id
  AND a.user_id IS NOT DISTINCT FROM b.user_id
  AND a.group_id IS NOT DISTINCT FROM b.group_id
  AND a.module IS NOT DISTINCT FROM b.module
  AND a.entity IS NOT DISTINCT FROM b.entity
  AND a.action IS NOT DISTINCT FROM b.action;

-- 2. Group grants: one row per (entity, group, module, entity, action).
CREATE UNIQUE INDEX IF NOT EXISTS ferp_rights_group_grant_uidx
    ON ferp_rights (entity_id, group_id, module, entity, action)
    WHERE user_id IS NULL;

-- 3. User grants: one row per (entity, user, module, entity, action).
CREATE UNIQUE INDEX IF NOT EXISTS ferp_rights_user_grant_uidx
    ON ferp_rights (entity_id, user_id, module, entity, action)
    WHERE group_id IS NULL;
