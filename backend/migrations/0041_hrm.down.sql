-- pending_hrm down: drop HRM record tables (reverse of pending_hrm.up.sql).

DROP TABLE IF EXISTS ferp_evaluations;
DROP TABLE IF EXISTS ferp_employee_skills;
DROP TABLE IF EXISTS ferp_employees;
DROP TABLE IF EXISTS ferp_establishments;
