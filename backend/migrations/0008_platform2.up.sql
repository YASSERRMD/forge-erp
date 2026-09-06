-- 0008_platform2: documents metadata, notification outbox, scheduler ledger, webhooks.
-- Dolibarr equivalents: llx_ecm_files (+documents/ dir), llx_notify/notify_def,
-- llx_cronjob, webhook module targets.

CREATE TABLE ferp_documents (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    scope       TEXT NOT NULL,                            -- sales|purchase|partners|...
    object_id   BIGINT NOT NULL DEFAULT 0,
    name        TEXT NOT NULL,
    mime        TEXT NOT NULL DEFAULT 'application/octet-stream',
    size        BIGINT NOT NULL DEFAULT 0,
    sha256      TEXT NOT NULL DEFAULT '',
    storage_key TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  BIGINT,
    CHECK (size >= 0)
);
CREATE INDEX ferp_docs_scope_idx ON ferp_documents (entity_id, scope, object_id);

CREATE TABLE ferp_notify_templates (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    code        TEXT NOT NULL,                            -- e.g. 'invoice.paid'
    channel     TEXT NOT NULL DEFAULT 'email',            -- email|webhook
    subject     TEXT NOT NULL DEFAULT '',
    body        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entity_id, code, channel)
);

CREATE TABLE ferp_notify_outbox (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    channel     TEXT NOT NULL,
    recipient   TEXT NOT NULL,
    subject     TEXT NOT NULL DEFAULT '',
    body        TEXT NOT NULL DEFAULT '',
    attempts    INTEGER NOT NULL DEFAULT 0,
    next_try_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at     TIMESTAMPTZ,
    error       TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ferp_notify_pending_idx ON ferp_notify_outbox (next_try_at) WHERE sent_at IS NULL;

CREATE TABLE ferp_jobs (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    code        TEXT NOT NULL,                            -- e.g. 'billing.recurring'
    interval_s  BIGINT NOT NULL,                          -- fixed-interval schedule
    next_run_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_status TEXT NOT NULL DEFAULT '',
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    UNIQUE (entity_id, code)
);

CREATE TABLE ferp_job_runs (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    job_id      BIGINT NOT NULL REFERENCES ferp_jobs(id) ON DELETE CASCADE,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    status      TEXT NOT NULL DEFAULT 'running',
    detail      TEXT NOT NULL DEFAULT ''
);

CREATE TABLE ferp_webhooks (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    subject     TEXT NOT NULL,                            -- event subject prefix to match
    url         TEXT NOT NULL,
    secret      TEXT NOT NULL DEFAULT '',
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
