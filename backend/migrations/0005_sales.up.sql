-- 0005_sales: commercial documents, lines, payments, allocations.
-- Dolibarr equivalents: llx_propal/propaldet, llx_commande/commandedet,
-- llx_expedition/expeditiondet, llx_facture/facturedet, llx_paiement +
-- llx_paiement_facture, element_element (lineage folded into source columns).

-- One header table for all four families (discriminated by type).
CREATE TABLE ferp_documents (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id       BIGINT NOT NULL REFERENCES ferp_entities(id),
    type            TEXT NOT NULL,                        -- proposal|order|shipment|invoice
    ref             TEXT NOT NULL,                        -- e.g. PROP-202609-0007
    status          SMALLINT NOT NULL DEFAULT 0,
    org_id          BIGINT NOT NULL REFERENCES ferp_organizations(id),
    currency        TEXT NOT NULL DEFAULT 'USD',
    rate_to_base    BIGINT NOT NULL DEFAULT 1000000,     -- ×1e6 snapshot at doc date
    source_type     TEXT NOT NULL DEFAULT '',
    source_id       BIGINT NOT NULL DEFAULT 0,
    total_net       BIGINT NOT NULL DEFAULT 0,
    total_vat       BIGINT NOT NULL DEFAULT 0,
    total_gross     BIGINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      BIGINT,
    updated_by      BIGINT,
    row_version     BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, type, ref)
);
CREATE INDEX ferp_doc_org_idx ON ferp_documents (org_id, type, status);

CREATE TABLE ferp_doc_lines (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    doc_id          BIGINT NOT NULL REFERENCES ferp_documents(id) ON DELETE CASCADE,
    pos             INTEGER NOT NULL DEFAULT 0,
    product_id      BIGINT,
    label           TEXT NOT NULL DEFAULT '',
    qty             BIGINT NOT NULL,
    unit_net        BIGINT NOT NULL DEFAULT 0,
    vat_rate_bps    INTEGER NOT NULL DEFAULT 0,
    discount_pc     INTEGER NOT NULL DEFAULT 0,
    CHECK (qty > 0)
);

-- Per-type monthly counters back NextRef (Dolibarr numbering masks equivalent).
CREATE TABLE ferp_doc_counters (
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    type        TEXT NOT NULL,
    year_month  TEXT NOT NULL,                            -- YYYYMM
    next_seq    BIGINT NOT NULL DEFAULT 1,
    PRIMARY KEY (entity_id, type, year_month)
);

CREATE TABLE ferp_payments (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id       BIGINT NOT NULL REFERENCES ferp_entities(id),
    ref             TEXT NOT NULL,                        -- PAY-YYYYMM-####
    org_id          BIGINT NOT NULL REFERENCES ferp_organizations(id),
    amount          BIGINT NOT NULL,                      -- minor units
    currency        TEXT NOT NULL DEFAULT 'USD',
    method          TEXT NOT NULL DEFAULT 'transfer',     -- Dolibarr c_paiement equivalent
    paid_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      BIGINT,
    UNIQUE (entity_id, ref),
    CHECK (amount > 0)
);

CREATE TABLE ferp_payment_allocations (
    payment_id  BIGINT NOT NULL REFERENCES ferp_payments(id) ON DELETE CASCADE,
    invoice_id  BIGINT NOT NULL REFERENCES ferp_documents(id) ON DELETE RESTRICT,
    amount      BIGINT NOT NULL,
    PRIMARY KEY (payment_id, invoice_id),
    CHECK (amount > 0)
);
