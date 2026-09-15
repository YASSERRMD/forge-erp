-- pending_sepa: prelevement mandate lifecycle, pain.008 R-transactions,
-- PaymentByBankTransfer credit-transfer batches (Phase 2).
-- Dolibarr equivalents: prelevement (mandates rum/sequence, R-transactions
-- reject/return/refund), PaymentByBankTransfer (pain.001 generation).
-- Pending central renumbering; applies last (lexical order).
-- RLS: tenant policy matches the 0024 pattern (not yet enforced).

CREATE TABLE ferp_sepa_mandates (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id     BIGINT NOT NULL REFERENCES ferp_entities(id),
    umr           TEXT NOT NULL,                       -- unique mandate reference, per entity
    debtor_name   TEXT NOT NULL,
    debtor_iban   TEXT NOT NULL,
    debtor_bic    TEXT NOT NULL DEFAULT '',
    sequence      TEXT NOT NULL,                       -- FRST|RCUR|FNAL|OOFF|OFF
    status        SMALLINT NOT NULL DEFAULT 0,         -- 0 draft, 1 signed/active, -1 canceled
    signed_at     TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    amended_at    TIMESTAMPTZ,
    row_version   BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, umr)
);
CREATE INDEX ferp_sepa_mandates_entity_idx ON ferp_sepa_mandates (entity_id);
CREATE POLICY tenant_isolation ON ferp_sepa_mandates
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);

-- One R-handling row per collected item: REJECT (pre-settlement), RETURN
-- (post-settlement) or REFUND (debtor-initiated). The ledger reversal itself
-- posts through the finance entry chain (see sepa.Service); ledger_ref keeps
-- the audit link. UNIQUE scopes one R-handling per batch item.
CREATE TABLE ferp_sepa_rtransactions (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id     BIGINT NOT NULL REFERENCES ferp_entities(id),
    batch_id      BIGINT NOT NULL REFERENCES ferp_sepa_batches(id),
    end_to_end_id TEXT NOT NULL,
    kind          TEXT NOT NULL,                       -- REJECT|RETURN|REFUND
    reason        TEXT NOT NULL,                       -- SEPA R-reason code, e.g. AC01, MD01, AM09
    amount        BIGINT NOT NULL CHECK (amount > 0),  -- minor units, copied from the batch line
    ledger_ref    TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version   BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, batch_id, end_to_end_id)
);
CREATE INDEX ferp_sepa_rtransactions_batch_idx ON ferp_sepa_rtransactions (entity_id, batch_id);
CREATE POLICY tenant_isolation ON ferp_sepa_rtransactions
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);

-- Outbound credit-transfer batches (PaymentByBankTransfer): debtor pays
-- suppliers; lines render to pain.001 XML at export.
CREATE TABLE ferp_sepa_transfers (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id     BIGINT NOT NULL REFERENCES ferp_entities(id),
    ref           TEXT NOT NULL,
    debtor_name   TEXT NOT NULL,
    debtor_iban   TEXT NOT NULL,
    debtor_bic    TEXT NOT NULL,
    requested_at  TIMESTAMPTZ NOT NULL,
    lines         JSONB NOT NULL DEFAULT '[]',
    status        SMALLINT NOT NULL DEFAULT 0,         -- 0 draft, 1 validated, 2 sent, -1 canceled
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version   BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, ref)
);
CREATE INDEX ferp_sepa_transfers_entity_idx ON ferp_sepa_transfers (entity_id);
CREATE POLICY tenant_isolation ON ferp_sepa_transfers
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
