-- Development seed. Idempotent. Region rows are placeholders only:
-- the launch region has not been decided (see docs/SPEC.md, "Keputusan terbuka").

INSERT INTO activity_types (slug, name) VALUES
    ('alam',      'Wisata alam'),
    ('budaya',    'Seni dan budaya'),
    ('kuliner',   'Kuliner'),
    ('edukasi',   'Edukasi'),
    ('pertanian', 'Pertanian dan perkebunan'),
    ('kerajinan', 'Kerajinan')
ON CONFLICT (slug) DO NOTHING;

INSERT INTO regions (parent_id, kind, name)
SELECT NULL, 'province', 'Provinsi Contoh'
WHERE NOT EXISTS (SELECT 1 FROM regions WHERE parent_id IS NULL AND name = 'Provinsi Contoh');

INSERT INTO regions (parent_id, kind, name)
SELECT p.id, 'regency', r.name
FROM regions p
CROSS JOIN (VALUES ('Kabupaten Contoh A'), ('Kabupaten Contoh B')) AS r (name)
WHERE p.parent_id IS NULL AND p.name = 'Provinsi Contoh'
ON CONFLICT DO NOTHING;

INSERT INTO regions (parent_id, kind, name)
SELECT k.id, 'district', k.name || ' — Kecamatan ' || d.n
FROM regions k
CROSS JOIN (VALUES ('1'), ('2')) AS d (n)
WHERE k.kind = 'regency' AND k.name IN ('Kabupaten Contoh A', 'Kabupaten Contoh B')
ON CONFLICT DO NOTHING;
