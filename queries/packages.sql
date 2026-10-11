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
SELECT * FROM tour_packages WHERE id = $1 AND village_id = $2;

-- name: ListPackagesByVillage :many
SELECT * FROM tour_packages WHERE village_id = $1 ORDER BY is_active DESC, name;

-- name: ListActivePackageCards :many
SELECT p.id, p.name, p.price_per_person_idr, p.duration_days, p.duration_nights,
       coalesce((SELECT m.path FROM media m WHERE m.package_id = p.id ORDER BY m.sort_order, m.id LIMIT 1), '')::text AS cover_path
FROM tour_packages p
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
SELECT id, path FROM media WHERE package_id = $1 ORDER BY sort_order, id;

-- name: CountPackageMedia :one
SELECT count(*) FROM media WHERE package_id = $1;

-- name: InsertPackageMedia :one
INSERT INTO media (package_id, path, mime, bytes, sort_order, consent_confirmed, uploaded_by)
VALUES ($1, $2, $3, $4, (SELECT coalesce(max(sort_order), 0) + 1 FROM media WHERE package_id = $1), true, $5)
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
