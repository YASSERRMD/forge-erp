-- pending_binding: chart-of-accounts binding table (Phase 2 accounting binding).
-- Prerequisite for Phase 3 auto-posting: maps (kind, key) pairs to accounts.
-- Dolibarr equivalent: scattered hardcoded account selections per module
-- (product/customer/supplier/VAT/bank defaults); here one explicit table.
-- Centrally renumbered on merge (pending_ prefix keeps lexical order last).

CREATE TABLE ferp_account_bindings (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    kind        TEXT NOT NULL,  -- product|vat|bank|partner|expense
    key         TEXT NOT NULL,  -- e.g. product SKU, VAT rate bps, bank code
    account_id  BIGINT NOT NULL REFERENCES ferp_accounts(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entity_id, kind, key)
);
CREATE INDEX ferp_binding_lookup_idx ON ferp_account_bindings (entity_id, kind, key);
