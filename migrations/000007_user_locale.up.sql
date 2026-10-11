-- Interface language per account (SPEC §15): used for pages when no cookie is set and for
-- every email sent to the account.
ALTER TABLE users ADD COLUMN locale text NOT NULL DEFAULT 'id' CHECK (locale IN ('id', 'en'));
