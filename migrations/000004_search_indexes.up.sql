-- Indexed search (SPEC §11): full-text on names + descriptions, trigram for partial names,
-- and a partial index for joining active packages. pg_trgm is a trusted extension (PG >= 13).
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- 'simple' config: no stemming (PostgreSQL ships no Indonesian dictionary), lowercase only.
ALTER TABLE tour_packages ADD COLUMN search_vector tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', coalesce(name, '')), 'A') ||
    setweight(to_tsvector('simple', coalesce(description, '')), 'B')
) STORED;

ALTER TABLE villages ADD COLUMN search_vector tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', coalesce(name, '')), 'A') ||
    setweight(to_tsvector('simple', coalesce(description, '')), 'C')
) STORED;

CREATE INDEX tour_packages_search_idx ON tour_packages USING gin (search_vector);
CREATE INDEX villages_search_idx ON villages USING gin (search_vector);
CREATE INDEX tour_packages_name_trgm_idx ON tour_packages USING gin (name gin_trgm_ops);
CREATE INDEX villages_name_trgm_idx ON villages USING gin (name gin_trgm_ops);

-- Public search only ever reads active packages, reached from verified villages.
CREATE INDEX tour_packages_active_village_idx ON tour_packages (village_id) WHERE is_active;
