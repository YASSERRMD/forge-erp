-- 0003_partners: organizations, contacts, categories.
-- Dolibarr equivalents: llx_societe (+llx_societe_extrafields), llx_socpeople
-- (+llx_socpeople_extrafields), llx_categorie + categorie_societe. Extrafields
-- collapse into custom_fields JSONB (no EAV tables — intentional difference).

CREATE TABLE ferp_organizations (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id       BIGINT NOT NULL REFERENCES ferp_entities(id),
    name            TEXT NOT NULL,                        -- llx_societe.nom
    alias           TEXT NOT NULL DEFAULT '',             -- name_alias
    ref_ext         TEXT NOT NULL DEFAULT '',
    parent_id       BIGINT REFERENCES ferp_organizations(id),
    status          SMALLINT NOT NULL DEFAULT 1,          -- 1 active, 0 inactive
    is_customer     BOOLEAN NOT NULL DEFAULT FALSE,
    is_supplier     BOOLEAN NOT NULL DEFAULT FALSE,
    is_prospect     BOOLEAN NOT NULL DEFAULT FALSE,
    customer_code   TEXT,                                 -- llx_societe.code_client
    supplier_code   TEXT,                                 -- llx_societe.code_fournisseur
    email           TEXT NOT NULL DEFAULT '',
    phone           TEXT NOT NULL DEFAULT '',
    address         JSONB NOT NULL DEFAULT '{}',
    acct_customer   TEXT NOT NULL DEFAULT '',
    acct_supplier   TEXT NOT NULL DEFAULT '',
    custom_fields   JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      BIGINT,
    updated_by      BIGINT,
    row_version     BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, customer_code),
    UNIQUE (entity_id, supplier_code)
);
CREATE INDEX ferp_org_entity_name_idx ON ferp_organizations (entity_id, name);
CREATE INDEX ferp_org_parent_idx ON ferp_organizations (parent_id) WHERE parent_id IS NOT NULL;

CREATE TABLE ferp_contacts (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id       BIGINT NOT NULL REFERENCES ferp_entities(id),
    org_id          BIGINT NOT NULL REFERENCES ferp_organizations(id) ON DELETE CASCADE,
    first_name      TEXT NOT NULL DEFAULT '',
    last_name       TEXT NOT NULL DEFAULT '',
    email           TEXT NOT NULL DEFAULT '',
    phone           TEXT NOT NULL DEFAULT '',
    role            TEXT NOT NULL DEFAULT 'other',
    is_default      BOOLEAN NOT NULL DEFAULT FALSE,
    custom_fields   JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      BIGINT,
    updated_by      BIGINT,
    row_version     BIGINT NOT NULL DEFAULT 1
);
CREATE INDEX ferp_contact_org_idx ON ferp_contacts (org_id);

CREATE TABLE ferp_categories (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    code        TEXT NOT NULL,
    label       TEXT NOT NULL,
    scope       TEXT NOT NULL,                            -- organization | contact | ...
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entity_id, scope, code)
);

CREATE TABLE ferp_category_links (
    category_id BIGINT NOT NULL REFERENCES ferp_categories(id) ON DELETE CASCADE,
    scope       TEXT NOT NULL,
    object_id   BIGINT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (category_id, scope, object_id)
);
