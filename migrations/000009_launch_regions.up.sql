-- Launch regions (SPEC §17): Bandung, Yogyakarta, Surabaya and Jakarta, with the districts
-- (kecamatan) where tourism villages and kampung wisata are found. Real administrative
-- data, so it ships with the schema; more districts can be added by later migrations.
-- Idempotent through regions_parent_name_uniq.

WITH data (province, regency, district) AS (VALUES
    ('Jawa Barat', 'Kota Bandung', 'Coblong'),
    ('Jawa Barat', 'Kota Bandung', 'Sukajadi'),
    ('Jawa Barat', 'Kota Bandung', 'Cidadap'),
    ('Jawa Barat', 'Kota Bandung', 'Cibeunying Kaler'),
    ('Jawa Barat', 'Kota Bandung', 'Sumur Bandung'),
    ('Jawa Barat', 'Kabupaten Bandung Barat', 'Lembang'),
    ('Jawa Barat', 'Kabupaten Bandung Barat', 'Parongpong'),
    ('Jawa Barat', 'Kabupaten Bandung Barat', 'Cisarua'),
    ('Jawa Barat', 'Kabupaten Bandung', 'Ciwidey'),
    ('Jawa Barat', 'Kabupaten Bandung', 'Rancabali'),
    ('Jawa Barat', 'Kabupaten Bandung', 'Pasirjambu'),
    ('Jawa Barat', 'Kabupaten Bandung', 'Pangalengan'),
    ('Daerah Istimewa Yogyakarta', 'Kota Yogyakarta', 'Kraton'),
    ('Daerah Istimewa Yogyakarta', 'Kota Yogyakarta', 'Gondomanan'),
    ('Daerah Istimewa Yogyakarta', 'Kota Yogyakarta', 'Kotagede'),
    ('Daerah Istimewa Yogyakarta', 'Kota Yogyakarta', 'Mergangsan'),
    ('Daerah Istimewa Yogyakarta', 'Kabupaten Sleman', 'Cangkringan'),
    ('Daerah Istimewa Yogyakarta', 'Kabupaten Sleman', 'Pakem'),
    ('Daerah Istimewa Yogyakarta', 'Kabupaten Sleman', 'Turi'),
    ('Daerah Istimewa Yogyakarta', 'Kabupaten Sleman', 'Prambanan'),
    ('Daerah Istimewa Yogyakarta', 'Kabupaten Bantul', 'Imogiri'),
    ('Daerah Istimewa Yogyakarta', 'Kabupaten Bantul', 'Kasihan'),
    ('Daerah Istimewa Yogyakarta', 'Kabupaten Bantul', 'Pundong'),
    ('Daerah Istimewa Yogyakarta', 'Kabupaten Gunungkidul', 'Patuk'),
    ('Daerah Istimewa Yogyakarta', 'Kabupaten Gunungkidul', 'Tanjungsari'),
    ('Jawa Timur', 'Kota Surabaya', 'Genteng'),
    ('Jawa Timur', 'Kota Surabaya', 'Bubutan'),
    ('Jawa Timur', 'Kota Surabaya', 'Pabean Cantikan'),
    ('Jawa Timur', 'Kota Surabaya', 'Semampir'),
    ('Jawa Timur', 'Kota Surabaya', 'Bulak'),
    ('Jawa Timur', 'Kota Surabaya', 'Sukolilo'),
    ('Jawa Timur', 'Kota Surabaya', 'Wonokromo'),
    ('DKI Jakarta', 'Kota Administrasi Jakarta Selatan', 'Jagakarsa'),
    ('DKI Jakarta', 'Kota Administrasi Jakarta Selatan', 'Cilandak'),
    ('DKI Jakarta', 'Kota Administrasi Jakarta Pusat', 'Menteng'),
    ('DKI Jakarta', 'Kota Administrasi Jakarta Barat', 'Taman Sari'),
    ('DKI Jakarta', 'Kota Administrasi Jakarta Utara', 'Penjaringan'),
    ('DKI Jakarta', 'Kota Administrasi Jakarta Timur', 'Cipayung'),
    ('DKI Jakarta', 'Kabupaten Administrasi Kepulauan Seribu', 'Kepulauan Seribu Selatan'),
    ('DKI Jakarta', 'Kabupaten Administrasi Kepulauan Seribu', 'Kepulauan Seribu Utara')
),
provinces AS (
    INSERT INTO regions (parent_id, kind, name)
    SELECT DISTINCT NULL::bigint, 'province', province FROM data
    ON CONFLICT DO NOTHING
    RETURNING id, name
),
all_provinces AS (
    SELECT id, name FROM provinces
    UNION
    SELECT id, name FROM regions WHERE kind = 'province' AND name IN (SELECT province FROM data)
),
regencies AS (
    INSERT INTO regions (parent_id, kind, name)
    SELECT DISTINCT p.id, 'regency', d.regency FROM data d JOIN all_provinces p ON p.name = d.province
    ON CONFLICT DO NOTHING
    RETURNING id, parent_id, name
),
all_regencies AS (
    SELECT r.id, p.name AS province, r.name FROM regencies r JOIN all_provinces p ON p.id = r.parent_id
    UNION
    SELECT r.id, p.name, r.name FROM regions r JOIN all_provinces p ON p.id = r.parent_id WHERE r.kind = 'regency'
)
INSERT INTO regions (parent_id, kind, name)
SELECT r.id, 'district', d.district
FROM data d JOIN all_regencies r ON r.province = d.province AND r.name = d.regency
ON CONFLICT DO NOTHING;
