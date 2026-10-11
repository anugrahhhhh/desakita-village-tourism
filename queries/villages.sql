-- name: ListDistricts :many
SELECT d.id, d.name AS district_name, r.name AS regency_name, p.name AS province_name
FROM regions d
JOIN regions r ON r.id = d.parent_id
JOIN regions p ON p.id = r.parent_id
WHERE d.kind = 'district'
ORDER BY p.name, r.name, d.name;

-- name: DistrictExists :one
SELECT EXISTS (SELECT 1 FROM regions WHERE id = $1 AND kind = 'district');

-- name: VillageSlugExists :one
SELECT EXISTS (SELECT 1 FROM villages WHERE slug = $1);

-- name: CreateVillage :one
INSERT INTO villages (slug, name, description, district_id, address, contact_phone, contact_whatsapp, lat, lng)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id;

-- name: AddVillageMember :exec
INSERT INTO village_members (village_id, user_id, role) VALUES ($1, $2, 'owner');

-- name: UpdateVillageProfile :execrows
UPDATE villages
SET name = $2, description = $3, district_id = $4, address = $5,
    contact_phone = $6, contact_whatsapp = $7, lat = $8, lng = $9, updated_at = now()
WHERE id = $1;

-- name: GetVillage :one
SELECT v.id, v.slug, v.name, v.description, v.district_id, v.address, v.lat, v.lng, v.contact_phone, v.contact_whatsapp,
       v.status, v.review_note, v.verified_by, v.verified_at, v.created_at, v.updated_at,
       d.name AS district_name, r.name AS regency_name
FROM villages v
JOIN regions d ON d.id = v.district_id
JOIN regions r ON r.id = d.parent_id
WHERE v.id = $1;

-- name: GetVillageBySlug :one
SELECT v.id, v.slug, v.name, v.description, v.district_id, v.address, v.lat, v.lng, v.contact_phone, v.contact_whatsapp,
       v.status, v.review_note, v.verified_by, v.verified_at, v.created_at, v.updated_at,
       d.name AS district_name, r.name AS regency_name
FROM villages v
JOIN regions d ON d.id = v.district_id
JOIN regions r ON r.id = d.parent_id
WHERE v.slug = $1;

-- name: IsVillageManager :one
SELECT EXISTS (SELECT 1 FROM village_members WHERE village_id = $1 AND user_id = $2);

-- name: ListVillagesForUser :many
SELECT v.id, v.slug, v.name, v.status, v.review_note
FROM villages v
JOIN village_members m ON m.village_id = v.id
WHERE m.user_id = $1
ORDER BY v.name;

-- name: LockVillage :exec
SELECT 1 FROM villages WHERE id = $1 FOR UPDATE;

-- name: ListVillageMedia :many
SELECT id, path, caption, width, height FROM media WHERE village_id = $1 ORDER BY sort_order, id;

-- name: CountVillageMedia :one
SELECT count(*) FROM media WHERE village_id = $1;

-- name: InsertVillageMedia :one
INSERT INTO media (village_id, path, mime, bytes, sort_order, consent_confirmed, uploaded_by, caption, width, height)
VALUES ($1, $2, $3, $4, (SELECT coalesce(max(sort_order), 0) + 1 FROM media WHERE village_id = $1), true, $5, $6, $7, $8)
RETURNING id;

-- name: DeleteVillageMedia :one
DELETE FROM media WHERE id = $1 AND village_id = $2 RETURNING path;

-- name: SetVillageStatus :execrows
-- Guarded by the allowed source statuses so concurrent admin actions can't skip a step.
UPDATE villages
SET status = @to_status::text,
    review_note = @review_note,
    verified_by = CASE WHEN @to_status::text = 'verified' THEN sqlc.narg(admin_id)::bigint ELSE verified_by END,
    verified_at = CASE WHEN @to_status::text = 'verified' THEN now() ELSE verified_at END,
    updated_at = now()
WHERE id = @id AND status = ANY(@from_statuses::text[]);

-- name: ListVillagesByStatus :many
SELECT v.id, v.slug, v.name, v.status, v.created_at, d.name AS district_name, r.name AS regency_name
FROM villages v
JOIN regions d ON d.id = v.district_id
JOIN regions r ON r.id = d.parent_id
WHERE v.status = $1
ORDER BY v.created_at;

-- name: ListVillageManagers :many
SELECT u.id, u.name, u.email, u.phone
FROM village_members m
JOIN users u ON u.id = m.user_id
WHERE m.village_id = $1
ORDER BY u.name;

-- name: ListRegencies :many
SELECT r.id, r.name, p.name AS province_name
FROM regions r
JOIN regions p ON p.id = r.parent_id
WHERE r.kind = 'regency'
ORDER BY p.name, r.name;

-- name: LockVillageStatus :one
-- Read the current status under a row lock so the audit entry records the real transition.
SELECT status FROM villages WHERE id = $1 FOR UPDATE;

-- name: CountVillagesByStatus :many
SELECT status, count(*) AS total FROM villages GROUP BY status;

-- name: ListMappedVillages :many
-- Verified villages with a location, for the public map (SPEC §14).
SELECT v.slug, v.name, v.lat, v.lng, d.name AS district_name, r.name AS regency_name
FROM villages v
JOIN regions d ON d.id = v.district_id
JOIN regions r ON r.id = d.parent_id
WHERE v.status = 'verified' AND v.lat IS NOT NULL
ORDER BY v.name
LIMIT 5000;
