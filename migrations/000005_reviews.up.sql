-- Reviews (SPEC §12): one per completed booking, published immediately, moderated after
-- publication (reports + admin hide/restore, audited).
CREATE TABLE reviews (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    booking_id      bigint      NOT NULL UNIQUE REFERENCES bookings (id) ON DELETE CASCADE,
    package_id      bigint      NOT NULL REFERENCES tour_packages (id) ON DELETE CASCADE,
    village_id      bigint      NOT NULL REFERENCES villages (id) ON DELETE CASCADE,
    -- SET NULL keeps the review when the author deletes their account (shown anonymised).
    author_id       bigint      REFERENCES users (id) ON DELETE SET NULL,
    rating          smallint    NOT NULL CHECK (rating BETWEEN 1 AND 5),
    body            text        NOT NULL CHECK (length(body) BETWEEN 10 AND 2000),
    status          text        NOT NULL DEFAULT 'published' CHECK (status IN ('published', 'hidden')),
    moderation_note text        NOT NULL DEFAULT '' CHECK (length(moderation_note) <= 1000),
    moderated_by    bigint      REFERENCES users (id) ON DELETE SET NULL,
    moderated_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'hidden') = (moderated_at IS NOT NULL AND moderation_note <> ''))
);
CREATE INDEX reviews_package_published_idx ON reviews (package_id, created_at DESC) WHERE status = 'published';
CREATE INDEX reviews_status_created_idx ON reviews (status, created_at DESC);
CREATE INDEX reviews_author_idx ON reviews (author_id);

-- Users can report reviews too.
ALTER TABLE reports DROP CONSTRAINT reports_target_type_check;
ALTER TABLE reports ADD CONSTRAINT reports_target_type_check
    CHECK (target_type IN ('village', 'package', 'review'));

-- Audited moderation actions.
ALTER TABLE audit_log DROP CONSTRAINT audit_log_action_check;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_action_check CHECK (action IN (
    'village.verify', 'village.reject', 'village.suspend', 'village.reinstate',
    'report.resolve', 'report.dismiss', 'user.promote_admin',
    'review.hide', 'review.restore'));
ALTER TABLE audit_log DROP CONSTRAINT audit_log_target_type_check;
ALTER TABLE audit_log ADD CONSTRAINT audit_log_target_type_check
    CHECK (target_type IN ('village', 'report', 'user', 'review'));
