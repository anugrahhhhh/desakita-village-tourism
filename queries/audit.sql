-- Owned by internal/platform/audit (writes) and internal/admin (reads).

-- name: InsertAuditEntry :exec
INSERT INTO audit_log (actor_id, actor_name, action, target_type, target_id, from_status, to_status, note)
VALUES (
    sqlc.narg(actor_id)::bigint,
    coalesce((SELECT u.name FROM users u WHERE u.id = sqlc.narg(actor_id)::bigint), ''),
    @action, @target_type, @target_id, @from_status, @to_status, @note
);

-- name: ListAuditEntries :many
SELECT * FROM audit_log
WHERE (sqlc.narg(action)::text IS NULL OR action = sqlc.narg(action)::text)
  AND (sqlc.narg(target_type)::text IS NULL OR target_type = sqlc.narg(target_type)::text)
  AND (sqlc.narg(target_id)::bigint IS NULL OR target_id = sqlc.narg(target_id)::bigint)
ORDER BY created_at DESC, id DESC
LIMIT @page_size OFFSET @page_offset;
