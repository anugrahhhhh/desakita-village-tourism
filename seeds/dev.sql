-- Seed data. Idempotent. Regions ship with migration 000009 (SPEC §17); demo villages and
-- packages come from `desakita seed-demo` (development only).

INSERT INTO activity_types (slug, name) VALUES
    ('alam',      'Wisata alam'),
    ('budaya',    'Seni dan budaya'),
    ('kuliner',   'Kuliner'),
    ('edukasi',   'Edukasi'),
    ('pertanian', 'Pertanian dan perkebunan'),
    ('kerajinan', 'Kerajinan')
ON CONFLICT (slug) DO NOTHING;
