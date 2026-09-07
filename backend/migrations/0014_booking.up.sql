-- 0014_booking: bookable resources + guarded reservations.
-- Dolibarr equivalents: llx_resource, llx_resource_booking (bookcal).

CREATE TABLE ferp_resources (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    code        TEXT NOT NULL,
    label       TEXT NOT NULL,
    capacity    BIGINT NOT NULL CHECK (capacity >= 1),
    status      SMALLINT NOT NULL DEFAULT 1,    -- 1 active, 0 inactive
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, code)
);

CREATE TABLE ferp_bookings (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    resource_id BIGINT NOT NULL REFERENCES ferp_resources(id) ON DELETE CASCADE,
    org_id      BIGINT REFERENCES ferp_organizations(id) ON DELETE SET NULL,
    user_login  TEXT NOT NULL,
    start_at    TIMESTAMPTZ NOT NULL,
    end_at      TIMESTAMPTZ NOT NULL,
    seats       BIGINT NOT NULL CHECK (seats >= 1),
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 booked, 1 checkedin, 2 completed, -1 canceled
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    CHECK (end_at > start_at)
);
CREATE INDEX ferp_bookings_resource_idx ON ferp_bookings (resource_id, status, start_at, end_at);
