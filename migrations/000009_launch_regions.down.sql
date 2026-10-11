-- Removes the launch regions. Fails (ON DELETE RESTRICT) while a village still uses one,
-- which is the intended protection.
DELETE FROM regions
WHERE kind = 'district' AND parent_id IN (
    SELECT r.id FROM regions r JOIN regions p ON p.id = r.parent_id
    WHERE p.parent_id IS NULL AND p.name IN ('Jawa Barat', 'Daerah Istimewa Yogyakarta', 'Jawa Timur', 'DKI Jakarta'));
DELETE FROM regions
WHERE kind = 'regency' AND parent_id IN (
    SELECT id FROM regions WHERE parent_id IS NULL AND name IN ('Jawa Barat', 'Daerah Istimewa Yogyakarta', 'Jawa Timur', 'DKI Jakarta'));
DELETE FROM regions
WHERE parent_id IS NULL AND name IN ('Jawa Barat', 'Daerah Istimewa Yogyakarta', 'Jawa Timur', 'DKI Jakarta');
