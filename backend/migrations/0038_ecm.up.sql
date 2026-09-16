-- pending_ecm: Phase 2 ECM depth — folder tree, file versioning, filing rules,
-- full-text sidecar. (pending_ prefix: centrally renumbered on merge; the
-- 'pending_*' name sorts lexically after all 00NN migrations so it applies last.)
--
-- Dolibarr equivalents: llx_ecm_directories (folders), llx_ecm_files versions,
-- automatic filing (document_model / directory rules), indexed search.

-- 1. Folder tree: UNIQUE per (entity, parent, name), root folders use NULL parent.
CREATE TABLE ferp_folders (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    parent_id   BIGINT REFERENCES ferp_folders(id) ON DELETE CASCADE,
    name        TEXT NOT NULL CHECK (name <> '' AND name NOT LIKE '%/%'),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (entity_id, parent_id, name)
);
CREATE INDEX ferp_folders_parent_idx ON ferp_folders (entity_id, parent_id);

-- 2. File versioning: ferp_files keeps the CURRENT revision pointer;
-- ferp_file_versions is append-only history (never mutated, restores insert).
ALTER TABLE ferp_files
    ADD COLUMN folder_id BIGINT REFERENCES ferp_folders(id) ON DELETE SET NULL,
    ADD COLUMN version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1);
CREATE UNIQUE INDEX ferp_files_folder_name_uidx
    ON ferp_files (entity_id, folder_id, name) NULLS NOT DISTINCT;

CREATE TABLE ferp_file_versions (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    file_id     BIGINT NOT NULL REFERENCES ferp_files(id) ON DELETE CASCADE,
    version     INTEGER NOT NULL CHECK (version >= 1),
    storage_key TEXT NOT NULL,
    mime        TEXT NOT NULL DEFAULT 'application/octet-stream',
    size        BIGINT NOT NULL DEFAULT 0 CHECK (size >= 0),
    sha256      TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  BIGINT,
    UNIQUE (file_id, version)
);
CREATE INDEX ferp_file_versions_file_idx ON ferp_file_versions (file_id, version DESC);

-- 3. Automatic filing rules: (scope, object_type) -> folder path template.
-- Templates support {entity}, {entity_id}, {object_id}, {object_type}, {scope}.
CREATE TABLE ferp_filing_rules (
    scope         TEXT NOT NULL,
    object_type   TEXT NOT NULL DEFAULT '',
    path_template TEXT NOT NULL CHECK (path_template LIKE '/%'),
    PRIMARY KEY (scope, object_type)
);
INSERT INTO ferp_filing_rules (scope, object_type, path_template) VALUES
    ('sales', 'invoice', '/sales/invoices/{entity}');

-- 4. Full-text sidecar: content fed by text extraction (text/plain stored
-- directly + filename fallback). NO binary parsers: PDFs/ODTs index by name
-- until a parser lands. tsv is maintained by trigger on insert/update.
CREATE TABLE ferp_file_texts (
    file_id    BIGINT PRIMARY KEY REFERENCES ferp_files(id) ON DELETE CASCADE,
    content    TEXT NOT NULL DEFAULT '',
    tsv        TSVECTOR NOT NULL DEFAULT ''::tsvector,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ferp_file_texts_tsv_idx ON ferp_file_texts USING GIN (tsv);

CREATE OR REPLACE FUNCTION ferp_file_texts_tsv_trigger() RETURNS trigger AS $$
BEGIN
    NEW.tsv := to_tsvector('english', coalesce(NEW.content, ''));
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER ferp_file_texts_tsv_trg
    BEFORE INSERT OR UPDATE OF content ON ferp_file_texts
    FOR EACH ROW EXECUTE FUNCTION ferp_file_texts_tsv_trigger();

-- Tenant isolation policies follow the 0024 convention (policies defined here,
-- enforcement via ENABLE/FORCE RLS stays with the central RLS phase).
CREATE POLICY tenant_isolation ON ferp_folders
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
-- ferp_file_versions / ferp_file_texts carry no entity_id by design:
-- cascade-guarded via parent ferp_files (same pattern as 0024 child rows).
-- ferp_filing_rules is global configuration (no entity_id by design).
