package app_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"desakita/internal/app"
	"desakita/internal/platform/config"
	"desakita/internal/platform/database"
	"desakita/internal/platform/dbtest"
)

func TestSeedDemo(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()
	if err := database.Seed(ctx, pool); err != nil {
		t.Fatal(err)
	}
	uploads := t.TempDir()
	cfg, err := config.Load(func(k string) string {
		return map[string]string{"DATABASE_URL": "unused", "UPLOAD_DIR": uploads}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.DiscardHandler)

	for run, want := range []int{8, 0} { // idempotent
		n, err := app.SeedDemo(ctx, pool, cfg, logger)
		if err != nil || n != want {
			t.Fatalf("run %d: added %d (%v), want %d", run, n, err, want)
		}
	}
	var villages, located, packages, photos int
	err = pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM villages WHERE status = 'verified'),
		(SELECT count(*) FROM villages WHERE lat IS NOT NULL),
		(SELECT count(*) FROM tour_packages WHERE is_active),
		(SELECT count(*) FROM media)`).Scan(&villages, &located, &packages, &photos)
	if err != nil {
		t.Fatal(err)
	}
	if villages != 8 || located != 8 || packages != 13 || photos != 21 {
		t.Errorf("villages %d, located %d, packages %d, photos %d", villages, located, packages, photos)
	}
	for _, city := range []string{"Kabupaten Bandung Barat", "Kabupaten Bandung", "Kabupaten Sleman", "Kabupaten Bantul", "Kota Surabaya",
		"Kota Administrasi Jakarta Selatan", "Kabupaten Administrasi Kepulauan Seribu"} {
		var n int
		pool.QueryRow(ctx, `SELECT count(*) FROM villages v JOIN regions d ON d.id = v.district_id JOIN regions r ON r.id = d.parent_id
			WHERE r.name = $1`, city).Scan(&n) //nolint:errcheck
		if n == 0 {
			t.Errorf("no demo village in %s", city)
		}
	}

	// The demo shows up in search (regency filter) and on the map, and the manager can log in.
	a, err := app.New(app.Options{Config: cfg, Pool: pool, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	srv := newServerFor(t, a)
	c := newClient(t, srv)
	var surabaya int64
	pool.QueryRow(ctx, "SELECT id FROM regions WHERE kind = 'regency' AND name = 'Kota Surabaya'").Scan(&surabaya) //nolint:errcheck
	r := c.get("/?wilayah=" + strconv.FormatInt(surabaya, 10))
	if !strings.Contains(r.body, "Kelas Masak Rujak Cingur") || strings.Contains(r.body, "Belajar Membatik Tulis") {
		t.Error("regency filter does not isolate Surabaya packages")
	}
	if r := c.get("/peta"); strings.Count(r.body, `data-lat="`) != 8 {
		t.Errorf("map lists %d villages, want 8", strings.Count(r.body, `data-lat="`))
	}
	tok := c.csrfFrom("/masuk")
	if r := c.post("/masuk", url.Values{"csrf_token": {tok}, "email": {app.DemoManagerEmail}, "password": {app.DemoPassword}}); r.code != http.StatusSeeOther {
		t.Errorf("demo manager login: %d", r.code)
	}

	cfg.Env = "production"
	if _, err := app.SeedDemo(ctx, pool, cfg, logger); err == nil {
		t.Error("seed-demo ran in production")
	}
}

func newServerFor(t *testing.T, a *app.App) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(a.Handler)
	t.Cleanup(srv.Close)
	return srv
}
