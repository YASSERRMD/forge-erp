-- 0042_p5a_sales down: manual recovery only (see platform.Migrate).
ALTER TABLE ferp_doc_lines DROP CONSTRAINT IF EXISTS ferp_doc_lines_kind_check;
ALTER TABLE ferp_doc_lines DROP COLUMN IF EXISTS kind;
ALTER TABLE ferp_doc_lines DROP CONSTRAINT IF EXISTS ferp_doc_lines_qty_check;
ALTER TABLE ferp_doc_lines ADD CHECK (qty > 0);
ALTER TABLE ferp_documents DROP COLUMN IF EXISTS incoterm;
DROP TABLE IF EXISTS ferp_price_assignments;
DROP TABLE IF EXISTS ferp_price_rules;
DROP TABLE IF EXISTS ferp_stock_transfer_lines;
DROP TABLE IF EXISTS ferp_stock_transfers;
DROP TABLE IF EXISTS ferp_incoterms;
