-- 0011_hr: leave requests, expense reports + lines, salary records.
-- Dolibarr equivalents: llx_holiday, llx_expensereport(+det), llx_salary.
-- Employees are identity users referenced by login (no separate employee table).

CREATE TABLE ferp_leave_requests (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    user_login  TEXT NOT NULL,
    type        TEXT NOT NULL,                  -- paid|sick|unpaid
    start_date  TIMESTAMPTZ NOT NULL,
    end_date    TIMESTAMPTZ NOT NULL,
    days        BIGINT NOT NULL,                -- inclusive calendar days, server-computed
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 draft, 1 submitted, 2 approved, -1 rejected, -2 canceled
    comment     TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    CHECK (end_date >= start_date),
    CHECK (days > 0)
);
CREATE INDEX ferp_leave_user_idx ON ferp_leave_requests (user_login, status);

CREATE TABLE ferp_expense_reports (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    ref         TEXT NOT NULL,
    user_login  TEXT NOT NULL,
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 draft, 1 submitted, 2 approved, 3 paid, -1 rejected, -2 canceled
    total       BIGINT NOT NULL DEFAULT 0,      -- minor units, server-computed
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, ref)
);
CREATE INDEX ferp_expense_user_idx ON ferp_expense_reports (user_login, status);

CREATE TABLE ferp_expense_lines (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    report_id   BIGINT NOT NULL REFERENCES ferp_expense_reports(id) ON DELETE CASCADE,
    date        TIMESTAMPTZ NOT NULL,
    label       TEXT NOT NULL,
    amount      BIGINT NOT NULL CHECK (amount > 0),
    vat_bps     BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX ferp_expense_lines_report_idx ON ferp_expense_lines (report_id);

CREATE TABLE ferp_salaries (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    user_login  TEXT NOT NULL,
    period      TEXT NOT NULL,                  -- YYYY-MM
    gross       BIGINT NOT NULL CHECK (gross >= 0),
    charges     BIGINT NOT NULL CHECK (charges >= 0),
    net         BIGINT NOT NULL,
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 draft, 1 validated, 2 paid, -1 canceled
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, user_login, period),
    CHECK (charges <= gross),
    CHECK (net = gross - charges)
);
