-- pending_payroll: salary payroll runs + run lines (Phase 2).
-- Centrally renumbered on merge (pending_ prefix keeps it out of the
-- numbered history until then).
-- Amounts on lines are GIVEN/recorded inputs (gross/charges/net with
-- net = gross - charges); payroll calculation (jurisdictional tax/social
-- logic) is explicitly out of scope and never computed server-side.

CREATE TABLE ferp_payroll_runs (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    label       TEXT NOT NULL,
    period_start TIMESTAMPTZ NOT NULL,
    period_end   TIMESTAMPTZ NOT NULL,
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 draft, 1 posted, -1 canceled
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    CHECK (period_end >= period_start),
    CHECK (label <> '')
);
CREATE INDEX ferp_payroll_runs_entity_idx ON ferp_payroll_runs (entity_id, status);

CREATE TABLE ferp_payroll_run_lines (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    run_id      BIGINT NOT NULL REFERENCES ferp_payroll_runs(id) ON DELETE CASCADE,
    salary_id   BIGINT REFERENCES ferp_salaries(id) ON DELETE SET NULL,
    gross       BIGINT NOT NULL CHECK (gross >= 0),
    charges     BIGINT NOT NULL CHECK (charges >= 0),
    net         BIGINT NOT NULL,
    CHECK (charges <= gross),
    CHECK (net = gross - charges)
);
CREATE INDEX ferp_payroll_run_lines_run_idx ON ferp_payroll_run_lines (run_id);

CREATE POLICY tenant_isolation ON ferp_payroll_runs
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
CREATE POLICY tenant_isolation ON ferp_payroll_run_lines
    USING (entity_id = NULLIF(current_setting('app.entity_id', true), '')::bigint);
