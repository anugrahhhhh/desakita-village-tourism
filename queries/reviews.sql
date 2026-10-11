-- Owned by internal/review.

-- name: InsertReview :one
INSERT INTO reviews (booking_id, package_id, village_id, author_id, rating, body)
VALUES (@booking_id, @package_id, @village_id, @author_id, @rating, @body)
RETURNING id;

-- name: GetReviewByBooking :one
SELECT id, booking_id, package_id, village_id, author_id, rating, body, status, moderation_note, created_at
FROM reviews WHERE booking_id = $1;

-- name: DeleteOwnReview :execrows
DELETE FROM reviews WHERE booking_id = @booking_id AND author_id = @author_id;

-- name: ListPublishedReviewsForPackage :many
-- Author shown as first name + initial; NULL author (deleted account) handled by the caller.
SELECT rv.id, rv.rating, rv.body, rv.created_at, u.name AS author_name
FROM reviews rv
LEFT JOIN users u ON u.id = rv.author_id
WHERE rv.package_id = @package_id AND rv.status = 'published'
ORDER BY rv.created_at DESC, rv.id DESC
LIMIT @page_size OFFSET @page_offset;

-- name: PackageRatingDistribution :many
SELECT rating, count(*) AS total
FROM reviews
WHERE package_id = $1 AND status = 'published'
GROUP BY rating;

-- name: ListReviewsForModeration :many
SELECT rv.id, rv.rating, rv.body, rv.status, rv.moderation_note, rv.moderated_at, rv.created_at,
       rv.package_id, rv.village_id,
       p.name AS package_name, v.name AS village_name, v.slug AS village_slug,
       coalesce(u.email, '')::text AS author_email,
       (SELECT count(*) FROM reports r
        WHERE r.target_type = 'review' AND r.target_id = rv.id AND r.status = 'open') AS open_reports
FROM reviews rv
JOIN tour_packages p ON p.id = rv.package_id
JOIN villages v ON v.id = rv.village_id
LEFT JOIN users u ON u.id = rv.author_id
WHERE rv.status = @status::text
  AND (NOT @reported_only::boolean OR EXISTS (
        SELECT 1 FROM reports r WHERE r.target_type = 'review' AND r.target_id = rv.id AND r.status = 'open'))
ORDER BY rv.created_at DESC, rv.id DESC
LIMIT 200;

-- name: LockReviewStatus :one
SELECT status FROM reviews WHERE id = $1 FOR UPDATE;

-- name: SetReviewStatus :execrows
UPDATE reviews
SET status = @to_status::text,
    moderation_note = @moderation_note,
    moderated_by = sqlc.narg(admin_id)::bigint,
    moderated_at = CASE WHEN @to_status::text = 'hidden' THEN now() ELSE NULL END
WHERE id = @id AND status = @from_status::text;

-- name: CountReviewsByStatus :many
SELECT status, count(*) AS total FROM reviews GROUP BY status;
