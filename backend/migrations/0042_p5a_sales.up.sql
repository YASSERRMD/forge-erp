-- 0042_p5a_sales: Phase 5 PORT sales-adjacent batch.
-- Subtotals on sales documents, Incoterms 2020, stock transfers, dynamic prices.
-- Dolibarr equivalents: propal/commande/facture section + subtotal lines
-- (llx_*_det.special_code), llx_c_incoterms, llx_stock_mouvement paired
-- transfer postings, product price rules (llx_product_price + price expressions).

-- 1. Subtotal lines: ferp_doc_lines gains a kind discriminator. Existing rows
-- stay 'normal'. The legacy CHECK (qty > 0) is relaxed to qty >= 0 so section
-- and subtotal marker lines (qty 0, amounts ignored) persist.
ALTER TABLE ferp_doc_lines DROP CONSTRAINT IF EXISTS ferp_doc_lines_qty_check;
ALTER TABLE ferp_doc_lines ADD CHECK (qty >= 0);
ALTER TABLE ferp_doc_lines ADD COLUMN kind TEXT NOT NULL DEFAULT 'normal';
ALTER TABLE ferp_doc_lines ADD CONSTRAINT ferp_doc_lines_kind_check
    CHECK (kind IN ('normal', 'section', 'subtotal'));

-- 2. Incoterm on sales documents (procurement wiring is a follow-up; the
-- ferp_supplier_docs table is untouched here).
ALTER TABLE ferp_documents ADD COLUMN incoterm TEXT NOT NULL DEFAULT '';

-- 3. Incoterms 2020 code table (Dolibarr llx_c_incoterms equivalent).
CREATE TABLE ferp_incoterms (
    code    TEXT PRIMARY KEY,   -- EXW, FCA, ... (upper-case, Incoterms 2020)
    label   TEXT NOT NULL,
    mode    TEXT NOT NULL       -- 'any' (all transport) | 'sea' (sea/inland waterway)
);
INSERT INTO ferp_incoterms (code, label, mode) VALUES
    ('EXW', 'Ex Works', 'any'),
    ('FCA', 'Free Carrier', 'any'),
    ('CPT', 'Carriage Paid To', 'any'),
    ('CIP', 'Carriage and Insurance Paid To', 'any'),
    ('DAP', 'Delivered At Place', 'any'),
    ('DPU', 'Delivered at Place Unloaded', 'any'),
    ('DDP', 'Delivered Duty Paid', 'any'),
    ('FAS', 'Free Alongside Ship', 'sea'),
    ('FOB', 'Free On Board', 'sea'),
    ('CFR', 'Cost and Freight', 'sea'),
    ('CIF', 'Cost, Insurance and Freight', 'sea');

-- 4. Inter-warehouse transfers (header + lines). Refs count through the
-- shared ferp_doc_counters with type 'transfer' (TRF-YYYYMM-####).
CREATE TABLE ferp_stock_transfers (
    id                  BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id           BIGINT NOT NULL REFERENCES ferp_entities(id),
    ref                 TEXT NOT NULL,                        -- TRF-202609-0001
    source_warehouse_id BIGINT NOT NULL REFERENCES ferp_warehouses(id),
    dest_warehouse_id   BIGINT NOT NULL REFERENCES ferp_warehouses(id),
    status              SMALLINT NOT NULL DEFAULT 0,          -- 0 draft, 1 validated, 9 canceled
    note                TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by          BIGINT,
    updated_by          BIGINT,
    row_version         BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, ref),
    CHECK (source_warehouse_id <> dest_warehouse_id)
);
CREATE INDEX ferp_transfer_entity_idx ON ferp_stock_transfers (entity_id, status);

CREATE TABLE ferp_stock_transfer_lines (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    transfer_id BIGINT NOT NULL REFERENCES ferp_stock_transfers(id) ON DELETE CASCADE,
    pos         INTEGER NOT NULL DEFAULT 0,
    product_id  BIGINT NOT NULL REFERENCES ferp_products(id),
    qty         BIGINT NOT NULL,                              -- base units, > 0
    unit_cost   BIGINT NOT NULL DEFAULT 0,                    -- minor units (PMP snapshot at validation)
    CHECK (qty > 0),
    CHECK (unit_cost >= 0)
);

-- 5. Dynamic price rules + per product/org assignment.
CREATE TABLE ferp_price_rules (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    code        TEXT NOT NULL,                                -- unique per entity
    label       TEXT NOT NULL DEFAULT '',
    expression  TEXT NOT NULL,      -- e.g. "max(base * 0.9, cost)" over base|qty|cost
    status      SMALLINT NOT NULL DEFAULT 1,                  -- 1 active, 0 archived
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  BIGINT,
    updated_by  BIGINT,
    row_version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, code)
);

CREATE TABLE ferp_price_assignments (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    rule_id     BIGINT NOT NULL REFERENCES ferp_price_rules(id) ON DELETE CASCADE,
    product_id  BIGINT NOT NULL REFERENCES ferp_products(id) ON DELETE CASCADE,
    org_id      BIGINT NOT NULL DEFAULT 0,  -- 0 = all customers
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entity_id, product_id, org_id)
);
