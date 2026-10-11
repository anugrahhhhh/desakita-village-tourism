package tourpackage_test

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/platform/dbtest"
	"desakita/internal/platform/form"
	"desakita/internal/platform/upload"
	"desakita/internal/tourpackage"
	"desakita/internal/village"
)

type env struct {
	pool       *pgxpool.Pool
	villages   *village.Service
	svc        *tourpackage.Service
	owner      int64
	villageA   int64
	villageB   int64
	activities []int64
}

func newEnv(t *testing.T) env {
	t.Helper()
	ctx := context.Background()
	pool := dbtest.New(t)
	store, err := upload.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.DiscardHandler)
	e := env{pool: pool, villages: village.NewService(pool, store, logger)}
	e.svc = tourpackage.NewService(pool, e.villages, store, logger)

	var district int64
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pool.QueryRow(ctx, `
		WITH p AS (INSERT INTO regions (kind, name) VALUES ('province', 'P') RETURNING id),
		     r AS (INSERT INTO regions (parent_id, kind, name) SELECT id, 'regency', 'R' FROM p RETURNING id)
		INSERT INTO regions (parent_id, kind, name) SELECT id, 'district', 'D' FROM r RETURNING id`).Scan(&district))
	must(pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, name) VALUES ('o@example.com', 'x', 'O') RETURNING id`).Scan(&e.owner))
	in := village.ProfileInput{DistrictID: strconv.FormatInt(district, 10), ContactPhone: "08123456789"}
	in.Name = "Desa A"
	e.villageA, err = e.villages.Create(ctx, e.owner, in)
	must(err)
	in.Name = "Desa B"
	e.villageB, err = e.villages.Create(ctx, e.owner, in)
	must(err)
	rows, err := pool.Query(ctx, `INSERT INTO activity_types (slug, name) VALUES ('alam', 'Alam'), ('budaya', 'Budaya') RETURNING id`)
	must(err)
	for rows.Next() {
		var id int64
		must(rows.Scan(&id))
		e.activities = append(e.activities, id)
	}
	must(rows.Err())
	return e
}

func (e env) input(name string) tourpackage.Input {
	return tourpackage.Input{
		Name: name, Price: "Rp 150.000", DurationDays: "2", DurationNights: "1", DefaultQuota: "20",
		ActivityTypeIDs: []string{strconv.FormatInt(e.activities[0], 10)},
	}
}

func TestCreateUpdateGet(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id, err := e.svc.Create(ctx, e.villageA, e.input("Menanam Padi"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.Get(ctx, e.villageA, id)
	if err != nil {
		t.Fatal(err)
	}
	if p.PricePerPersonIDR != 150000 || p.DurationDays != 2 || p.DurationNights != 1 || p.DefaultQuota != 20 || !p.IsActive {
		t.Errorf("unexpected package %+v", p)
	}
	if len(p.ActivityTypeIDs) != 1 || p.ActivityTypeIDs[0] != e.activities[0] {
		t.Errorf("activity types = %v", p.ActivityTypeIDs)
	}

	if _, err := e.svc.Get(ctx, e.villageB, id); !errors.Is(err, tourpackage.ErrNotFound) {
		t.Errorf("package reachable through another village: %v", err)
	}
	if err := e.svc.Update(ctx, e.villageB, id, e.input("Hijack")); !errors.Is(err, tourpackage.ErrNotFound) {
		t.Errorf("update through another village: %v", err)
	}
	if err := e.svc.SetActive(ctx, e.villageB, id, false); !errors.Is(err, tourpackage.ErrNotFound) {
		t.Errorf("deactivate through another village: %v", err)
	}

	in := e.input("Menanam Padi Organik")
	in.ActivityTypeIDs = []string{strconv.FormatInt(e.activities[1], 10), strconv.FormatInt(e.activities[0], 10)}
	in.DurationDays, in.DurationNights = "1", "0"
	if err := e.svc.Update(ctx, e.villageA, id, in); err != nil {
		t.Fatal(err)
	}
	p, _ = e.svc.Get(ctx, e.villageA, id)
	if p.Name != "Menanam Padi Organik" || p.DurationDays != 1 || len(p.ActivityTypeIDs) != 2 {
		t.Errorf("after update %+v", p)
	}
}

func TestValidation(t *testing.T) {
	e := newEnv(t)
	cases := map[string]struct {
		mutate func(*tourpackage.Input)
		field  string
	}{
		"empty name":           {func(in *tourpackage.Input) { in.Name = " " }, "name"},
		"negative price":       {func(in *tourpackage.Input) { in.Price = "-5" }, "price"},
		"text price":           {func(in *tourpackage.Input) { in.Price = "gratis" }, "price"},
		"zero days":            {func(in *tourpackage.Input) { in.DurationDays = "0" }, "duration_days"},
		"nights exceed days":   {func(in *tourpackage.Input) { in.DurationDays, in.DurationNights = "1", "2" }, "duration_nights"},
		"zero quota":           {func(in *tourpackage.Input) { in.DefaultQuota = "0" }, "default_quota"},
		"unknown activity":     {func(in *tourpackage.Input) { in.ActivityTypeIDs = []string{"999999"} }, "activity_types"},
		"non-numeric activity": {func(in *tourpackage.Input) { in.ActivityTypeIDs = []string{"x"} }, "activity_types"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := e.input("Paket")
			tc.mutate(&in)
			_, err := e.svc.Create(context.Background(), e.villageA, in)
			var verr *form.ValidationError
			if !errors.As(err, &verr) || verr.Fields[tc.field] == "" {
				t.Errorf("err = %v, want error on %s", err, tc.field)
			}
		})
	}
}

func TestPublicVisibilityAndCards(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	active, _ := e.svc.Create(ctx, e.villageA, e.input("Aktif"))
	inactive, _ := e.svc.Create(ctx, e.villageA, e.input("Nonaktif"))
	if err := e.svc.SetActive(ctx, e.villageA, inactive, false); err != nil {
		t.Fatal(err)
	}
	var stranger int64
	if err := e.pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, name) VALUES ('s@example.com', 'x', 'S') RETURNING id`).Scan(&stranger); err != nil {
		t.Fatal(err)
	}

	// Village still pending: nobody but managers sees packages.
	if _, err := e.svc.Public(ctx, "desa-a", active, 0); !errors.Is(err, tourpackage.ErrNotFound) {
		t.Errorf("pending village package visible: %v", err)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE villages SET status = 'verified' WHERE id = $1`, e.villageA); err != nil {
		t.Fatal(err)
	}
	if v, err := e.svc.Public(ctx, "desa-a", active, 0); err != nil || v.Preview || len(v.ActivityTypes) != 1 {
		t.Errorf("active package: %+v %v", v, err)
	}
	if _, err := e.svc.Public(ctx, "desa-a", inactive, stranger); !errors.Is(err, tourpackage.ErrNotFound) {
		t.Errorf("inactive package visible to stranger: %v", err)
	}
	if v, err := e.svc.Public(ctx, "desa-a", inactive, e.owner); err != nil || !v.Preview {
		t.Errorf("owner preview of inactive package: %+v %v", v.Preview, err)
	}
	// Package id under the wrong village slug.
	if _, err := e.pool.Exec(ctx, `UPDATE villages SET status = 'verified' WHERE id = $1`, e.villageB); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Public(ctx, "desa-b", active, 0); !errors.Is(err, tourpackage.ErrNotFound) {
		t.Errorf("package shown under another village: %v", err)
	}

	cards, err := e.svc.ActivePackageCards(ctx, e.villageA)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards[0].ID != active || cards[0].CoverPath != "" {
		t.Errorf("cards = %+v", cards)
	}
}
