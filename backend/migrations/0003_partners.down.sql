-- Manual-recovery rollback for 0003_partners (never applied automatically).
DROP TABLE IF EXISTS ferp_category_links;
DROP TABLE IF EXISTS ferp_categories;
DROP TABLE IF EXISTS ferp_contacts;
DROP TABLE IF EXISTS ferp_organizations;
