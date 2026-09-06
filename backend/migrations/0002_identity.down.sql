-- Manual-recovery rollback for 0002_identity (never applied automatically).
DROP TABLE IF EXISTS ferp_sessions;
DROP TABLE IF EXISTS ferp_rights;
DROP TABLE IF EXISTS ferp_group_members;
DROP TABLE IF EXISTS ferp_groups;
DROP TABLE IF EXISTS ferp_users;
