-- 0027_field_defs: custom-field definitions (Kernel 6).
-- Dolibarr equivalents: llx_extrafields (+ per-entity llx_*_extrafields EAV
-- tables). ForgeERP stores values in custom_fields JSONB per entity row
-- (intentional difference 3); this table holds the definitions those values
-- validate against, plus which definitions get expression indexes.
-- RLS: tenant policy defined here matches the 0024 pattern (policies are not
-- yet enforced; enforcement lands with the follow-up ENABLE/FORCE migration).

CREATE TABLE ferp_field_defs (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id       BIGINT NOT NULL REFERENCES ferp_entities(id),
    scope           TEXT NOT NULL,                            -- e.g. 'organization', 'product'
    key             TEXT NOT NULL,                            -- machine key within the scope
    type            TEXT NOT NULL,                            -- text|number|select|date|boolean
    label           TEXT NOT NULL DEFAULT '',
    required        BOOLEAN NOT NULL DEFAULT FALSE,
    options         JSONB NOT NULL DEFAULT '[]',             -- allowed values for select
    validation_rule TEXT NOT NULL DEFAULT '',                -- optional regexp over the string form
    display_order   INTEGER NOT NULL DEFAULT 0,
    searchable      BOOLEAN NOT NULL DEFAULT FALSE,          -- gets a custom_fields expression index
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entity_id, scope, key),
    CHECK (scope <> ''),
    CHECK (key <> ''),
    CHECK (type IN ('text', 'number', 'select', 'date', 'boolean'))
);
CREATE INDEX ferp_field_defs_entity_scope_idx ON ferp_field_defs (entity_id, scope);

CREATE POLICY tenant_isolation ON ferp_field_defs
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
