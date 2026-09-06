-- 0009_services down: drop services tables (reverse of up).
DROP TABLE IF EXISTS ferp_ticket_messages;
DROP TABLE IF EXISTS ferp_tickets;
DROP TABLE IF EXISTS ferp_interventions;
DROP TABLE IF EXISTS ferp_service_contracts;
DROP TABLE IF EXISTS ferp_time_entries;
DROP TABLE IF EXISTS ferp_project_tasks;
DROP TABLE IF EXISTS ferp_projects;
