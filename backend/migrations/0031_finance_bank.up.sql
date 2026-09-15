-- pending_finance_bank: Phase 2 financial depth (bank import, tax, VAT, closing).
-- Bank statement import dedupe key, VAT rate metadata + per-line rate tag,
-- tax declaration periods, social/fiscal charges.
-- pending_ prefix sorts last; centrally renumbered later.

-- 1. Bank statement import: natural key for idempotent imports.
ALTER TABLE ferp_bank_transactions ADD COLUMN bank_ref TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX ferp_bank_tx_dedupe_idx
    ON ferp_bank_transactions (entity_id, account_id, bank_ref) WHERE bank_ref <> '';
CREATE INDEX ferp_bank_tx_ref_idx
    ON ferp_bank_transactions (entity_id, bank_ref) WHERE bank_ref <> '';

-- 2. VAT rate metadata (minimal): code + rate, optionally bound to the
-- collected-VAT balance-sheet account. Entry lines carry the rate tag so the
-- VAT return aggregates posted lines joined to this table.
CREATE TABLE ferp_vat_rates (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id       BIGINT NOT NULL REFERENCES ferp_entities(id),
    code            TEXT NOT NULL,                        -- e.g. "TVA20"
    label           TEXT NOT NULL DEFAULT '',
    rate_bps        INTEGER NOT NULL,                     -- e.g. 2000 = 20%
    vat_account_id  BIGINT REFERENCES ferp_accounts(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entity_id, code),
    CHECK (rate_bps >= 0)
);

ALTER TABLE ferp_entry_lines ADD COLUMN vat_rate_bps INTEGER NOT NULL DEFAULT 0;

-- 3. Tax declaration periods (VAT returns, etc.): entity, label, start/end, status.
CREATE TABLE ferp_tax_periods (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    label       TEXT NOT NULL,
    start_date  TIMESTAMPTZ NOT NULL,
    end_date    TIMESTAMPTZ NOT NULL,
    status      TEXT NOT NULL DEFAULT 'open',             -- open|filed|paid
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (end_date >= start_date)
);

-- 4. Social/fiscal charge tracking with due dates.
CREATE TABLE ferp_tax_charges (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    label       TEXT NOT NULL,
    kind        TEXT NOT NULL DEFAULT 'social',           -- social|fiscal
    amount      BIGINT NOT NULL,                          -- minor units, > 0
    due_date    TIMESTAMPTZ NOT NULL,
    paid        BOOLEAN NOT NULL DEFAULT FALSE,
    paid_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (amount > 0)
);
CREATE INDEX ferp_tax_charges_due_idx
    ON ferp_tax_charges (entity_id, due_date) WHERE paid = FALSE;

-- Tenant policies for the new entity-owned tables (not yet enforced;
-- enforcement follows with Phase 1 service completion, see 0024).
CREATE POLICY tenant_isolation ON ferp_vat_rates
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_tax_periods
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_tax_charges
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
