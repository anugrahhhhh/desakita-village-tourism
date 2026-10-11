-- Transactional outbox for email (SPEC §13). Rows are inserted in the same transaction as
-- the business change; a background worker delivers them at least once.
CREATE TABLE email_outbox (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind            text        NOT NULL CHECK (length(kind) BETWEEN 1 AND 60),
    -- One message per business event and recipient, even if the event is retried.
    dedupe_key      text        NOT NULL UNIQUE,
    to_address      citext      NOT NULL CHECK (to_address ~ '^[^\s@]+@[^\s@]+$'),
    subject         text        NOT NULL CHECK (length(subject) BETWEEN 1 AND 200 AND subject !~ '[\r\n]'),
    body            text        NOT NULL CHECK (length(body) BETWEEN 1 AND 20000),
    status          text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sending', 'sent', 'failed')),
    attempts        integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    locked_until    timestamptz,
    last_error      text        NOT NULL DEFAULT '' CHECK (length(last_error) <= 500),
    created_at      timestamptz NOT NULL DEFAULT now(),
    sent_at         timestamptz,
    CHECK ((status = 'sent') = (sent_at IS NOT NULL))
);
-- The worker's claim query: due pending rows, plus 'sending' rows whose lock expired.
CREATE INDEX email_outbox_due_idx ON email_outbox (next_attempt_at, id) WHERE status = 'pending';
CREATE INDEX email_outbox_stale_idx ON email_outbox (locked_until) WHERE status = 'sending';
CREATE INDEX email_outbox_sent_idx ON email_outbox (sent_at) WHERE status = 'sent';
CREATE INDEX email_outbox_to_idx ON email_outbox (to_address) WHERE status IN ('pending', 'sending');
