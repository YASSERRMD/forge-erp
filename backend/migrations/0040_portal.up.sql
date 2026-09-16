-- pending_portal: customer self-service portal credentials + cron expression
-- column (Parity Build Order Phase 2, Order 2).
-- Centrally renumbered on merge (pending_ prefix = unnumbered proposal).
--
-- ferp_portal_tokens: opaque bearer credentials bound to a partners contact's
-- org. Only SHA256(salt‖token) is persisted (salt per token, non-secret,
-- embedded in the "<salt>.<secret>" bearer format crypt/MCF-style so auth is
-- one indexed lookup + constant-time verify). Raw secrets never touch disk.
-- Distinct from documentsvc ferp_share_tokens (single-file public links, raw
-- token stored): portal tokens scope a whole customer (org) across invoices,
-- quotes and tickets, and are revocable + expirable.
-- ferp_jobs.cron_expr: 5-field standard cron (no seconds), "" = legacy
-- interval_s fallback. No Go owner existed for ferp_jobs/ferp_job_runs
-- (migration 0008_platform2 only); backend/internal/platform/cron owns it.

CREATE TABLE ferp_portal_tokens (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    org_id      BIGINT NOT NULL,
    contact_id  BIGINT,
    salt        TEXT NOT NULL,
    token_hash  TEXT NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (token_hash)
);
CREATE INDEX ferp_portal_tokens_org_idx ON ferp_portal_tokens (entity_id, org_id);

ALTER TABLE ferp_jobs ADD COLUMN IF NOT EXISTS cron_expr TEXT NOT NULL DEFAULT '';
