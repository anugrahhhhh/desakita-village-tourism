-- Optional alt text for photos, and stored dimensions so <img> can carry width/height
-- (prevents layout shift). Existing rows keep NULL dimensions; templates fall back to 4:3.
ALTER TABLE media
    ADD COLUMN caption text NOT NULL DEFAULT '' CHECK (length(caption) <= 200),
    ADD COLUMN width   integer CHECK (width > 0),
    ADD COLUMN height  integer CHECK (height > 0);
