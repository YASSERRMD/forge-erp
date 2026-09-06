-- 0007_finance: chart of accounts, journals, fiscal years, chained entries,
-- bank accounts + transactions, loans.
-- Dolibarr equivalents: llx_accounting_account, llx_accounting_journal,
-- llx_accounting_fiscalyear, llx_accounting_bookkeeping (+blockedlog chain folded
-- into prev_hash/chain_hash), llx_bank_account, llx_bank, llx_loan + llx_loan_schedule.

CREATE TABLE ferp_accounts (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    code        TEXT NOT NULL,
    label       TEXT NOT NULL,
    type        TEXT NOT NULL,                            -- asset|liability|equity|revenue|expense
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entity_id, code)
);

CREATE TABLE ferp_journals (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    code        TEXT NOT NULL,                            -- VEN|ACH|BNK|...
    label       TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entity_id, code)
);

CREATE TABLE ferp_fiscal_years (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    label       TEXT NOT NULL,
    start_date  TIMESTAMPTZ NOT NULL,
    end_date    TIMESTAMPTZ NOT NULL,
    locked      BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE TABLE ferp_entries (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    journal_id  BIGINT NOT NULL REFERENCES ferp_journals(id),
    ref         TEXT NOT NULL,
    date        TIMESTAMPTZ NOT NULL,
    memo        TEXT NOT NULL DEFAULT '',
    status      SMALLINT NOT NULL DEFAULT 0,              -- 0 draft, 1 posted, 9 void
    prev_hash   TEXT NOT NULL,
    chain_hash  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  BIGINT,
    row_version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, journal_id, ref)
);

CREATE TABLE ferp_entry_lines (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entry_id    BIGINT NOT NULL REFERENCES ferp_entries(id) ON DELETE CASCADE,
    pos         INTEGER NOT NULL DEFAULT 0,
    account_id  BIGINT NOT NULL REFERENCES ferp_accounts(id),
    label       TEXT NOT NULL DEFAULT '',
    debit       BIGINT NOT NULL DEFAULT 0,
    credit      BIGINT NOT NULL DEFAULT 0,
    CHECK (debit >= 0 AND credit >= 0),
    CHECK ((debit = 0) <> (credit = 0))
);

CREATE TABLE ferp_bank_accounts (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    code        TEXT NOT NULL,
    label       TEXT NOT NULL,
    iban        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entity_id, code)
);

CREATE TABLE ferp_bank_transactions (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id       BIGINT NOT NULL REFERENCES ferp_entities(id),
    account_id      BIGINT NOT NULL REFERENCES ferp_bank_accounts(id),
    amount          BIGINT NOT NULL,
    label           TEXT NOT NULL DEFAULT '',
    value_date      TIMESTAMPTZ NOT NULL DEFAULT now(),
    reconciled      BOOLEAN NOT NULL DEFAULT FALSE,
    reconciled_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (amount <> 0)
);
CREATE INDEX ferp_bank_tx_acct_idx ON ferp_bank_transactions (account_id, value_date);

CREATE TABLE ferp_loans (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    label       TEXT NOT NULL,
    principal   BIGINT NOT NULL,
    rate_bps    INTEGER NOT NULL DEFAULT 0,
    start_date  TIMESTAMPTZ NOT NULL,
    periods     INTEGER NOT NULL,
    schedule    JSONB NOT NULL DEFAULT '[]',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (principal > 0 AND periods > 0)
);
