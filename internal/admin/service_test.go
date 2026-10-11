package admin_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"desakita/internal/admin"
	"desakita/internal/platform/dbtest"
	"desakita/internal/platform/form"
)

func TestReports(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()
	svc := admin.NewService(pool)
	var user, adminID, village int64
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pool.QueryRow(ctx, "INSERT INTO users (email, password_hash, name) VALUES ('r@example.com', 'x', 'R') RETURNING id").Scan(&user))
	must(pool.QueryRow(ctx, "INSERT INTO users (email, password_hash, name, is_admin) VALUES ('a@example.com', 'x', 'A', true) RETURNING id").Scan(&adminID))
	must(pool.QueryRow(ctx, `
		WITH p AS (INSERT INTO regions (kind, name) VALUES ('province', 'P') RETURNING id),
		     r AS (INSERT INTO regions (parent_id, kind, name) SELECT id, 'regency', 'R' FROM p RETURNING id),
		     d AS (INSERT INTO regions (parent_id, kind, name) SELECT id, 'district', 'D' FROM r RETURNING id)
		INSERT INTO villages (slug, name, district_id, contact_phone) SELECT 'desa-x', 'Desa X', id, '0811' FROM d RETURNING id`).Scan(&village))
	vid := strconv.FormatInt(village, 10)

	var verr *form.ValidationError
	if err := svc.CreateReport(ctx, user, "village", vid, " "); !errors.As(err, &verr) {
		t.Errorf("empty reason: %v", err)
	}
	if err := svc.CreateReport(ctx, user, "user", vid, "x"); !errors.As(err, &verr) {
		t.Errorf("bad target type: %v", err)
	}
	if err := svc.CreateReport(ctx, user, "package", "999999", "x"); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("missing target: %v", err)
	}
	must(svc.CreateReport(ctx, user, "village", vid, "Nomor kontak palsu"))

	open, err := svc.OpenReports(ctx)
	if err != nil || len(open) != 1 || open[0].VillageName != "Desa X" || open[0].ReporterEmail != "r@example.com" {
		t.Fatalf("open reports: %+v %v", open, err)
	}
	must(svc.ResolveReport(ctx, adminID, open[0].ID, admin.StatusDismissed))
	if err := svc.ResolveReport(ctx, adminID, open[0].ID, admin.StatusResolved); !errors.Is(err, admin.ErrNotFound) {
		t.Errorf("resolving a closed report: %v", err)
	}
	if n, _ := svc.CountOpenReports(ctx); n != 0 {
		t.Errorf("open count = %d", n)
	}
}
