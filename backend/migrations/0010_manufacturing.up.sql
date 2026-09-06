-- 0010_manufacturing: bills of materials + manufacturing orders lite.
-- Dolibarr equivalents: llx_bom, llx_bomline, llx_mrp_mo. Stock effects post
-- through the catalog movement ledger (consume/produce reasons).

CREATE TABLE ferp_boms (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    ref         TEXT NOT NULL,
    product_id  BIGINT NOT NULL REFERENCES ferp_products(id),
    label       TEXT NOT NULL,
    revision    INTEGER NOT NULL DEFAULT 1,
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 draft, 1 active, 2 obsolete
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, ref)
);
CREATE INDEX ferp_boms_product_idx ON ferp_boms (product_id, status);

CREATE TABLE ferp_bom_lines (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id    BIGINT NOT NULL REFERENCES ferp_entities(id),
    bom_id       BIGINT NOT NULL REFERENCES ferp_boms(id) ON DELETE CASCADE,
    component_id BIGINT NOT NULL REFERENCES ferp_products(id),
    qty          BIGINT NOT NULL CHECK (qty > 0),   -- per finished unit, integer units
    position     INTEGER NOT NULL DEFAULT 0,
    UNIQUE (bom_id, component_id),
    CHECK (component_id <> 0)
);

CREATE TABLE ferp_mos (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id    BIGINT NOT NULL REFERENCES ferp_entities(id),
    ref          TEXT NOT NULL,
    bom_id       BIGINT NOT NULL REFERENCES ferp_boms(id),
    product_id   BIGINT NOT NULL REFERENCES ferp_products(id),
    warehouse_id BIGINT NOT NULL REFERENCES ferp_warehouses(id),
    qty          BIGINT NOT NULL CHECK (qty > 0),
    status       SMALLINT NOT NULL DEFAULT 0,    -- 0 draft, 1 validated, 2 inprogress, 3 produced, -1 canceled
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version  BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, ref)
);
CREATE INDEX ferp_mos_bom_idx ON ferp_mos (bom_id, status);
