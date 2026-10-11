-- Manager statistics (SPEC §16) count requests per village by creation time.
CREATE INDEX bookings_village_created_idx ON bookings (village_id, created_at);
