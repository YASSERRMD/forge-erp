-- 0004_catalog: products, warehouses, stock ledger, lots.
-- Dolibarr equivalents: llx_product (+llx_product_lang/extrafields), llx_entrepot,
-- llx_product_stock (derived into ferp_stock_levels), llx_stock_mouvement (append-only
-- ferp_stock_movements), llx_product_lot. Prices in minor units (BIGINT).

CREATE TABLE ferp_products (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id       BIGINT NOT NULL REFERENCES ferp_entities(id),
    sku             TEXT NOT NULL,                        -- llx_product.ref
    name            TEXT NOT NULL,
    type            SMALLINT NOT NULL DEFAULT 0,          -- 0 goods, 1 service
    unit            TEXT NOT NULL DEFAULT 'unit',
    net_price       BIGINT NOT NULL DEFAULT 0,            -- minor units, excl. VAT
    vat_rate_bps    INTEGER NOT NULL DEFAULT 0,           -- e.g. 2000 = 20%
    status          SMALLINT NOT NULL DEFAULT 1,          -- 1 active, 0 archived
    stock_tracked   BOOLEAN NOT NULL DEFAULT TRUE,
    custom_fields   JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      BIGINT,
    updated_by      BIGINT,
    row_version     BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, sku)
);
CREATE INDEX ferp_product_entity_name_idx ON ferp_products (entity_id, name);

CREATE TABLE ferp_warehouses (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    code        TEXT NOT NULL,
    label       TEXT NOT NULL,
    status      SMALLINT NOT NULL DEFAULT 1,              -- 1 open, 0 closed
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entity_id, code)
);

-- Append-only ledger (Dolibarr llx_stock_mouvement). Never updated or deleted.
CREATE TABLE ferp_stock_movements (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id       BIGINT NOT NULL REFERENCES ferp_entities(id),
    product_id      BIGINT NOT NULL REFERENCES ferp_products(id),
    warehouse_id    BIGINT NOT NULL REFERENCES ferp_warehouses(id),
    lot_id          BIGINT,
    qty             BIGINT NOT NULL,                      -- >0 in, <0 out
    unit_cost       BIGINT NOT NULL DEFAULT 0,            -- minor units (inbound)
    reason          TEXT NOT NULL,
    ref             TEXT NOT NULL DEFAULT '',             -- source document ref
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      BIGINT,
    CHECK (qty <> 0),
    CHECK (unit_cost >= 0)
);
CREATE INDEX ferp_movement_product_wh_idx ON ferp_stock_movements (product_id, warehouse_id, id);

-- Derived on-hand positions (Dolibarr llx_product_stock); maintained by the store
-- in the same transaction that appends the movement.
CREATE TABLE ferp_stock_levels (
    product_id      BIGINT NOT NULL REFERENCES ferp_products(id),
    warehouse_id    BIGINT NOT NULL REFERENCES ferp_warehouses(id),
    qty             BIGINT NOT NULL DEFAULT 0,
    total_value     BIGINT NOT NULL DEFAULT 0,            -- minor units at PMP
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (product_id, warehouse_id)
);

CREATE TABLE ferp_lots (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    product_id  BIGINT NOT NULL REFERENCES ferp_products(id),
    number      TEXT NOT NULL,                            -- llx_product_lot.batch
    expires_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (product_id, number)
);
ALTER TABLE ferp_stock_movements
    ADD CONSTRAINT ferp_movement_lot_fk FOREIGN KEY (lot_id) REFERENCES ferp_lots(id);
