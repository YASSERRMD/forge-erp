-- pending_oauth: outbound OAuth token vault (Parity Build Order Phase 2).
-- Centrally renumbered on merge (pending_ prefix = unnumbered proposal).
-- Distinct from inbound OIDC login (no schema; Keycloak verifies RS256):
-- this stores OUR tokens to THIRD PARTIES per (entity, provider, owner).
-- Only AES-256-GCM ciphertext (key: FERP_OAUTH_KEY env) + a log-safe
-- token_ref fingerprint are persisted; raw secrets never touch this table.

CREATE TABLE ferp_oauth_tokens (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id     BIGINT NOT NULL REFERENCES ferp_entities(id),
    provider      TEXT NOT NULL,                  -- stripe|paypal|...
    owner         TEXT NOT NULL,                  -- entity-scoped owner key (merchant/acct id)
    access_sealed BYTEA NOT NULL,                 -- nonce‖AES-256-GCM(access token)
    refresh_sealed BYTEA NOT NULL,                -- nonce‖AES-256-GCM(refresh token)
    token_ref     TEXT NOT NULL DEFAULT '',       -- log-safe fingerprint (provider:truncated-sha256)
    expires_at    TIMESTAMPTZ NOT NULL,           -- access-token expiry (refresh trigger)
    scopes        TEXT NOT NULL DEFAULT '',       -- comma-joined granted scopes (metadata)
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version   BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, provider, owner)
);
CREATE INDEX ferp_oauth_tokens_entity_idx ON ferp_oauth_tokens (entity_id);
