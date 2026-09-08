-- Manual-recovery rollback for 0008_platform2 (never applied automatically).
DROP TABLE IF EXISTS ferp_webhooks;
DROP TABLE IF EXISTS ferp_job_runs;
DROP TABLE IF EXISTS ferp_jobs;
DROP TABLE IF EXISTS ferp_notify_outbox;
DROP TABLE IF EXISTS ferp_notify_templates;
DROP TABLE IF EXISTS ferp_files;
