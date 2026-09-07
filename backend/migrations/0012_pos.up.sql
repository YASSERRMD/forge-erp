-- 0012_pos: terminals, cashier sessions, till sales.
-- Dolibarr equivalent: takepos (till) posting into facture + paiement + stock.
-- Invoices/payments/moves live in their own tables; ferp_pos_sales links them.

CREATE TABLE ferp_pos_terminals (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id    BIGINT NOT NULL REFERENCES ferp_entities(id),
    code         TEXT NOT NULL,
    label        TEXT NOT NULL,
    warehouse_id BIGINT NOT NULL REFERENCES ferp_warehouses(id),
    status       SMALLINT NOT NULL DEFAULT 1,    -- 1 active, 0 inactive
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version  BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, code)
);

CREATE TABLE ferp_pos_sessions (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id      BIGINT NOT NULL REFERENCES ferp_entities(id),
    terminal_id    BIGINT NOT NULL REFERENCES ferp_pos_terminals(id),
    cashier        TEXT NOT NULL,
    opening_float  BIGINT NOT NULL DEFAULT 0,
    status         SMALLINT NOT NULL DEFAULT 0,  -- 0 open, 1 closed
    opened_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at      TIMESTAMPTZ,
    row_version    BIGINT NOT NULL DEFAULT 1
);
CREATE INDEX ferp_pos_sessions_terminal_idx ON ferp_pos_sessions (terminal_id, status);

CREATE TABLE ferp_pos_sales (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    session_id  BIGINT NOT NULL REFERENCES ferp_pos_sessions(id),
    ref         TEXT NOT NULL,
    org_id      BIGINT NOT NULL REFERENCES ferp_organizations(id),
    lines       JSONB NOT NULL DEFAULT '[]',
    total_gross BIGINT NOT NULL,
    method      TEXT NOT NULL,
    tendered    BIGINT NOT NULL,
    change      BIGINT NOT NULL,
    status      SMALLINT NOT NULL DEFAULT 1,    -- 1 completed, -1 voided
    invoice_id  BIGINT REFERENCES ferp_documents(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  BIGINT REFERENCES ferp_users(id),
    UNIQUE (entity_id, ref)
);
CREATE INDEX ferp_pos_sales_session_idx ON ferp_pos_sales (session_id, status);
