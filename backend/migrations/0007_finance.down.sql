-- Manual-recovery rollback for 0007_finance (never applied automatically).
DROP TABLE IF EXISTS ferp_loans;
DROP TABLE IF EXISTS ferp_bank_transactions;
DROP TABLE IF EXISTS ferp_bank_accounts;
DROP TABLE IF EXISTS ferp_entry_lines;
DROP TABLE IF EXISTS ferp_entries;
DROP TABLE IF EXISTS ferp_fiscal_years;
DROP TABLE IF EXISTS ferp_journals;
DROP TABLE IF EXISTS ferp_accounts;
