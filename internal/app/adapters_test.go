package app

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"desakita/internal/booking"
	"desakita/internal/platform/dbtest"
	"desakita/internal/platform/upload"
	"desakita/internal/tourpackage"
	"desakita/internal/village"
)

func TestPackageLookupAdapter(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	store, err := upload.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.DiscardHandler)
	villages := village.NewService(pool, store, logger)
	lookup := packageLookup{tourpackage.NewService(pool, villages, store, logger)}

	var pkg int64
	err = pool.QueryRow(ctx, `
		WITH p AS (INSERT INTO regions (kind, name) VALUES ('province', 'P') RETURNING id),
		     r AS (INSERT INTO regions (parent_id, kind, name) SELECT id, 'regency', 'R' FROM p RETURNING id),
		     d AS (INSERT INTO regions (parent_id, kind, name) SELECT id, 'district', 'D' FROM r RETURNING id),
		     v AS (INSERT INTO villages (slug, name, district_id, contact_phone, status) SELECT 'v', 'V', id, '0811', 'verified' FROM d RETURNING id)
		INSERT INTO tour_packages (village_id, name, price_per_person_idr, duration_days, default_quota)
		SELECT id, 'P', 50000, 1, 7 FROM v RETURNING id`).Scan(&pkg)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("maps fields and not-found", func(t *testing.T) {
		err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			info, err := lookup.LookupForBooking(ctx, tx, pkg)
			if err != nil {
				return err
			}
			if !info.Active || !info.VillageVerified || info.DefaultQuota != 7 || info.PricePerPersonIDR != 50000 {
				t.Errorf("unexpected info %+v", info)
			}
			if _, err := lookup.LookupForBooking(ctx, tx, 999999); !errors.Is(err, booking.ErrNotFound) {
				t.Errorf("missing package: %v, want booking.ErrNotFound", err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("share lock blocks deactivation until the booking transaction ends", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		if _, err := lookup.LookupForBooking(ctx, tx, pkg); err != nil {
			t.Fatal(err)
		}

		other, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer other.Rollback(ctx) //nolint:errcheck
		if _, err := other.Exec(ctx, "SET LOCAL lock_timeout = '300ms'"); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		_, err = other.Exec(ctx, `UPDATE tour_packages SET is_active = false WHERE id = $1`, pkg)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "55P03" { // lock_not_available
			t.Fatalf("deactivation was not blocked (err=%v after %v)", err, time.Since(start))
		}
	})
}
