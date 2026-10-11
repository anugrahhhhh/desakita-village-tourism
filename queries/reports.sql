-- Owned by internal/admin.

-- name: ReportTargetExists :one
SELECT CASE @target_type::text
         WHEN 'village' THEN EXISTS (SELECT 1 FROM villages tv WHERE tv.id = @target_id::bigint)
         WHEN 'package' THEN EXISTS (SELECT 1 FROM tour_packages tp WHERE tp.id = @target_id::bigint)
         ELSE false
       END::boolean AS target_exists;

-- name: CreateReport :one
INSERT INTO reports (reporter_id, target_type, target_id, reason)
VALUES ($1, $2, $3, $4)
RETURNING id;

-- name: ListOpenReports :many
SELECT rp.id, rp.target_type, rp.target_id, rp.reason, rp.created_at,
       coalesce(u.email, '')::text AS reporter_email,
       coalesce(v.name, pv.name, '')::text AS village_name,
       coalesce(v.slug, pv.slug, '')::text AS village_slug,
       coalesce(p.name, '')::text AS package_name
FROM reports rp
LEFT JOIN users u ON u.id = rp.reporter_id
LEFT JOIN villages v ON rp.target_type = 'village' AND v.id = rp.target_id
LEFT JOIN tour_packages p ON rp.target_type = 'package' AND p.id = rp.target_id
LEFT JOIN villages pv ON pv.id = p.village_id
WHERE rp.status = 'open'
ORDER BY rp.created_at;

-- name: CountOpenReports :one
SELECT count(*) FROM reports WHERE status = 'open';

-- name: ResolveReport :execrows
UPDATE reports
SET status = @status::text, resolved_by = @resolved_by, resolved_at = now()
WHERE id = @id AND status = 'open';
