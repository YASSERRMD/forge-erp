-- Manual-recovery rollback for 0001_platform (the migrator never applies down files automatically).
DROP TABLE IF EXISTS ferp_outbox;
DROP TABLE IF EXISTS ferp_config;
DROP TABLE IF EXISTS ferp_entities;
