-- Owned by internal/admin.

-- name: ReportTargetExists :one
SELECT CASE @target_type::text
         WHEN 'village' THEN EXISTS (SELECT 1 FROM villages tv WHERE tv.id = @target_id::bigint)
         WHEN 'package' THEN EXISTS (SELECT 1 FROM tour_packages tp WHERE tp.id = @target_id::bigint)
         WHEN 'review' THEN EXISTS (SELECT 1 FROM reviews tr WHERE tr.id = @target_id::bigint AND tr.status = 'published')
         ELSE false
       END::boolean AS target_exists;

-- name: CreateReport :one
INSERT INTO reports (reporter_id, target_type, target_id, reason)
VALUES ($1, $2, $3, $4)
RETURNING id;

-- name: ListReports :many
-- Open reports oldest first (a queue); closed ones newest first (history).
SELECT rp.id, rp.target_type, rp.target_id, rp.reason, rp.status, rp.created_at,
       rp.resolved_at, rp.resolution_note,
       coalesce(u.email, '')::text AS reporter_email,
       coalesce(ru.name, '')::text AS resolver_name,
       coalesce(v.id, pv.id, rvv.id, 0)::bigint AS village_id,
       coalesce(v.name, pv.name, rvv.name, '')::text AS village_name,
       coalesce(v.slug, pv.slug, rvv.slug, '')::text AS village_slug,
       coalesce(p.name, rvp.name, '')::text AS package_name,
       coalesce(rvp.id, 0)::bigint AS review_package_id,
       coalesce(rv.body, '')::text AS review_body
FROM reports rp
LEFT JOIN users u ON u.id = rp.reporter_id
LEFT JOIN users ru ON ru.id = rp.resolved_by
LEFT JOIN villages v ON rp.target_type = 'village' AND v.id = rp.target_id
LEFT JOIN tour_packages p ON rp.target_type = 'package' AND p.id = rp.target_id
LEFT JOIN villages pv ON pv.id = p.village_id
LEFT JOIN reviews rv ON rp.target_type = 'review' AND rv.id = rp.target_id
LEFT JOIN tour_packages rvp ON rvp.id = rv.package_id
LEFT JOIN villages rvv ON rvv.id = rv.village_id
WHERE rp.status = @status::text
ORDER BY CASE WHEN @status::text = 'open' THEN rp.created_at END ASC,
         CASE WHEN @status::text <> 'open' THEN rp.resolved_at END DESC,
         rp.id
LIMIT 200;

-- name: CountOpenReportsForVillage :one
-- Reports about the village itself or any of its packages.
SELECT count(*) FROM reports rp
WHERE rp.status = 'open'
  AND ((rp.target_type = 'village' AND rp.target_id = @village_id::bigint)
    OR (rp.target_type = 'package' AND rp.target_id IN (SELECT id FROM tour_packages WHERE village_id = @village_id::bigint))
    OR (rp.target_type = 'review' AND rp.target_id IN (SELECT id FROM reviews WHERE village_id = @village_id::bigint)));

-- name: CountOpenReports :one
SELECT count(*) FROM reports WHERE status = 'open';

-- name: ResolveReport :execrows
UPDATE reports
SET status = @status::text, resolved_by = @resolved_by, resolved_at = now(), resolution_note = @resolution_note
WHERE id = @id AND status = 'open';
