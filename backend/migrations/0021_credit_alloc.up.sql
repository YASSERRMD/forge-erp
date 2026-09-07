-- 0021_credit_alloc: credit-note applications against invoices.
-- Credit notes (ferp_documents type credit_note) reduce the open balance
-- without mutating posted payments.

CREATE TABLE ferp_credit_allocations (
    invoice_id BIGINT NOT NULL REFERENCES ferp_documents(id) ON DELETE CASCADE,
    credit_id  BIGINT NOT NULL REFERENCES ferp_documents(id) ON DELETE CASCADE,
    amount     BIGINT NOT NULL CHECK (amount > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (invoice_id, credit_id)
);
CREATE INDEX ferp_credit_allocations_invoice_idx ON ferp_credit_allocations (invoice_id);
