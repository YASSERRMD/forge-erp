-- pending_loan_sched: member-loan headers + amortisation lines (Phase 2 loan/don depth).
-- Dolibarr equivalents: llx_loan (+llx_loan_schedule) scoped to the members
-- context; ledger legs stay in ferp_entries via finance PostEntry.
-- Centrally renumbered on merge (pending_ prefix keeps lexical order last).

CREATE TABLE ferp_member_loans (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id   BIGINT NOT NULL REFERENCES ferp_entities(id),
    label       TEXT NOT NULL,
    principal   BIGINT NOT NULL CHECK (principal > 0),
    rate_bps    INTEGER NOT NULL DEFAULT 0 CHECK (rate_bps >= 0),
    start_date  TIMESTAMPTZ NOT NULL,
    periods     INTEGER NOT NULL CHECK (periods > 0),
    status      SMALLINT NOT NULL DEFAULT 0,    -- 0 draft, 1 disbursed, 2 repaid, -1 canceled
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version BIGINT NOT NULL DEFAULT 1
);

CREATE TABLE ferp_member_loan_lines (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    loan_id     BIGINT NOT NULL REFERENCES ferp_member_loans(id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL,                 -- 1-based installment number
    due_date    TIMESTAMPTZ NOT NULL,
    payment     BIGINT NOT NULL CHECK (payment > 0),
    principal   BIGINT NOT NULL CHECK (principal >= 0),
    interest    BIGINT NOT NULL CHECK (interest >= 0),
    remaining   BIGINT NOT NULL CHECK (remaining >= 0),
    paid        BOOLEAN NOT NULL DEFAULT FALSE,
    paid_at     TIMESTAMPTZ,
    UNIQUE (loan_id, seq)
);
CREATE INDEX ferp_member_loan_lines_loan_idx ON ferp_member_loan_lines (loan_id, seq);
