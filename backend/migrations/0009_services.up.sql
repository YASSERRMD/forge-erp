-- 0009_services: projects/tasks/time, service contracts, interventions, tickets.
-- Dolibarr equivalents: llx_projet, llx_projet_task, llx_projet_task_time,
-- llx_contrat, llx_contratdet (lines omitted: use documents lines instead),
-- llx_fichinter, llx_ticket, llx_ticket_msg.

CREATE TABLE ferp_projects (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    ref         TEXT NOT NULL,
    label       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    org_id      BIGINT REFERENCES ferp_organizations(id),
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 draft, 1 active, 2 onhold, 3 closed, -1 canceled
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  BIGINT REFERENCES ferp_users(id),
    updated_by  BIGINT REFERENCES ferp_users(id),
    row_version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, ref)
);
CREATE INDEX ferp_projects_entity_idx ON ferp_projects (entity_id, status);

CREATE TABLE ferp_project_tasks (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    project_id  BIGINT NOT NULL REFERENCES ferp_projects(id) ON DELETE CASCADE,
    label       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 todo, 1 doing, 2 done, -1 canceled
    assignee    TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1
);
CREATE INDEX ferp_project_tasks_project_idx ON ferp_project_tasks (project_id, status);

CREATE TABLE ferp_time_entries (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    project_id  BIGINT NOT NULL REFERENCES ferp_projects(id) ON DELETE CASCADE,
    task_id     BIGINT NOT NULL REFERENCES ferp_project_tasks(id) ON DELETE CASCADE,
    author      TEXT NOT NULL,
    hours       BIGINT NOT NULL CHECK (hours > 0),  -- hundredths of an hour
    entry_date  TIMESTAMPTZ NOT NULL,
    note        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ferp_time_entries_task_idx ON ferp_time_entries (task_id, entry_date);

CREATE TABLE ferp_service_contracts (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    ref         TEXT NOT NULL,
    org_id      BIGINT NOT NULL REFERENCES ferp_organizations(id),
    label       TEXT NOT NULL,
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 draft, 1 active, 2 suspended, 3 closed, -1 canceled
    start_date  TIMESTAMPTZ,
    end_date    TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, ref),
    CHECK (end_date IS NULL OR start_date IS NULL OR end_date >= start_date)
);
CREATE INDEX ferp_service_contracts_org_idx ON ferp_service_contracts (org_id, status);

CREATE TABLE ferp_interventions (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    ref         TEXT NOT NULL,
    org_id      BIGINT NOT NULL REFERENCES ferp_organizations(id),
    project_id  BIGINT REFERENCES ferp_projects(id) ON DELETE SET NULL,
    contract_id BIGINT REFERENCES ferp_service_contracts(id) ON DELETE SET NULL,
    label       TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 scheduled, 1 inprogress, 2 done, -1 canceled
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, ref)
);
CREATE INDEX ferp_interventions_org_idx ON ferp_interventions (org_id, status);

CREATE TABLE ferp_tickets (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    ref         TEXT NOT NULL,
    org_id      BIGINT REFERENCES ferp_organizations(id) ON DELETE SET NULL,
    project_id  BIGINT REFERENCES ferp_projects(id) ON DELETE SET NULL,
    subject     TEXT NOT NULL,
    priority    SMALLINT NOT NULL DEFAULT 2 CHECK (priority BETWEEN 1 AND 4),
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 open, 1 pending, 2 resolved, 3 closed
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, ref)
);
CREATE INDEX ferp_tickets_entity_idx ON ferp_tickets (entity_id, status);

CREATE TABLE ferp_ticket_messages (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    ticket_id   BIGINT NOT NULL REFERENCES ferp_tickets(id) ON DELETE CASCADE,
    author      TEXT NOT NULL,
    body        TEXT NOT NULL,
    internal    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ferp_ticket_messages_ticket_idx ON ferp_ticket_messages (ticket_id, created_at);
