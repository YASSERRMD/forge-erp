-- pending_portal down (manual-recovery rollback; never applied automatically).

ALTER TABLE ferp_jobs DROP COLUMN IF EXISTS cron_expr;
DROP TABLE IF EXISTS ferp_portal_tokens;
