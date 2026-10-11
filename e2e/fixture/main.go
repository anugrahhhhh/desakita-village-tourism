// Command fixture starts DesaKita on a fresh database filled with known demo data, for the
// Playwright end-to-end tests (see e2e/playwright.config.ts). It never touches the dev DB.
//
//	E2E_ADMIN_DATABASE_URL  server to create desakita_e2e on (default local postgres)
//	E2E_ADDR                listen address (default 127.0.0.1:18095)
package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/app"
	"desakita/internal/auth"
	"desakita/internal/platform/config"
	"desakita/internal/platform/database"
	"desakita/internal/platform/upload"
	"desakita/internal/tourpackage"
	"desakita/internal/village"
)

const (
	dbName   = "desakita_e2e"
	password = "rahasia123"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fixture:", err)
		os.Exit(1)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func run() error {
	ctx := context.Background()
	adminURL := env("E2E_ADMIN_DATABASE_URL", "postgres://postgres@localhost:5432/postgres?sslmode=disable")
	addr := env("E2E_ADDR", "127.0.0.1:18095")

	if err := recreateDB(ctx, adminURL); err != nil {
		return err
	}
	u, err := url.Parse(adminURL)
	if err != nil {
		return err
	}
	u.Path = "/" + dbName
	dbURL := u.String()
	if err := database.MigrateUp(dbURL); err != nil {
		return err
	}
	pool, err := database.Open(ctx, dbURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := database.Seed(ctx, pool); err != nil {
		return err
	}

	uploads, err := os.MkdirTemp("", "desakita-e2e-uploads-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(uploads)
	if err := seedDemo(ctx, pool, uploads); err != nil {
		return fmt.Errorf("seed demo: %w", err)
	}

	tileDir, err := os.MkdirTemp("", "desakita-e2e-tiles-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tileDir)
	tileUpstream, err := fakeTileServer()
	if err != nil {
		return err
	}
	cfg, err := config.Load(func(k string) string {
		return map[string]string{
			"DATABASE_URL": dbURL, "UPLOAD_DIR": uploads, "HTTP_ADDR": addr,
			"MAP_TILE_URL": tileUpstream, "MAP_TILE_DIR": tileDir,
		}[k]
	})
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a, err := app.New(app.Options{Config: cfg, Pool: pool, Logger: logger})
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: addr, Handler: a.Handler, ReadHeaderTimeout: 5 * time.Second}
	fmt.Println("fixture listening on", addr)
	return srv.ListenAndServe()
}

func recreateDB(ctx context.Context, adminURL string) error {
	c, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer c.Close(ctx)
	if _, err := c.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)"); err != nil {
		return err
	}
	_, err = c.Exec(ctx, "CREATE DATABASE "+dbName)
	return err
}

// landscape returns a small JPEG so photos render in the pages under test.
func landscape(sky, field color.RGBA) *bytes.Reader {
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			c := sky
			if y > 170+(x%80)/8 {
				c = field
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	jpeg.Encode(&buf, img, nil) //nolint:errcheck
	return bytes.NewReader(buf.Bytes())
}

func seedDemo(ctx context.Context, pool *pgxpool.Pool, uploads string) error {
	authSvc, err := auth.NewService(pool)
	if err != nil {
		return err
	}
	register := func(name, email, phone string) (auth.User, error) {
		return authSvc.Register(ctx, auth.RegisterInput{Name: name, Email: email, Password: password, Phone: phone})
	}
	manager, err := register("Bu Sri Pengelola", "pengelola@e2e.test", "0812-1111-2222")
	if err != nil {
		return err
	}
	if _, err := register("Sari Wisatawan", "sari@e2e.test", "0813-3333-4444"); err != nil {
		return err
	}
	if _, err := register("Admin DesaKita", "admin@e2e.test", ""); err != nil {
		return err
	}
	if err := authSvc.PromoteAdmin(ctx, "admin@e2e.test"); err != nil {
		return err
	}

	store, err := upload.NewLocalStore(uploads)
	if err != nil {
		return err
	}
	logger := slog.New(slog.DiscardHandler)
	villages := village.NewService(pool, store, logger)
	packages := tourpackage.NewService(pool, villages, store, logger)

	var district int64
	if err := pool.QueryRow(ctx, "SELECT id FROM regions WHERE kind = 'district' AND name = 'Kraton'").Scan(&district); err != nil {
		return err
	}
	vid, err := villages.Create(ctx, manager.ID, village.ProfileInput{
		Name:            "Desa Wisata Sukamaju",
		DistrictID:      strconv.FormatInt(district, 10),
		Address:         "Dusun Krajan RT 02",
		Description:     "Desa di kaki bukit dengan sawah terasering dan sanggar batik warga.",
		ContactPhone:    "0812-1111-2222",
		ContactWhatsApp: "0812-1111-2222",
		Lat:             "-7.797068",
		Lng:             "110.370529",
	})
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, "UPDATE villages SET status = 'verified' WHERE id = $1", vid); err != nil {
		return err
	}
	green := color.RGBA{60, 130, 70, 255}
	if err := villages.AddPhoto(ctx, manager.ID, vid, landscape(color.RGBA{130, 185, 230, 255}, green), true, "Sawah terasering Sukamaju"); err != nil {
		return err
	}

	activity := func(slug string) string {
		var id int64
		pool.QueryRow(ctx, "SELECT id FROM activity_types WHERE slug = $1", slug).Scan(&id) //nolint:errcheck
		return strconv.FormatInt(id, 10)
	}
	padi, err := packages.Create(ctx, vid, tourpackage.Input{
		Name: "Menanam Padi dan Membajak Sawah", Description: "Belajar menanam padi bersama petani.",
		Price: "150000", DurationDays: "1", DurationNights: "0", DefaultQuota: "20",
		ActivityTypeIDs: []string{activity("pertanian"), activity("edukasi")},
	})
	if err != nil {
		return err
	}
	if err := packages.AddPhoto(ctx, manager.ID, padi, landscape(color.RGBA{240, 200, 150, 255}, green), true, "Wisatawan menanam padi"); err != nil {
		return err
	}
	// A visit Sari already made, so she can write a review.
	if _, err := pool.Exec(ctx, `INSERT INTO bookings (package_id, village_id, traveler_id, start_date, party_size,
			total_price_idr, contact_name, contact_phone, expires_at, status, decided_at, completed_at)
		SELECT $1, $2, u.id, CURRENT_DATE - 7, 2, 300000, 'Sari Wisatawan', '0813-3333-4444',
			now() - interval '8 days', 'completed', now() - interval '9 days', now() - interval '6 days'
		FROM users u WHERE u.email = 'sari@e2e.test'`, padi, vid); err != nil {
		return err
	}
	if _, err := packages.Create(ctx, vid, tourpackage.Input{
		Name: "Belajar Membatik Tulis", Description: "Membuat sapu tangan batik tulis.",
		Price: "85000", DurationDays: "1", DurationNights: "0", DefaultQuota: "15",
		ActivityTypeIDs: []string{activity("budaya"), activity("kerajinan")},
	}); err != nil {
		return err
	}

	// A registration waiting in the admin verification queue.
	_, err = villages.Create(ctx, manager.ID, village.ProfileInput{
		Name: "Desa Wisata Menunggu", DistrictID: strconv.FormatInt(district, 10),
		ContactPhone: "0812-5555-6666", Description: "Desa yang baru mendaftar.",
	})
	return err
}

// fakeTileServer stands in for OpenStreetMap so the E2E run never leaves the machine: every
// tile is the same plain 256 px PNG. It returns the upstream URL template.
func fakeTileServer() (string, error) {
	img := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			img.Set(x, y, color.RGBA{232, 226, 210, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	tile := buf.Bytes()
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { //nolint:errcheck
		w.Header().Set("Content-Type", "image/png")
		w.Write(tile) //nolint:errcheck
	}))
	return "http://" + ln.Addr().String() + "/{z}/{x}/{y}.png", nil
}
