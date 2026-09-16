-- pending_mrp: workstations, routing operations and MO operation scheduling.
-- Phase 2 MRP depth + Phase 5 workstation module, planned as one piece of work.
-- Centrally renumbered on merge (pending_ prefix sorts after 00NN, applies last).
-- Dolibarr equivalents: llx_product_attribute/workstation tables + MRP scheduling
-- (Dolibarr core has no workstation capacity model; this is new structure).

CREATE TABLE ferp_workstations (
    id                 BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id          BIGINT NOT NULL REFERENCES ferp_entities(id),
    code               TEXT NOT NULL,
    label              TEXT NOT NULL,
    daily_capacity_min BIGINT NOT NULL CHECK (daily_capacity_min > 0),
    status             SMALLINT NOT NULL DEFAULT 1,   -- 0 inactive, 1 active
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version        BIGINT NOT NULL DEFAULT 1,
    UNIQUE (entity_id, code)
);
CREATE INDEX ferp_workstations_entity_idx ON ferp_workstations (entity_id, status);

-- Routing: ordered operations per BOM, each bound to a workstation.
CREATE TABLE ferp_bom_operations (
    id                   BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id            BIGINT NOT NULL REFERENCES ferp_entities(id),
    bom_id               BIGINT NOT NULL REFERENCES ferp_boms(id) ON DELETE CASCADE,
    seq                  INTEGER NOT NULL CHECK (seq > 0),
    workstation_id       BIGINT NOT NULL REFERENCES ferp_workstations(id),
    run_minutes_per_unit BIGINT NOT NULL CHECK (run_minutes_per_unit >= 0),
    setup_minutes        BIGINT NOT NULL DEFAULT 0 CHECK (setup_minutes >= 0),
    UNIQUE (bom_id, seq)
);
CREATE INDEX ferp_bom_operations_bom_idx ON ferp_bom_operations (bom_id);

-- Scheduled MO operations: forward-scheduled from routing x order qty.
-- Overload is flagged on the operation, never force-resolved by rescheduling.
CREATE TABLE ferp_mo_operations (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entity_id        BIGINT NOT NULL REFERENCES ferp_entities(id),
    mo_id            BIGINT NOT NULL REFERENCES ferp_mos(id) ON DELETE CASCADE,
    seq              INTEGER NOT NULL CHECK (seq > 0),
    workstation_id   BIGINT NOT NULL REFERENCES ferp_workstations(id),
    planned_minutes  BIGINT NOT NULL CHECK (planned_minutes > 0),
    scheduled_start  TIMESTAMPTZ NOT NULL,
    scheduled_end    TIMESTAMPTZ NOT NULL,
    status           SMALLINT NOT NULL DEFAULT 0,   -- 0 pending, 1 done, -1 canceled
    overloaded       BOOLEAN NOT NULL DEFAULT FALSE,
    actual_minutes   BIGINT NOT NULL DEFAULT 0 CHECK (actual_minutes >= 0),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    row_version      BIGINT NOT NULL DEFAULT 1,
    UNIQUE (mo_id, seq),
    CHECK (scheduled_end > scheduled_start)
);
CREATE INDEX ferp_mo_operations_ws_idx ON ferp_mo_operations (workstation_id, scheduled_start);
CREATE INDEX ferp_mo_operations_mo_idx ON ferp_mo_operations (mo_id, seq);
