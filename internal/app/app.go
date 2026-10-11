// Package app is the composition root: it wires platform services and domain modules into
// one http.Handler. It is the only package allowed to import every module.
package app

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"desakita/internal/auth"
	"desakita/internal/booking"
	"desakita/internal/platform/config"
	"desakita/internal/platform/csrf"
	"desakita/internal/platform/httpx"
	"desakita/internal/platform/i18n"
	"desakita/internal/platform/render"
	"desakita/internal/platform/session"
	"desakita/internal/platform/upload"
	"desakita/internal/tourpackage"
	"desakita/internal/village"
	"desakita/web"
)

// Request body caps: ordinary forms, and multipart photo uploads (5 MiB file + form overhead).
const (
	formBodyLimit      = 1 << 20
	multipartBodyLimit = 6 << 20
)

type Options struct {
	Config config.Config
	Pool   *pgxpool.Pool
	Logger *slog.Logger
	// SessionCleanup is how often expired sessions are purged; 0 disables (tests).
	SessionCleanup time.Duration
	// Now overrides the clock for booking rules (tests); nil means time.Now.
	Now func() time.Time
}

// App is the wired application: an HTTP handler plus background workers.
type App struct {
	Handler http.Handler
	Booking *booking.Service
	logger  *slog.Logger
}

// expiryInterval is how often pending bookings past expires_at are expired (SPEC §4).
const expiryInterval = 5 * time.Minute

// RunBackground runs background workers until ctx is cancelled.
func (a *App) RunBackground(ctx context.Context) {
	a.Booking.RunExpiry(ctx, expiryInterval, a.logger)
}

func New(opts Options) (*App, error) {
	tr, err := i18n.Load(web.FS, "id")
	if err != nil {
		return nil, err
	}
	sm := session.New(opts.Pool, opts.Config.IsProduction(), opts.SessionCleanup)
	rd, err := render.New(web.FS, tr, sm, opts.Logger)
	if err != nil {
		return nil, err
	}
	authSvc, err := auth.NewService(opts.Pool)
	if err != nil {
		return nil, err
	}
	authH := auth.NewHandler(authSvc, sm, rd)

	store, err := upload.NewLocalStore(opts.Config.UploadDir)
	if err != nil {
		return nil, err
	}
	villageSvc := village.NewService(opts.Pool, store, opts.Logger)
	packageSvc := tourpackage.NewService(opts.Pool, villageSvc, store, opts.Logger)
	villageH := village.NewHandler(villageSvc, packageSvc, sm, rd)
	packageH := tourpackage.NewHandler(packageSvc, sm, rd)
	bookingSvc := booking.NewService(opts.Pool, packageLookup{packageSvc}, villageSvc, booking.Config{
		PendingTTL: opts.Config.BookingPendingTTL,
		Location:   opts.Config.Location,
		Now:        opts.Now,
	})
	bookingH := booking.NewHandler(bookingSvc, catalog{packageSvc, villageSvc}, sm, rd, tr)

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.NotFound(rd.NotFound)

	static := http.StripPrefix("/static/", http.FileServerFS(web.Static()))
	r.Handle("/static/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		static.ServeHTTP(w, r)
	}))
	r.Get("/media/{name}", upload.ServeHandler(store))
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := opts.Pool.Ping(ctx); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok")) //nolint:errcheck
	})

	// Everything below uses sessions, CSRF and the current viewer.
	r.Group(func(r chi.Router) {
		r.Use(sm.LoadAndSave)
		r.Use(httpx.BodyLimit(formBodyLimit, multipartBodyLimit))
		r.Use(csrf.Protect(sm, http.HandlerFunc(rd.Forbidden), http.HandlerFunc(rd.TooLarge)))
		r.Use(authH.LoadViewer)

		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			rd.Page(w, r, http.StatusOK, "home", nil)
		})
		authH.Routes(r)
		villageH.PublicRoutes(r)
		packageH.PublicRoutes(r)
		bookingH.TravelerRoutes(r)

		r.Route("/kelola", func(r chi.Router) {
			r.Use(httpx.RequireLogin)
			villageH.DashboardRoutes(r)
			r.Route("/desa/{villageID}", func(r chi.Router) {
				r.Use(villageH.RequireManager)
				villageH.ManageRoutes(r)
				r.Route("/paket", packageH.ManageRoutes)
				bookingH.ManageRoutes(r)
			})
		})
	})
	return &App{Handler: r, Booking: bookingSvc, logger: opts.Logger}, nil
}
