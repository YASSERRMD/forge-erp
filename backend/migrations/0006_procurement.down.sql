-- Manual-recovery rollback for 0006_procurement (never applied automatically).
DROP TABLE IF EXISTS ferp_supplier_allocations;
DROP TABLE IF EXISTS ferp_supplier_payments;
DROP TABLE IF EXISTS ferp_supplier_prices;
DROP TABLE IF EXISTS ferp_supplier_doc_lines;
DROP TABLE IF EXISTS ferp_supplier_docs;
