-- 0016_agenda: calendar events with reminder tracking.
-- Dolibarr equivalents: llx_actioncomm (+ notify reminders).

CREATE TABLE ferp_events (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    title       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    location    TEXT NOT NULL DEFAULT '',
    start_at    TIMESTAMPTZ NOT NULL,
    end_at      TIMESTAMPTZ NOT NULL,
    all_day     BOOLEAN NOT NULL DEFAULT FALSE,
    owner_login TEXT NOT NULL,
    attendees   JSONB NOT NULL DEFAULT '[]',
    org_id      BIGINT REFERENCES ferp_organizations(id) ON DELETE SET NULL,
    project_id  BIGINT REFERENCES ferp_projects(id) ON DELETE SET NULL,
    reminder_min BIGINT NOT NULL DEFAULT 0,
    reminded_at TIMESTAMPTZ,
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 scheduled, 1 done, -1 canceled
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    CHECK (end_at > start_at)
);
CREATE INDEX ferp_events_window_idx ON ferp_events (entity_id, status, start_at, end_at);
CREATE INDEX ferp_events_reminder_idx ON ferp_events (status, start_at) WHERE reminded_at IS NULL AND reminder_min > 0;
