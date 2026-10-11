-- Owned by internal/booking. All quota changes are conditional UPDATEs inside the caller's
-- transaction; see docs/SPEC.md §4.

-- name: LockTravelerPending :exec
-- Serializes booking creation per traveler so the pending-limit count can't race.
SELECT pg_advisory_xact_lock(hashtextextended(@lock_name::text, 0));

-- name: CountPendingForTraveler :one
SELECT count(*) FROM bookings WHERE traveler_id = $1 AND status = 'pending';

-- name: EnsureAvailability :exec
INSERT INTO availability (package_id, date, quota)
VALUES ($1, $2, $3)
ON CONFLICT (package_id, date) DO NOTHING;

-- name: ReserveQuota :one
UPDATE availability
SET reserved = reserved + @party_size::int, updated_at = now()
WHERE package_id = @package_id AND date = @date
  AND NOT is_closed
  AND reserved + @party_size::int <= quota
RETURNING reserved, quota;

-- name: ReleaseQuota :execrows
UPDATE availability
SET reserved = reserved - @party_size::int, updated_at = now()
WHERE package_id = @package_id AND date = @date;

-- name: GetAvailability :one
SELECT * FROM availability WHERE package_id = $1 AND date = $2;

-- name: SetQuota :one
UPDATE availability
SET quota = @quota::int, updated_at = now()
WHERE package_id = @package_id AND date = @date AND reserved <= @quota::int
RETURNING *;

-- name: SetClosed :exec
-- Closing only blocks new reservations; existing bookings are untouched.
UPDATE availability SET is_closed = @is_closed, updated_at = now()
WHERE package_id = @package_id AND date = @date;

-- name: ListAvailability :many
SELECT * FROM availability
WHERE package_id = @package_id AND date BETWEEN @from_date::date AND @to_date::date
ORDER BY date;

-- name: InsertBooking :one
INSERT INTO bookings (package_id, village_id, traveler_id, start_date, party_size, note,
                      total_price_idr, contact_name, contact_phone, expires_at, created_at, updated_at)
VALUES (@package_id, @village_id, @traveler_id, @start_date, @party_size, @note,
        @total_price_idr, @contact_name, @contact_phone, @expires_at, @now::timestamptz, @now::timestamptz)
RETURNING *;

-- name: GetBooking :one
SELECT * FROM bookings WHERE id = $1;

-- name: ConfirmBooking :one
UPDATE bookings
SET status = 'confirmed', decided_at = @now::timestamptz, updated_at = @now::timestamptz
WHERE id = @id AND status = 'pending' AND expires_at > @now::timestamptz
RETURNING *;

-- name: RejectBooking :one
UPDATE bookings
SET status = 'rejected', decided_at = @now::timestamptz, decision_note = @decision_note, updated_at = @now::timestamptz
WHERE id = @id AND status = 'pending'
RETURNING *;

-- name: CancelBooking :one
UPDATE bookings
SET status = 'cancelled', cancelled_at = @now::timestamptz, cancelled_by = @cancelled_by::text,
    decision_note = @decision_note, updated_at = @now::timestamptz
WHERE id = @id AND status IN ('pending', 'confirmed') AND start_date >= @today::date
RETURNING *;

-- name: CompleteBooking :one
UPDATE bookings
SET status = 'completed', completed_at = @now::timestamptz, updated_at = @now::timestamptz
WHERE id = @id AND status = 'confirmed' AND start_date <= @today::date
RETURNING *;

-- name: ExpireDueBookings :many
-- SKIP LOCKED: a booking being rejected/confirmed right now is left to that transaction.
WITH due AS (
    SELECT id FROM bookings
    WHERE status = 'pending' AND expires_at <= @now::timestamptz
    ORDER BY expires_at
    LIMIT @batch_size
    FOR UPDATE SKIP LOCKED
)
UPDATE bookings b
SET status = 'expired', updated_at = @now::timestamptz
FROM due
WHERE b.id = due.id
RETURNING b.*;

-- name: ListBookingsForTraveler :many
SELECT * FROM bookings WHERE traveler_id = $1 ORDER BY start_date DESC, id DESC;

-- name: ListBookingsForVillage :many
SELECT * FROM bookings WHERE village_id = $1 ORDER BY start_date, id;

-- name: GetCompletedBookingForReview :one
-- Used by the review module (through an adapter) inside its transaction. FOR SHARE keeps
-- the booking from changing while the review is written.
SELECT id, package_id, village_id FROM bookings
WHERE id = @id AND traveler_id = @traveler_id AND status = 'completed'
FOR SHARE;
