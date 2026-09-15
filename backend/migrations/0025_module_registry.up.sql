-- 0025_module_registry: Kernel 1 (Parity Build Order Phase 1) module registry state.
-- Per-entity enable/disable flags for every module; the SPA reads them via
-- ListModules (GET /api/v1/modules) to hide disabled modules.
-- Rights themselves live in ferp_rights (see 0002_identity) — this table only
-- tracks activation state, never the rights catalogue.

CREATE TABLE ferp_modules (
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    name        TEXT NOT NULL,                        -- module.Name(), e.g. 'sales'
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    version     TEXT NOT NULL DEFAULT '',             -- last activated version string
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (entity_id, name)
);

-- Tenant boundary, same fail-closed convention as 0024_rls_policies (not yet
-- enforced: ENABLE/FORCE ROW LEVEL SECURITY lands with the Phase 1 follow-up
-- migration, which must cover ferp_modules alongside the 0024 tables).
CREATE POLICY tenant_isolation ON ferp_modules
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
