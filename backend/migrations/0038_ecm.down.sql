-- pending_ecm down migration (manual recovery only).

DROP TRIGGER IF EXISTS ferp_file_texts_tsv_trg ON ferp_file_texts;
DROP FUNCTION IF EXISTS ferp_file_texts_tsv_trigger();
DROP TABLE IF EXISTS ferp_file_texts;
DROP TABLE IF EXISTS ferp_file_versions;
DROP TABLE IF EXISTS ferp_filing_rules;
DROP INDEX IF EXISTS ferp_files_folder_name_uidx;
ALTER TABLE ferp_files DROP COLUMN IF EXISTS version;
ALTER TABLE ferp_files DROP COLUMN IF EXISTS folder_id;
DROP POLICY IF EXISTS tenant_isolation ON ferp_folders;
DROP TABLE IF EXISTS ferp_folders;
