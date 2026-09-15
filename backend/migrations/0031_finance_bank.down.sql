-- Manual-recovery rollback for pending_finance_bank (never applied automatically).
DROP POLICY IF EXISTS tenant_isolation ON ferp_tax_charges;
DROP POLICY IF EXISTS tenant_isolation ON ferp_tax_periods;
DROP POLICY IF EXISTS tenant_isolation ON ferp_vat_rates;
DROP INDEX IF EXISTS ferp_tax_charges_due_idx;
DROP TABLE IF EXISTS ferp_tax_charges;
DROP TABLE IF EXISTS ferp_tax_periods;
ALTER TABLE ferp_entry_lines DROP COLUMN IF EXISTS vat_rate_bps;
DROP TABLE IF EXISTS ferp_vat_rates;
DROP INDEX IF EXISTS ferp_bank_tx_ref_idx;
DROP INDEX IF EXISTS ferp_bank_tx_dedupe_idx;
ALTER TABLE ferp_bank_transactions DROP COLUMN IF EXISTS bank_ref;
