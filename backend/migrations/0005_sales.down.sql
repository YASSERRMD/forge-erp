-- Manual-recovery rollback for 0005_sales (never applied automatically).
DROP TABLE IF EXISTS ferp_payment_allocations;
DROP TABLE IF EXISTS ferp_payments;
DROP TABLE IF EXISTS ferp_doc_counters;
DROP TABLE IF EXISTS ferp_doc_lines;
DROP TABLE IF EXISTS ferp_documents;
