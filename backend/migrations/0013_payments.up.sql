-- 0013_payments: provider payment attempts with webhook idempotency.
-- Dolibarr equivalents: paypal/stripe transaction logs. Settlement posts
-- through sales/finance; attempts link by invoice_id/ref.

CREATE TABLE ferp_payment_attempts (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    ref         TEXT NOT NULL,
    org_id      BIGINT NOT NULL REFERENCES ferp_organizations(id),
    invoice_id  BIGINT REFERENCES ferp_documents(id) ON DELETE SET NULL,
    amount      BIGINT NOT NULL CHECK (amount > 0),
    currency    TEXT NOT NULL,
    provider    TEXT NOT NULL,                  -- manual|stripe|paypal
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 pending, 1 succeeded, -1 failed, -2 refunded
    webhook_key TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, ref)
);
CREATE UNIQUE INDEX ferp_payment_attempts_webhook_idx ON ferp_payment_attempts (entity_id, webhook_key)
    WHERE webhook_key <> '';
CREATE INDEX ferp_payment_attempts_invoice_idx ON ferp_payment_attempts (invoice_id);
