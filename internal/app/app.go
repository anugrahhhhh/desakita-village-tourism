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
	"desakita/internal/platform/config"
	"desakita/internal/platform/csrf"
	"desakita/internal/platform/httpx"
	"desakita/internal/platform/i18n"
	"desakita/internal/platform/render"
	"desakita/internal/platform/session"
	"desakita/web"
)

// formBodyLimit caps ordinary (non-upload) request bodies.
const formBodyLimit = 1 << 20

type Options struct {
	Config config.Config
	Pool   *pgxpool.Pool
	Logger *slog.Logger
	// SessionCleanup is how often expired sessions are purged; 0 disables (tests).
	SessionCleanup time.Duration
}

func New(opts Options) (http.Handler, error) {
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

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.NotFound(rd.NotFound)

	static := http.StripPrefix("/static/", http.FileServerFS(web.Static()))
	r.Handle("/static/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		static.ServeHTTP(w, r)
	}))
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
		r.Use(httpx.BodyLimit(formBodyLimit))
		r.Use(csrf.Protect(sm, http.HandlerFunc(rd.Forbidden)))
		r.Use(authH.LoadViewer)

		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			rd.Page(w, r, http.StatusOK, "home", nil)
		})
		authH.Routes(r)
	})
	return r, nil
}
