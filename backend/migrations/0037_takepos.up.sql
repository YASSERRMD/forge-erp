-- pending_takepos: TakePOS depth (Parity Build Order Phase 2 Order 2).
-- Centrally renumbered on merge (pending_ prefix keeps lexical order last,
-- so this applies after all 00NN migrations including 0012_pos).
--
-- 1. ferp_pos_queue: offline till payloads. The till keeps selling without a
--    link; each payload carries an idempotency key unique per entity so a
--    retried sync or a double replay never rings the sale twice.
--    status: queued → sent | failed (failed keeps last_error + attempts).
-- 2. ferp_pos_payouts: cash paid OUT of the drawer mid-shift (petty cash,
--    supplier COD). Reduces the expected drawer cash.
-- 3. ferp_pos_counts: cash-count snapshots (counted vs expected → variance).
--    X reports read them; Z reports close the session (the close IS the
--    counter reset — expected is always recomputed from sales).
-- Money is int64 minor units everywhere, matching the existing pos tables.

CREATE TABLE ferp_pos_queue (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id       BIGINT NOT NULL REFERENCES ferp_entities(id),
    session_id      BIGINT NOT NULL REFERENCES ferp_pos_sessions(id),
    idempotency_key TEXT NOT NULL,
    payload         JSONB NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'sent', 'failed')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at         TIMESTAMPTZ,
    UNIQUE (entity_id, idempotency_key)
);
CREATE INDEX ferp_pos_queue_drain_idx ON ferp_pos_queue (entity_id, session_id, status, id);

CREATE TABLE ferp_pos_payouts (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    session_id  BIGINT NOT NULL REFERENCES ferp_pos_sessions(id),
    amount      BIGINT NOT NULL CHECK (amount > 0),
    reason      TEXT NOT NULL DEFAULT '',
    created_by  BIGINT REFERENCES ferp_users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ferp_pos_payouts_session_idx ON ferp_pos_payouts (session_id);

CREATE TABLE ferp_pos_counts (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id     BIGINT NOT NULL REFERENCES ferp_entities(id),
    session_id    BIGINT NOT NULL REFERENCES ferp_pos_sessions(id),
    counted_cash  BIGINT NOT NULL CHECK (counted_cash >= 0),
    expected_cash BIGINT NOT NULL,
    variance      BIGINT NOT NULL, -- counted_cash - expected_cash, may be negative
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ferp_pos_counts_session_idx ON ferp_pos_counts (session_id);

-- Tenant isolation follows the 0024 pattern (fail-closed on app.entity_id).
CREATE POLICY tenant_isolation ON ferp_pos_queue
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_pos_payouts
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_pos_counts
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
