-- name: ListActivityTypes :many
SELECT id, slug, name FROM activity_types ORDER BY name;

-- name: ListRegionChildren :many
-- Children of a region; pass NULL for top-level provinces.
SELECT id, parent_id, kind, name
FROM regions
WHERE parent_id IS NOT DISTINCT FROM sqlc.narg(parent_id)::bigint
ORDER BY name;
