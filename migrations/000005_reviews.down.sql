-- audit_log is append-only, so review entries can't be deleted; the down migration
-- refuses to run once any exist rather than silently weakening the constraint.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM audit_log WHERE target_type = 'review') THEN
        RAISE EXCEPTION 'audit_log contains review entries; cannot roll back 000005';
    END IF;
END;
$$;

ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_type_check;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_target_type_check
    CHECK (target_type IN ('village', 'report', 'user'));
ALTER TABLE audit_log DROP CONSTRAINT audit_log_action_check;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_action_check CHECK (action IN (
    'village.verify', 'village.reject', 'village.suspend', 'village.reinstate',
    'report.resolve', 'report.dismiss', 'user.promote_admin'));

DELETE FROM reports WHERE target_type = 'review';
ALTER TABLE reports DROP CONSTRAINT reports_target_type_check;
ALTER TABLE reports ADD CONSTRAINT reports_target_type_check
    CHECK (target_type IN ('village', 'package'));

DROP TABLE IF EXISTS reviews;
