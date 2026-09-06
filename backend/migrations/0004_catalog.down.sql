-- Manual-recovery rollback for 0004_catalog (never applied automatically).
ALTER TABLE ferp_stock_movements DROP CONSTRAINT IF EXISTS ferp_movement_lot_fk;
DROP TABLE IF EXISTS ferp_lots;
DROP TABLE IF EXISTS ferp_stock_levels;
DROP TABLE IF EXISTS ferp_stock_movements;
DROP TABLE IF EXISTS ferp_warehouses;
DROP TABLE IF EXISTS ferp_products;
