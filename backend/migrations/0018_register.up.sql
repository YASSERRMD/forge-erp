-- 0018_register: product variants, physical assets, knowledge articles.
-- Dolibarr equivalents: variants tables, llx_asset (+workstation lite as
-- asset kind), llx_knowledgemanagement.

CREATE TABLE ferp_product_variants (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    product_id  BIGINT NOT NULL REFERENCES ferp_products(id) ON DELETE CASCADE,
    sku         TEXT NOT NULL,
    attributes  JSONB NOT NULL DEFAULT '{}',
    price_delta BIGINT NOT NULL DEFAULT 0,
    barcode     TEXT NOT NULL DEFAULT '',
    row_version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, sku)
);
CREATE INDEX ferp_product_variants_product_idx ON ferp_product_variants (product_id);

CREATE TABLE ferp_assets (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id    BIGINT NOT NULL REFERENCES ferp_entities(id),
    code         TEXT NOT NULL,
    label        TEXT NOT NULL,
    kind         TEXT NOT NULL DEFAULT 'equipment',  -- equipment|workstation|vehicle|it
    product_id   BIGINT REFERENCES ferp_products(id) ON DELETE SET NULL,
    serial       TEXT NOT NULL DEFAULT '',
    warehouse_id BIGINT REFERENCES ferp_warehouses(id) ON DELETE SET NULL,
    status       SMALLINT NOT NULL DEFAULT 1,    -- 1 in_service, 2 maintenance, 0 retired
    acquired_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version  BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, code)
);
CREATE INDEX ferp_assets_status_idx ON ferp_assets (entity_id, status);

CREATE TABLE ferp_articles (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    slug        TEXT NOT NULL,
    title       TEXT NOT NULL,
    body        TEXT NOT NULL DEFAULT '',
    tags        JSONB NOT NULL DEFAULT '[]',
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 draft, 1 published
    author      TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, slug)
);
CREATE INDEX ferp_articles_status_idx ON ferp_articles (entity_id, status);
