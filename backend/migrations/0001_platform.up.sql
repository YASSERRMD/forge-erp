-- 0001_platform: kernel tables for the ForgeERP modular monolith.
-- Dolibarr equivalent: htdocs/conf/conf.php + llx_const (scoped key/value config)
-- plus the implicit `entity` multi-company convention now made explicit.

-- Multi-company scope: every tenant row references ferp_entities (Dolibarr: `entity` int column).
CREATE TABLE ferp_entities (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code        TEXT NOT NULL UNIQUE,               -- e.g. 'main'
    label       TEXT NOT NULL,
    active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO ferp_entities (code, label) VALUES ('main', 'Main entity')
ON CONFLICT (code) DO NOTHING;

-- Scoped configuration overlay (env FERP_* wins; Dolibarr equivalent: llx_const).
CREATE TABLE ferp_config (
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    name        TEXT NOT NULL,
    value       TEXT NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by  BIGINT,
    PRIMARY KEY (entity_id, name)
);

-- Outbox for cross-module events (NATS JetStream publishing; Dolibarr equivalent: triggers fan-out).
CREATE TABLE ferp_outbox (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    subject     TEXT NOT NULL,                      -- forgeerp.<ctx>.<event>.v1
    payload     JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    claimed_at  TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ
);
CREATE INDEX ferp_outbox_pending_idx ON ferp_outbox (created_at) WHERE delivered_at IS NULL;
