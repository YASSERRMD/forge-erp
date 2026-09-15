-- 0028_dictionaries: Kernel 7 reference dictionaries (Parity Build Order Phase 1).
-- Ports the llx_c_* Dolibarr dictionary tables into two generic tables.
--
-- Design (see backend/internal/platform/dict/doc.go):
--   dictionaries are GLOBAL shared data (no entity_id): one row set serves
--   every tenant. Per-locale display names ride in
--   ferp_dictionary_entries.locale_overrides; per-dictionary columns
--   (VAT rate bps, ISO codes, payment-term days, ...) ride in extra JSONB.
--   Writes are admin-only (enforced at the handler layer, not here).
--   Dictionaries carry no RLS policy by design (cf. 0024 header).

CREATE TABLE ferp_dictionaries (
    code       TEXT PRIMARY KEY,
    label      TEXT NOT NULL,
    scope      TEXT NOT NULL DEFAULT 'global',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE ferp_dictionary_entries (
    id               BIGSERIAL PRIMARY KEY,
    dictionary       TEXT NOT NULL REFERENCES ferp_dictionaries (code) ON DELETE CASCADE,
    code             TEXT NOT NULL,
    label            TEXT NOT NULL,
    sort             INTEGER NOT NULL DEFAULT 0,
    active           BOOLEAN NOT NULL DEFAULT TRUE,
    is_core          BOOLEAN NOT NULL DEFAULT FALSE,
    locale_overrides JSONB NOT NULL DEFAULT '{}',
    extra            JSONB NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ferp_dictionary_entries_dict_code_unique UNIQUE (dictionary, code),
    CONSTRAINT ferp_dictionary_entries_code_nonempty CHECK (code <> ''),
    CONSTRAINT ferp_dictionary_entries_label_nonempty CHECK (label <> '')
);

CREATE INDEX ferp_dictionary_entries_dict_active_idx
    ON ferp_dictionary_entries (dictionary, active);
