DROP INDEX IF EXISTS tour_packages_active_village_idx;
DROP INDEX IF EXISTS villages_name_trgm_idx;
DROP INDEX IF EXISTS tour_packages_name_trgm_idx;
DROP INDEX IF EXISTS villages_search_idx;
DROP INDEX IF EXISTS tour_packages_search_idx;
ALTER TABLE villages DROP COLUMN IF EXISTS search_vector;
ALTER TABLE tour_packages DROP COLUMN IF EXISTS search_vector;
DROP EXTENSION IF EXISTS pg_trgm;
