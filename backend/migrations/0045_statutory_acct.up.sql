-- 0045_statutory_acct: Phase 3 statutory accounting (chart packs, auto-posting
-- idempotency, close audit trail).
-- Dolibarr equivalents: llx_accounting_account_<cc> per-country packs feed
-- ferp_accounting_accounts; llx_accounting_bookkeeping idempotency moves to
-- ferp_account_postings; fiscal-year close/reopen audit lives in
-- ferp_close_log (Dolibarr has no reopen trail).

-- 1. Statutory chart of accounts: one row per (entity, pack, code).
-- code/label mirror Dolibarr account_number/label; parent is the longest
-- proper code prefix present in the same pack (NULL at class roots);
-- category folds Dolibarr pcg_type classes onto the ferp_accounts type set
-- so pack rows mirror 1:1 into ferp_accounts for posting/FEC.
CREATE TABLE ferp_accounting_accounts (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    pack        TEXT NOT NULL,                            -- FR|DE|US (Dolibarr fk_pcg_version family)
    code        TEXT NOT NULL,
    label       TEXT NOT NULL,
    parent      TEXT,
    category    TEXT NOT NULL,                            -- asset|liability|equity|revenue|expense
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entity_id, pack, code),
    CHECK (category IN ('asset','liability','equity','revenue','expense'))
);
CREATE INDEX ferp_acct_lookup_idx ON ferp_accounting_accounts (entity_id, pack, code);

-- 2. Auto-posting idempotency: one row per posted commercial document.
-- (entity, doc_type, doc_id) is the posting key: retrying validation of an
-- already-posted invoice returns the existing entry instead of double-posting.
CREATE TABLE ferp_account_postings (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    doc_type    TEXT NOT NULL,                            -- sales_invoice|supplier_invoice
    doc_id      BIGINT NOT NULL,
    entry_id    BIGINT NOT NULL REFERENCES ferp_entries(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entity_id, doc_type, doc_id)
);
CREATE INDEX ferp_posting_entry_idx ON ferp_account_postings (entry_id);

-- 3. Close audit trail: every lock/unlock of a fiscal year records WHO/when.
-- Reopening a locked year is allowed but always leaves a trail row.
CREATE TABLE ferp_close_log (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    year_id     BIGINT NOT NULL REFERENCES ferp_fiscal_years(id),
    action      TEXT NOT NULL,                            -- close|reopen
    entry_id    BIGINT REFERENCES ferp_entries(id),       -- close: the carry-forward entry
    actor       BIGINT,                                   -- user id when known
    note        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (action IN ('close','reopen'))
);
CREATE INDEX ferp_close_log_year_idx ON ferp_close_log (entity_id, year_id, id);

-- Tenant policies for the new entity-owned tables (not yet enforced;
-- enforcement follows with Phase 1 service completion, see 0024).
CREATE POLICY tenant_isolation ON ferp_accounting_accounts
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_account_postings
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_close_log
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
