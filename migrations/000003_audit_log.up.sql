-- Append-only record of administrative actions. Rows are written in the same transaction
-- as the change they describe and can never be modified or removed.
CREATE TABLE audit_log (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- No FK: entries must survive account deletion, so the actor's name is snapshotted.
    actor_id    bigint,
    actor_name  text        NOT NULL DEFAULT '',
    action      text        NOT NULL CHECK (action IN (
                    'village.verify', 'village.reject', 'village.suspend', 'village.reinstate',
                    'report.resolve', 'report.dismiss', 'user.promote_admin')),
    target_type text        NOT NULL CHECK (target_type IN ('village', 'report', 'user')),
    target_id   bigint      NOT NULL,
    from_status text        NOT NULL DEFAULT '',
    to_status   text        NOT NULL DEFAULT '',
    note        text        NOT NULL DEFAULT '' CHECK (length(note) <= 1000),
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_created_idx ON audit_log (created_at DESC, id DESC);
CREATE INDEX audit_log_target_idx ON audit_log (target_type, target_id, created_at DESC);
CREATE INDEX audit_log_action_idx ON audit_log (action, created_at DESC);

CREATE FUNCTION audit_log_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only' USING ERRCODE = 'insufficient_privilege';
END;
$$;

CREATE TRIGGER audit_log_no_update_delete
    BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_immutable();
CREATE TRIGGER audit_log_no_truncate
    BEFORE TRUNCATE ON audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION audit_log_immutable();

-- Admins can leave a note when closing a report.
ALTER TABLE reports
    ADD COLUMN resolution_note text NOT NULL DEFAULT '' CHECK (length(resolution_note) <= 1000);
CREATE INDEX reports_target_idx ON reports (target_type, target_id);
