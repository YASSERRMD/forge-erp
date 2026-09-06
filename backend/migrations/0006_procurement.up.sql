-- 0006_procurement: supplier documents, contract prices, supplier payments.
-- Dolibarr equivalents: llx_supplier_proposal(+det), llx_commande_fournisseur(+det),
-- llx_reception(+det), llx_facture_fourn(+det), llx_product_fournisseur_price,
-- llx_paiementfourn + llx_paiementfourn_facturefourn.

CREATE TABLE ferp_supplier_docs (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id       BIGINT NOT NULL REFERENCES ferp_entities(id),
    type            TEXT NOT NULL,                        -- supplier_proposal|supplier_order|reception|supplier_invoice
    ref             TEXT NOT NULL,
    status          SMALLINT NOT NULL DEFAULT 0,
    org_id          BIGINT NOT NULL REFERENCES ferp_organizations(id),
    currency        TEXT NOT NULL DEFAULT 'USD',
    rate_to_base    BIGINT NOT NULL DEFAULT 1000000,
    source_type     TEXT NOT NULL DEFAULT '',
    source_id       BIGINT NOT NULL DEFAULT 0,
    approved_by     BIGINT,
    total_net       BIGINT NOT NULL DEFAULT 0,
    total_vat       BIGINT NOT NULL DEFAULT 0,
    total_gross     BIGINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      BIGINT,
    row_version     BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, type, ref)
);
CREATE INDEX ferp_supdoc_org_idx ON ferp_supplier_docs (org_id, type, status);

CREATE TABLE ferp_supplier_doc_lines (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    doc_id          BIGINT NOT NULL REFERENCES ferp_supplier_docs(id) ON DELETE CASCADE,
    pos             INTEGER NOT NULL DEFAULT 0,
    product_id      BIGINT,
    label           TEXT NOT NULL DEFAULT '',
    qty             BIGINT NOT NULL,
    unit_net        BIGINT NOT NULL DEFAULT 0,
    vat_rate_bps    INTEGER NOT NULL DEFAULT 0,
    discount_pc     INTEGER NOT NULL DEFAULT 0,
    CHECK (qty > 0)
);

-- Contract prices (Dolibarr llx_product_fournisseur_price); latest row wins per triple.
CREATE TABLE ferp_supplier_prices (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    product_id  BIGINT NOT NULL REFERENCES ferp_products(id),
    org_id      BIGINT NOT NULL REFERENCES ferp_organizations(id),
    unit_net    BIGINT NOT NULL,
    currency    TEXT NOT NULL DEFAULT 'USD',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entity_id, product_id, org_id)
);

CREATE TABLE ferp_supplier_payments (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id       BIGINT NOT NULL REFERENCES ferp_entities(id),
    ref             TEXT NOT NULL,                        -- SPAY-YYYYMM-####
    org_id          BIGINT NOT NULL REFERENCES ferp_organizations(id),
    amount          BIGINT NOT NULL,
    currency        TEXT NOT NULL DEFAULT 'USD',
    method          TEXT NOT NULL DEFAULT 'transfer',
    paid_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      BIGINT,
    UNIQUE (entity_id, ref),
    CHECK (amount > 0)
);

CREATE TABLE ferp_supplier_allocations (
    payment_id  BIGINT NOT NULL REFERENCES ferp_supplier_payments(id) ON DELETE CASCADE,
    invoice_id  BIGINT NOT NULL REFERENCES ferp_supplier_docs(id) ON DELETE RESTRICT,
    amount      BIGINT NOT NULL,
    PRIMARY KEY (payment_id, invoice_id),
    CHECK (amount > 0)
);
