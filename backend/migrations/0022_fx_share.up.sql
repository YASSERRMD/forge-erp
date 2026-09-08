-- 0022_fx_share: currency board rates + document share tokens.
-- Dolibarr equivalents: llx_multicurrency_rate, public shared links.

CREATE TABLE ferp_fx_rates (
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    code        TEXT NOT NULL,                  -- ISO-4217
    rate_to_base BIGINT NOT NULL CHECK (rate_to_base > 0),  -- ×1e6
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (entity_id, code)
);
INSERT INTO ferp_fx_rates (entity_id, code, rate_to_base) VALUES
    (1, 'USD', 1000000), (1, 'EUR', 1080000), (1, 'GBP', 1270000)
ON CONFLICT DO NOTHING;

CREATE TABLE ferp_share_tokens (
    token       TEXT PRIMARY KEY,               -- random hex
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    doc_id      BIGINT NOT NULL REFERENCES ferp_files(id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ferp_share_tokens_expiry_idx ON ferp_share_tokens (expires_at);
