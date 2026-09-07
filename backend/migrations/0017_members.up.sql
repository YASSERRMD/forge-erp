-- 0017_members: member types, members, subscriptions, donations.
-- Dolibarr equivalents: llx_adherent_type, llx_adherent, llx_subscription, llx_don.

CREATE TABLE ferp_member_types (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    code        TEXT NOT NULL,
    label       TEXT NOT NULL,
    annual_fee  BIGINT NOT NULL DEFAULT 0 CHECK (annual_fee >= 0),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entity_id, code)
);

CREATE TABLE ferp_members (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    ref         TEXT NOT NULL,
    type_id     BIGINT NOT NULL REFERENCES ferp_member_types(id),
    first_name  TEXT NOT NULL DEFAULT '',
    last_name   TEXT NOT NULL DEFAULT '',
    company     TEXT NOT NULL DEFAULT '',
    email       TEXT NOT NULL DEFAULT '',
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 draft, 1 active, -1 resigned, -2 excluded
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, ref)
);
CREATE INDEX ferp_members_type_idx ON ferp_members (type_id, status);

CREATE TABLE ferp_subscriptions (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    member_id   BIGINT NOT NULL REFERENCES ferp_members(id) ON DELETE CASCADE,
    year        TEXT NOT NULL,                  -- YYYY
    amount      BIGINT NOT NULL CHECK (amount > 0),
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 draft, 1 validated, 2 paid, -1 canceled
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, member_id, year)
);

CREATE TABLE ferp_donations (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    ref         TEXT NOT NULL,
    donor_name  TEXT NOT NULL,
    org_id      BIGINT REFERENCES ferp_organizations(id) ON DELETE SET NULL,
    amount      BIGINT NOT NULL CHECK (amount > 0),
    donated_at  TIMESTAMPTZ NOT NULL,
    method      TEXT NOT NULL,
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 promised, 1 paid, -1 canceled
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, ref)
);
CREATE INDEX ferp_donations_status_idx ON ferp_donations (entity_id, status);
