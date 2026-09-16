-- pending_hrm: employee records, establishments, skills, evaluations.
-- Centrally renumbered on merge (pending_ prefix by convention).
--
-- ferp_employees are HR records DISTINCT from identity users: there is
-- deliberately NO FK to identity users. Identity logins are authentication
-- handles (may be renamed, deactivated, or shared across entities, and one
-- human can hold several logins); HR needs a stable per-entity employment
-- record with its own code, establishment posting, job title, hire date and
-- lifecycle status. Leave/salary rows keep referencing user_login for
-- backward compatibility; new HRM flows reference ferp_employees.id.

CREATE TABLE ferp_establishments (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    code        TEXT NOT NULL,
    label       TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, code)
);

CREATE TABLE ferp_employees (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id        BIGINT NOT NULL REFERENCES ferp_entities(id),
    code             TEXT NOT NULL,
    name             TEXT NOT NULL,
    establishment_id BIGINT NOT NULL REFERENCES ferp_establishments(id),
    job_title        TEXT NOT NULL DEFAULT '',
    hire_date        DATE NOT NULL,
    status           SMALLINT NOT NULL DEFAULT 0, -- 0 active, 1 suspended, 2 terminated
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version      BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, code)
);
CREATE INDEX ferp_employees_est_idx ON ferp_employees (establishment_id);

CREATE TABLE ferp_employee_skills (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    employee_id BIGINT NOT NULL REFERENCES ferp_employees(id) ON DELETE CASCADE,
    skill       TEXT NOT NULL,
    level       SMALLINT NOT NULL DEFAULT 1, -- 1..5
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entity_id, employee_id, skill)
);

CREATE TABLE ferp_evaluations (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    employee_id BIGINT NOT NULL REFERENCES ferp_employees(id) ON DELETE CASCADE,
    period      TEXT NOT NULL, -- YYYY-MM
    rating      SMALLINT NOT NULL, -- 1..5
    notes       TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (entity_id, employee_id, period)
);
