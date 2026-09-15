-- Manual-recovery rollback for pending_loan_sched (never applied automatically).
DROP TABLE IF EXISTS ferp_member_loan_lines;
DROP TABLE IF EXISTS ferp_member_loans;
