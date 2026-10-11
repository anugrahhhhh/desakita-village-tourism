-- name: CreateUser :one
INSERT INTO users (email, password_hash, name, phone)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: LockMembershipsOfUserVillages :exec
-- Locks every membership row of every village the user manages, so two co-managers
-- deleting their accounts concurrently cannot both pass the "last manager" check.
SELECT 1 FROM village_members
WHERE village_id IN (SELECT m.village_id FROM village_members m WHERE m.user_id = $1)
ORDER BY village_id, user_id
FOR UPDATE;

-- name: CountVillagesSolelyManagedBy :one
SELECT count(*) FROM village_members vm
WHERE vm.user_id = $1
  AND NOT EXISTS (
      SELECT 1 FROM village_members o
      WHERE o.village_id = vm.village_id AND o.user_id <> vm.user_id
  );

-- name: AnonymizeTravelerBookings :exec
UPDATE bookings
SET note = '', contact_name = 'Akun dihapus', contact_phone = '', updated_at = now()
WHERE traveler_id = $1;

-- name: DeleteUser :exec
-- bookings.traveler_id and reports.reporter_id are cleared by ON DELETE SET NULL.
DELETE FROM users WHERE id = $1;

