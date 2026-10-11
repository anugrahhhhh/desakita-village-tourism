-- name: CreatePackage :one
INSERT INTO tour_packages (village_id, name, description, price_per_person_idr, duration_days, duration_nights, default_quota)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id;

-- name: UpdatePackage :execrows
UPDATE tour_packages
SET name = $3, description = $4, price_per_person_idr = $5, duration_days = $6,
    duration_nights = $7, default_quota = $8, updated_at = now()
WHERE id = $1 AND village_id = $2;

-- name: SetPackageActive :execrows
UPDATE tour_packages SET is_active = $3, updated_at = now() WHERE id = $1 AND village_id = $2;

-- name: GetPackage :one
-- Always scoped by village so a manager can't reach another village's package by id.
SELECT id, village_id, name, description, price_per_person_idr, duration_days, duration_nights, default_quota, is_active, created_at, updated_at
FROM tour_packages WHERE id = $1 AND village_id = $2;

-- name: ListPackagesByVillage :many
SELECT id, village_id, name, description, price_per_person_idr, duration_days, duration_nights, default_quota, is_active, created_at, updated_at
FROM tour_packages WHERE village_id = $1 ORDER BY is_active DESC, name;

-- name: ListActivePackageCards :many
SELECT p.id, p.name, p.price_per_person_idr, p.duration_days, p.duration_nights,
       coalesce(c.path, '')::text AS cover_path, coalesce(c.caption, '')::text AS cover_caption,
       coalesce(c.width, 0)::int AS cover_width, coalesce(c.height, 0)::int AS cover_height
FROM tour_packages p
LEFT JOIN LATERAL (
    SELECT m.path, m.caption, m.width, m.height FROM media m
    WHERE m.package_id = p.id ORDER BY m.sort_order, m.id LIMIT 1
) c ON true
WHERE p.village_id = $1 AND p.is_active
ORDER BY p.price_per_person_idr, p.name;

-- name: ListPackageActivityTypeIDs :many
SELECT activity_type_id FROM package_activity_types WHERE package_id = $1;

-- name: ListPackageActivityTypes :many
SELECT a.id, a.slug, a.name
FROM activity_types a
JOIN package_activity_types pa ON pa.activity_type_id = a.id
WHERE pa.package_id = $1
ORDER BY a.name;

-- name: ClearPackageActivityTypes :exec
DELETE FROM package_activity_types WHERE package_id = $1;

-- name: AddPackageActivityTypes :exec
INSERT INTO package_activity_types (package_id, activity_type_id)
SELECT $1, unnest(@activity_type_ids::bigint[]);

-- name: CountActivityTypes :one
SELECT count(*) FROM activity_types WHERE id = ANY(@ids::bigint[]);

-- name: LockPackage :exec
SELECT 1 FROM tour_packages WHERE id = $1 FOR UPDATE;

-- name: ListPackageMedia :many
SELECT id, path, caption, width, height FROM media WHERE package_id = $1 ORDER BY sort_order, id;

-- name: CountPackageMedia :one
SELECT count(*) FROM media WHERE package_id = $1;

-- name: InsertPackageMedia :one
INSERT INTO media (package_id, path, mime, bytes, sort_order, consent_confirmed, uploaded_by, caption, width, height)
VALUES ($1, $2, $3, $4, (SELECT coalesce(max(sort_order), 0) + 1 FROM media WHERE package_id = $1), true, $5, $6, $7, $8)
RETURNING id;

-- name: DeletePackageMedia :one
DELETE FROM media WHERE id = $1 AND package_id = $2 RETURNING path;

-- name: GetPackageForBooking :one
-- Used by booking (through an adapter) inside its own transaction. FOR SHARE blocks a
-- concurrent deactivation or village status change until the booking commits.
SELECT p.id, p.village_id, p.price_per_person_idr, p.default_quota, p.is_active, v.status AS village_status
FROM tour_packages p
JOIN villages v ON v.id = p.village_id
WHERE p.id = $1
FOR SHARE OF p, v;

-- name: GetPackageSummary :one
SELECT p.id, p.village_id, p.name, p.default_quota, p.price_per_person_idr, p.is_active,
       v.name AS village_name, v.slug AS village_slug, v.status AS village_status,
       v.contact_phone AS village_phone, v.contact_whatsapp AS village_whatsapp
FROM tour_packages p
JOIN villages v ON v.id = p.village_id
WHERE p.id = $1;

-- name: ListPackageSummariesByVillage :many
SELECT p.id, p.village_id, p.name, p.default_quota, p.price_per_person_idr, p.is_active,
       v.name AS village_name, v.slug AS village_slug, v.status AS village_status,
       v.contact_phone AS village_phone, v.contact_whatsapp AS village_whatsapp
FROM tour_packages p
JOIN villages v ON v.id = p.village_id
WHERE p.village_id = $1
ORDER BY p.is_active DESC, p.name;

-- name: SearchPackages :many
-- Public search over active packages of verified villages. NULL filters are ignored.
-- Text matching: full-text (names + descriptions, GIN on search_vector) OR partial name
-- match (ILIKE, GIN trigram). Candidates come from a UNION so each branch can use its own
-- table's indexes; the query text is used directly (not via a CTE column) so the planner
-- sees a constant and can build bitmap index scans. `total` is the count before LIMIT.
WITH text_matches AS (
    SELECT p.id FROM tour_packages p
    WHERE p.is_active
      AND (p.search_vector @@ websearch_to_tsquery('simple', sqlc.narg(query)::text)
           OR p.name ILIKE sqlc.narg(pattern)::text)
    UNION
    SELECT p.id FROM tour_packages p
    WHERE p.is_active
      AND p.village_id IN (
          SELECT v.id FROM villages v
          WHERE v.search_vector @@ websearch_to_tsquery('simple', sqlc.narg(query)::text)
             OR v.name ILIKE sqlc.narg(pattern)::text)
),
results AS (
    SELECT p.id, p.name, p.price_per_person_idr, p.duration_days, p.duration_nights, p.created_at,
           v.name AS village_name, v.slug AS village_slug,
           d.name AS district_name, r.name AS regency_name,
           coalesce(c.path, '')::text AS cover_path, coalesce(c.caption, '')::text AS cover_caption,
           coalesce(c.width, 0)::int AS cover_width, coalesce(c.height, 0)::int AS cover_height,
           coalesce(2 * ts_rank(p.search_vector, websearch_to_tsquery('simple', sqlc.narg(query)::text))
                    + ts_rank(v.search_vector, websearch_to_tsquery('simple', sqlc.narg(query)::text))
                    + word_similarity(sqlc.narg(query)::text, p.name), 0)::float8 AS rank,
           count(*) OVER () AS total
    FROM tour_packages p
    JOIN villages v ON v.id = p.village_id
    JOIN regions d ON d.id = v.district_id
    JOIN regions r ON r.id = d.parent_id
    LEFT JOIN LATERAL (
        SELECT m.path, m.caption, m.width, m.height FROM media m
        WHERE m.package_id = p.id ORDER BY m.sort_order, m.id LIMIT 1
    ) c ON true
    WHERE p.is_active
      AND v.status = 'verified'
      AND (sqlc.narg(query)::text IS NULL OR p.id IN (SELECT id FROM text_matches))
      AND (sqlc.narg(regency_id)::bigint IS NULL OR r.id = sqlc.narg(regency_id)::bigint)
      AND (sqlc.narg(activity_type_id)::bigint IS NULL OR EXISTS (
            SELECT 1 FROM package_activity_types pa
            WHERE pa.package_id = p.id AND pa.activity_type_id = sqlc.narg(activity_type_id)::bigint))
      AND (sqlc.narg(min_price)::bigint IS NULL OR p.price_per_person_idr >= sqlc.narg(min_price)::bigint)
      AND (sqlc.narg(max_price)::bigint IS NULL OR p.price_per_person_idr <= sqlc.narg(max_price)::bigint)
)
SELECT id, name, price_per_person_idr, duration_days, duration_nights, village_name, village_slug,
       district_name, regency_name, cover_path, cover_caption, cover_width, cover_height, rank, total
FROM results
ORDER BY
    CASE WHEN @sort::text = 'relevance' THEN rank END DESC,
    CASE WHEN @sort::text = 'price_desc' THEN price_per_person_idr END DESC,
    CASE WHEN @sort::text = 'newest' THEN created_at END DESC,
    price_per_person_idr, id
LIMIT @page_size OFFSET @page_offset;
