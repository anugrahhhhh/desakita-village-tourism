package auth_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/auth"
	"desakita/internal/platform/dbtest"
	"desakita/internal/platform/form"
)

func newService(t *testing.T) (*auth.Service, *pgxpool.Pool) {
	t.Helper()
	pool := dbtest.New(t)
	svc, err := auth.NewService(pool)
	if err != nil {
		t.Fatal(err)
	}
	return svc, pool
}

func register(t *testing.T, svc *auth.Service, email string) auth.User {
	t.Helper()
	u, err := svc.Register(context.Background(), auth.RegisterInput{Name: "Wisatawan", Email: email, Password: "rahasia123"})
	if err != nil {
		t.Fatalf("register %s: %v", email, err)
	}
	return u
}

func fieldErrors(t *testing.T, err error) form.Errors {
	t.Helper()
	var verr *form.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want *form.ValidationError, got %v", err)
	}
	return verr.Fields
}

func TestRegister(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	u, err := svc.Register(ctx, auth.RegisterInput{Name: "  Sari  ", Email: " sari@example.com ", Password: "rahasia123", Phone: "0812 3456 789"})
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "Sari" || u.Email != "sari@example.com" || u.Phone != "0812 3456 789" || u.IsAdmin {
		t.Errorf("unexpected user %+v", u)
	}

	t.Run("duplicate email differing only in case", func(t *testing.T) {
		_, err := svc.Register(ctx, auth.RegisterInput{Name: "X", Email: "SARI@example.com", Password: "rahasia123"})
		if got := fieldErrors(t, err)["email"]; got != "auth.email_taken" {
			t.Errorf("email error = %q", got)
		}
	})

	t.Run("invalid input", func(t *testing.T) {
		_, err := svc.Register(ctx, auth.RegisterInput{Name: "", Email: "Sari <s@example.com>", Password: "short", Phone: "abc"})
		errs := fieldErrors(t, err)
		want := map[string]string{
			"name": "validation.required", "email": "validation.email",
			"password": "validation.password_length", "phone": "validation.phone",
		}
		for f, k := range want {
			if errs[f] != k {
				t.Errorf("%s: got %q, want %q", f, errs[f], k)
			}
		}
	})
}

func TestAuthenticate(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	want := register(t, svc, "budi@example.com")

	if u, err := svc.Authenticate(ctx, "BUDI@example.com", "rahasia123"); err != nil || u.ID != want.ID {
		t.Errorf("valid login: u=%+v err=%v", u, err)
	}
	if _, err := svc.Authenticate(ctx, "budi@example.com", "salah-sandi"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("wrong password: %v", err)
	}
	if _, err := svc.Authenticate(ctx, "nobody@example.com", "rahasia123"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("unknown email: %v", err)
	}
}

// seedVillage creates a village managed by the given users and returns its id.
func seedVillage(t *testing.T, pool *pgxpool.Pool, slug string, managers ...int64) int64 {
	t.Helper()
	ctx := context.Background()
	var district, village int64
	err := pool.QueryRow(ctx, `
		WITH p AS (INSERT INTO regions (kind, name) VALUES ('province', $1) RETURNING id),
		     r AS (INSERT INTO regions (parent_id, kind, name) SELECT id, 'regency', 'R' FROM p RETURNING id)
		INSERT INTO regions (parent_id, kind, name) SELECT id, 'district', 'D' FROM r RETURNING id`, "P-"+slug).Scan(&district)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO villages (slug, name, district_id, contact_phone) VALUES ($1, $1, $2, '0811') RETURNING id`, slug, district).Scan(&village); err != nil {
		t.Fatal(err)
	}
	for _, m := range managers {
		if _, err := pool.Exec(ctx, `INSERT INTO village_members (village_id, user_id) VALUES ($1, $2)`, village, m); err != nil {
			t.Fatal(err)
		}
	}
	return village
}

func TestDeleteAccount(t *testing.T) {
	svc, pool := newService(t)
	ctx := context.Background()

	t.Run("wrong password is refused", func(t *testing.T) {
		u := register(t, svc, "wrongpw@example.com")
		if err := svc.DeleteAccount(ctx, u.ID, "bukan-ini"); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("last manager of a village is refused", func(t *testing.T) {
		u := register(t, svc, "solo@example.com")
		seedVillage(t, pool, "desa-solo", u.ID)
		if err := svc.DeleteAccount(ctx, u.ID, "rahasia123"); !errors.Is(err, auth.ErrLastManager) {
			t.Errorf("err = %v, want ErrLastManager", err)
		}
		if _, err := svc.UserByID(ctx, u.ID); err != nil {
			t.Errorf("user should still exist: %v", err)
		}
	})

	t.Run("anonymizes bookings and reports", func(t *testing.T) {
		manager := register(t, svc, "mgr@example.com")
		traveler := register(t, svc, "trav@example.com")
		village := seedVillage(t, pool, "desa-anon", manager.ID)
		var pkg, booking, report int64
		if err := pool.QueryRow(ctx, `INSERT INTO tour_packages (village_id, name, price_per_person_idr, duration_days, default_quota) VALUES ($1, 'P', 1000, 1, 5) RETURNING id`, village).Scan(&pkg); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO bookings (package_id, village_id, traveler_id, start_date, party_size, total_price_idr, contact_name, contact_phone, note, expires_at)
			VALUES ($1, $2, $3, '2030-01-01', 2, 2000, 'Trav', '0812', 'alergi kacang', now()) RETURNING id`, pkg, village, traveler.ID).Scan(&booking); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `INSERT INTO reports (reporter_id, target_type, target_id, reason) VALUES ($1, 'village', $2, 'x') RETURNING id`, traveler.ID, village).Scan(&report); err != nil {
			t.Fatal(err)
		}

		if err := svc.DeleteAccount(ctx, traveler.ID, "rahasia123"); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.UserByID(ctx, traveler.ID); !errors.Is(err, auth.ErrNotFound) {
			t.Errorf("user still exists: %v", err)
		}
		var travelerID, reporterID *int64
		var note, name, phone string
		if err := pool.QueryRow(ctx, `SELECT traveler_id, note, contact_name, contact_phone FROM bookings WHERE id = $1`, booking).Scan(&travelerID, &note, &name, &phone); err != nil {
			t.Fatal(err)
		}
		if travelerID != nil || note != "" || name != "Akun dihapus" || phone != "" {
			t.Errorf("booking not anonymized: traveler=%v note=%q name=%q phone=%q", travelerID, note, name, phone)
		}
		if err := pool.QueryRow(ctx, `SELECT reporter_id FROM reports WHERE id = $1`, report).Scan(&reporterID); err != nil {
			t.Fatal(err)
		}
		if reporterID != nil {
			t.Errorf("reporter_id = %v, want NULL", *reporterID)
		}
	})

	t.Run("co-managers deleting concurrently leave one manager", func(t *testing.T) {
		a := register(t, svc, "co-a@example.com")
		b := register(t, svc, "co-b@example.com")
		village := seedVillage(t, pool, "desa-co", a.ID, b.ID)

		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, u := range []auth.User{a, b} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = svc.DeleteAccount(ctx, u.ID, "rahasia123")
			}()
		}
		wg.Wait()

		var ok, refused int
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, auth.ErrLastManager):
				refused++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}
		if ok != 1 || refused != 1 {
			t.Errorf("ok=%d refused=%d, want exactly one of each", ok, refused)
		}
		var members int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM village_members WHERE village_id = $1`, village).Scan(&members); err != nil {
			t.Fatal(err)
		}
		if members != 1 {
			t.Errorf("village has %d managers, want 1", members)
		}
	})
}
