-- pending_assets: asset depreciation schedules (Phase 2).
-- Centrally renumbered on merge (pending_ prefix keeps it out of the
-- numbered history until then).
-- One schedule per asset; periodic postings advance posted_periods /
-- accumulated. Cost basis lives here (ferp_assets carries no money column).

CREATE TABLE ferp_asset_schedules (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id      BIGINT NOT NULL REFERENCES ferp_entities(id),
    asset_id       BIGINT NOT NULL REFERENCES ferp_assets(id) ON DELETE CASCADE,
    method         TEXT NOT NULL,                    -- linear|degressive
    cost           BIGINT NOT NULL CHECK (cost > 0),
    rate_bps       BIGINT NOT NULL DEFAULT 0,        -- per-period bps for degressive (1..10000); ignored for linear
    start_date     TIMESTAMPTZ NOT NULL,
    periods        INTEGER NOT NULL CHECK (periods > 0),
    posted_periods INTEGER NOT NULL DEFAULT 0 CHECK (posted_periods >= 0),
    accumulated    BIGINT NOT NULL DEFAULT 0 CHECK (accumulated >= 0),
    status         SMALLINT NOT NULL DEFAULT 0,      -- 0 active, 1 fully posted
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version    BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, asset_id),
    CHECK (posted_periods <= periods),
    CHECK (accumulated <= cost),
    CHECK (method IN ('linear', 'degressive'))
);
CREATE INDEX ferp_asset_schedules_asset_idx ON ferp_asset_schedules (asset_id);

CREATE POLICY tenant_isolation ON ferp_asset_schedules
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
