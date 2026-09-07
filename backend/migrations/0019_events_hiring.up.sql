-- 0019_events_hiring: organized events + registrations, job positions + applications.
-- Dolibarr equivalents: eventorganization, llx_recruitment.

CREATE TABLE ferp_org_events (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    location    TEXT NOT NULL DEFAULT '',
    starts_at   TIMESTAMPTZ NOT NULL,
    ends_at     TIMESTAMPTZ NOT NULL,
    capacity    BIGINT NOT NULL CHECK (capacity >= 1),
    price       BIGINT NOT NULL DEFAULT 0 CHECK (price >= 0),
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 draft, 1 published, 2 closed, -1 canceled
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    CHECK (ends_at > starts_at)
);
CREATE INDEX ferp_org_events_status_idx ON ferp_org_events (entity_id, status);

CREATE TABLE ferp_registrations (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    event_id    BIGINT NOT NULL REFERENCES ferp_org_events(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    email       TEXT NOT NULL DEFAULT '',
    org_id      BIGINT REFERENCES ferp_organizations(id) ON DELETE SET NULL,
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 registered, 1 confirmed, 2 attended, -1 canceled
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entity_id, event_id, email)
);
CREATE INDEX ferp_registrations_event_idx ON ferp_registrations (event_id, status);

CREATE TABLE ferp_positions (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    code        TEXT NOT NULL,
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 draft, 1 open, 2 closed
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, code)
);

CREATE TABLE ferp_applications (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    position_id BIGINT NOT NULL REFERENCES ferp_positions(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    email       TEXT NOT NULL DEFAULT '',
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 received, 1 screening, 2 interview, 3 offer, 4 hired, -1 rejected
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1
);
CREATE INDEX ferp_applications_position_idx ON ferp_applications (position_id, status);
