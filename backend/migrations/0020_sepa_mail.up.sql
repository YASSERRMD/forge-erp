-- 0020_sepa_mail: direct-debit batches, inbound mailboxes.
-- Dolibarr equivalents: prelevement (batches), emailcollector (mailboxes).

CREATE TABLE ferp_sepa_batches (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id     BIGINT NOT NULL REFERENCES ferp_entities(id),
    ref           TEXT NOT NULL,
    creditor_name TEXT NOT NULL,
    creditor_iban TEXT NOT NULL,
    creditor_bic  TEXT NOT NULL,
    creditor_id   TEXT NOT NULL,
    sequence      TEXT NOT NULL,                -- FRST|RCUR|OOFF|FNAL
    requested_at  TIMESTAMPTZ NOT NULL,
    transactions  JSONB NOT NULL DEFAULT '[]',
    status        SMALLINT NOT NULL DEFAULT 0,  -- 0 draft, 1 validated, 2 sent, -1 canceled
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version   BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, ref)
);

CREATE TABLE ferp_mailboxes (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    code        TEXT NOT NULL,
    host        TEXT NOT NULL,
    port        INTEGER NOT NULL DEFAULT 993,
    username    TEXT NOT NULL DEFAULT '',
    use_tls     BOOLEAN NOT NULL DEFAULT TRUE,
    active      BOOLEAN NOT NULL DEFAULT TRUE,
    last_fetch  TIMESTAMPTZ,
    last_error  TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, code)
);
