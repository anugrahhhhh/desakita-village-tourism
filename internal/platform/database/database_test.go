package database_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/db"
	"desakita/internal/platform/database"
	"desakita/internal/platform/dbtest"
)

func TestMigrateUpDownUp(t *testing.T) {
	url := dbtest.NewURL(t, false)
	if err := database.MigrateUp(url); err != nil {
		t.Fatalf("up: %v", err)
	}
	if err := database.MigrateDown(url, 0); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := database.MigrateUp(url); err != nil {
		t.Fatalf("up again: %v", err)
	}

	pool, err := database.Open(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	// Seed must be idempotent.
	for range 2 {
		if err := database.Seed(context.Background(), pool); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM activity_types").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Errorf("activity_types = %d after two seeds, want 6", n)
	}
}

func TestSeedWithGeneratedQueries(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()
	if err := database.Seed(ctx, pool); err != nil {
		t.Fatal(err)
	}
	q := db.New(pool)

	provinces, err := q.ListRegionChildren(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(provinces) != 1 || provinces[0].Name != "Provinsi Contoh" {
		t.Fatalf("provinces = %+v, want only the placeholder province", provinces)
	}
	regencies, err := q.ListRegionChildren(ctx, &provinces[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(regencies) != 2 {
		t.Errorf("regencies = %d, want 2", len(regencies))
	}
	types, err := q.ListActivityTypes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 6 {
		t.Errorf("activity types = %d, want 6", len(types))
	}
}

// fixture inserts one user, district, village and package and returns their ids.
type fixture struct{ userID, districtID, villageID, packageID int64 }

func newFixture(t *testing.T, pool *pgxpool.Pool) fixture {
	t.Helper()
	ctx := context.Background()
	var f fixture
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, name) VALUES ('a@example.com', 'x', 'A') RETURNING id`).Scan(&f.userID))
	var prov, reg int64
	must(pool.QueryRow(ctx, `INSERT INTO regions (kind, name) VALUES ('province', 'P') RETURNING id`).Scan(&prov))
	must(pool.QueryRow(ctx, `INSERT INTO regions (parent_id, kind, name) VALUES ($1, 'regency', 'R') RETURNING id`, prov).Scan(&reg))
	must(pool.QueryRow(ctx, `INSERT INTO regions (parent_id, kind, name) VALUES ($1, 'district', 'D') RETURNING id`, reg).Scan(&f.districtID))
	must(pool.QueryRow(ctx, `INSERT INTO villages (slug, name, district_id, contact_phone) VALUES ('desa-a', 'Desa A', $1, '0811') RETURNING id`, f.districtID).Scan(&f.villageID))
	must(pool.QueryRow(ctx, `INSERT INTO tour_packages (village_id, name, price_per_person_idr, duration_days, default_quota) VALUES ($1, 'Paket', 100000, 1, 10) RETURNING id`, f.villageID).Scan(&f.packageID))
	return f
}

func wantConstraintViolation(t *testing.T, err error, label string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || (pgErr.Code[:2] != "23") {
		t.Errorf("%s: want integrity constraint violation, got %v", label, err)
	}
}

func TestSchemaConstraints(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()
	f := newFixture(t, pool)

	t.Run("email is case-insensitive unique", func(t *testing.T) {
		_, err := pool.Exec(ctx, `INSERT INTO users (email, password_hash, name) VALUES ('A@Example.COM', 'x', 'B')`)
		wantConstraintViolation(t, err, "duplicate email")
	})

	t.Run("media needs exactly one target", func(t *testing.T) {
		ins := `INSERT INTO media (village_id, package_id, path, mime, bytes, consent_confirmed) VALUES ($1, $2, $3, 'image/jpeg', 10, true)`
		_, err := pool.Exec(ctx, ins, nil, nil, "none.jpg")
		wantConstraintViolation(t, err, "no target")
		_, err = pool.Exec(ctx, ins, f.villageID, f.packageID, "both.jpg")
		wantConstraintViolation(t, err, "two targets")
		if _, err := pool.Exec(ctx, ins, f.villageID, nil, "village.jpg"); err != nil {
			t.Errorf("village target: %v", err)
		}
		if _, err := pool.Exec(ctx, ins, nil, f.packageID, "package.jpg"); err != nil {
			t.Errorf("package target: %v", err)
		}
	})

	t.Run("media requires consent", func(t *testing.T) {
		_, err := pool.Exec(ctx, `INSERT INTO media (village_id, path, mime, bytes, consent_confirmed) VALUES ($1, 'c.jpg', 'image/jpeg', 10, false)`, f.villageID)
		wantConstraintViolation(t, err, "no consent")
	})

	t.Run("reserved never exceeds quota", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `INSERT INTO availability (package_id, date, quota, reserved) VALUES ($1, '2030-01-01', 5, 5)`, f.packageID); err != nil {
			t.Fatal(err)
		}
		_, err := pool.Exec(ctx, `UPDATE availability SET reserved = 6 WHERE package_id = $1`, f.packageID)
		wantConstraintViolation(t, err, "reserved > quota")
		_, err = pool.Exec(ctx, `UPDATE availability SET quota = 4 WHERE package_id = $1`, f.packageID)
		wantConstraintViolation(t, err, "quota < reserved")
	})

	t.Run("booking village must match package village", func(t *testing.T) {
		var other int64
		if err := pool.QueryRow(ctx, `INSERT INTO villages (slug, name, district_id, contact_phone) VALUES ('desa-b', 'Desa B', $1, '0812') RETURNING id`, f.districtID).Scan(&other); err != nil {
			t.Fatal(err)
		}
		_, err := pool.Exec(ctx, `INSERT INTO bookings (package_id, village_id, traveler_id, start_date, party_size, total_price_idr, contact_name, contact_phone, expires_at)
			VALUES ($1, $2, $3, '2030-01-01', 1, 100000, 'A', '0811', now())`, f.packageID, other, f.userID)
		wantConstraintViolation(t, err, "mismatched village")
	})

	t.Run("cancelled status requires cancelled_by", func(t *testing.T) {
		_, err := pool.Exec(ctx, `INSERT INTO bookings (package_id, village_id, traveler_id, start_date, party_size, total_price_idr, contact_name, contact_phone, expires_at, status)
			VALUES ($1, $2, $3, '2030-01-01', 1, 100000, 'A', '0811', now(), 'cancelled')`, f.packageID, f.villageID, f.userID)
		wantConstraintViolation(t, err, "cancelled without cancelled_by")
	})

	t.Run("deleting a user nulls traveler and reporter", func(t *testing.T) {
		var uid, bid, rid int64
		if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, name) VALUES ('del@example.com', 'x', 'Del') RETURNING id`).Scan(&uid); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO bookings (package_id, village_id, traveler_id, start_date, party_size, total_price_idr, contact_name, contact_phone, expires_at)
			VALUES ($1, $2, $3, '2030-01-02', 1, 100000, 'Del', '0811', now()) RETURNING id`, f.packageID, f.villageID, uid).Scan(&bid); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO reports (reporter_id, target_type, target_id, reason) VALUES ($1, 'village', $2, 'spam') RETURNING id`, uid, f.villageID).Scan(&rid); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, uid); err != nil {
			t.Fatal(err)
		}
		var traveler, reporter *int64
		if err := pool.QueryRow(ctx, `SELECT traveler_id FROM bookings WHERE id = $1`, bid).Scan(&traveler); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT reporter_id FROM reports WHERE id = $1`, rid).Scan(&reporter); err != nil {
			t.Fatal(err)
		}
		if traveler != nil || reporter != nil {
			t.Errorf("traveler_id=%v reporter_id=%v, want both NULL", traveler, reporter)
		}
	})
}
