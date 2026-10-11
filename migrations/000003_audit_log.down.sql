DROP INDEX IF EXISTS reports_target_idx;
ALTER TABLE reports DROP COLUMN IF EXISTS resolution_note;
DROP TABLE IF EXISTS audit_log;
DROP FUNCTION IF EXISTS audit_log_immutable();
