-- Owned by internal/platform/mail.

-- name: EnqueueEmail :exec
INSERT INTO email_outbox (kind, dedupe_key, to_address, subject, body)
VALUES (@kind, @dedupe_key, @to_address, @subject, @body)
ON CONFLICT (dedupe_key) DO NOTHING;

-- name: ClaimEmails :many
-- Claims due rows for one worker: pending ones that are due, and 'sending' ones whose lock
-- expired (a worker crashed mid-send). SKIP LOCKED keeps concurrent workers disjoint.
WITH due AS (
    SELECT id FROM email_outbox
    WHERE (status = 'pending' AND next_attempt_at <= @now::timestamptz)
       OR (status = 'sending' AND locked_until < @now::timestamptz)
    ORDER BY next_attempt_at, id
    LIMIT @batch_size
    FOR UPDATE SKIP LOCKED
)
UPDATE email_outbox o
SET status = 'sending', locked_until = @locked_until::timestamptz, attempts = o.attempts + 1
FROM due
WHERE o.id = due.id
RETURNING o.id, o.kind, o.to_address, o.subject, o.body, o.attempts;

-- name: MarkEmailSent :exec
UPDATE email_outbox
SET status = 'sent', sent_at = @now::timestamptz, locked_until = NULL, last_error = ''
WHERE id = @id AND status = 'sending';

-- name: MarkEmailRetry :exec
UPDATE email_outbox
SET status = CASE WHEN @give_up::boolean THEN 'failed' ELSE 'pending' END,
    next_attempt_at = @next_attempt_at::timestamptz,
    locked_until = NULL,
    last_error = @last_error
WHERE id = @id AND status = 'sending';

-- name: PurgeSentEmails :execrows
-- Sent messages contain personal data; keep them only as long as useful for support.
DELETE FROM email_outbox WHERE status = 'sent' AND sent_at < @before::timestamptz;

-- name: PurgeUnsentEmailsTo :exec
DELETE FROM email_outbox WHERE to_address = @to_address AND status IN ('pending', 'sending');

-- name: CountEmailsByStatus :many
SELECT status, count(*) AS total FROM email_outbox GROUP BY status;

-- Recipient lookups used by the notification adapters (inside the business transaction).

-- name: VillageManagerEmails :many
SELECT u.email, u.name FROM village_members m JOIN users u ON u.id = m.user_id
WHERE m.village_id = $1 ORDER BY u.id;

-- name: BookingNotificationData :one
SELECT b.id, b.village_id, b.start_date, b.party_size, b.total_price_idr, b.contact_name,
       b.decision_note, b.expires_at, coalesce(u.email, '')::text AS traveler_email,
       p.name AS package_name, v.name AS village_name
FROM bookings b
JOIN tour_packages p ON p.id = b.package_id
JOIN villages v ON v.id = b.village_id
LEFT JOIN users u ON u.id = b.traveler_id
WHERE b.id = $1;

-- name: VillageNotificationData :one
SELECT v.id, v.name, v.slug, v.review_note FROM villages v WHERE v.id = $1;
