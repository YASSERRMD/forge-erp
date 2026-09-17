-- Manual-recovery rollback for 0045_statutory_acct (never applied automatically).
DROP TABLE IF EXISTS ferp_close_log;
DROP TABLE IF EXISTS ferp_account_postings;
DROP TABLE IF EXISTS ferp_accounting_accounts;
