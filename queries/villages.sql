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
INSERT INTO villages (slug, name, description, district_id, address, contact_phone, contact_whatsapp)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id;

-- name: AddVillageMember :exec
INSERT INTO village_members (village_id, user_id, role) VALUES ($1, $2, 'owner');

-- name: UpdateVillageProfile :execrows
UPDATE villages
SET name = $2, description = $3, district_id = $4, address = $5,
    contact_phone = $6, contact_whatsapp = $7, updated_at = now()
WHERE id = $1;

-- name: GetVillage :one
SELECT v.*, d.name AS district_name, r.name AS regency_name
FROM villages v
JOIN regions d ON d.id = v.district_id
JOIN regions r ON r.id = d.parent_id
WHERE v.id = $1;

-- name: GetVillageBySlug :one
SELECT v.*, d.name AS district_name, r.name AS regency_name
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
SELECT id, path FROM media WHERE village_id = $1 ORDER BY sort_order, id;

-- name: CountVillageMedia :one
SELECT count(*) FROM media WHERE village_id = $1;

-- name: InsertVillageMedia :one
INSERT INTO media (village_id, path, mime, bytes, sort_order, consent_confirmed, uploaded_by)
VALUES ($1, $2, $3, $4, (SELECT coalesce(max(sort_order), 0) + 1 FROM media WHERE village_id = $1), true, $5)
RETURNING id;

-- name: DeleteVillageMedia :one
DELETE FROM media WHERE id = $1 AND village_id = $2 RETURNING path;
