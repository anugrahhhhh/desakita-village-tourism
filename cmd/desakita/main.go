// Command desakita runs the DesaKita web application and its maintenance tasks.
//
//	desakita serve              start the HTTP server (default)
//	desakita migrate up         apply all pending migrations
//	desakita migrate down [N]   roll back N migrations (default 1)
//	desakita seed               load idempotent development seed data
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
	_ "time/tzdata" // Asia/Jakarta must resolve even in minimal container images.

	"github.com/joho/godotenv"

	"desakita/internal/app"
	"desakita/internal/platform/config"
	"desakita/internal/platform/database"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger, os.Args[1:]); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, args []string) error {
	// A local .env is a development convenience; real environments set variables directly.
	_ = godotenv.Load()
	cfg, err := config.Load(nil)
	if err != nil {
		return err
	}

	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "serve":
		return serve(ctx, logger, cfg)
	case "migrate":
		return migrateCmd(cfg, args)
	case "seed":
		pool, err := database.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer pool.Close()
		return database.Seed(ctx, pool)
	default:
		return fmt.Errorf("unknown command %q (want serve, migrate, seed)", cmd)
	}
}

func migrateCmd(cfg config.Config, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: migrate up | migrate down [N]")
	}
	switch args[0] {
	case "up":
		return database.MigrateUp(cfg.DatabaseURL)
	case "down":
		steps := 1
		if len(args) > 1 {
			n, err := strconv.Atoi(args[1])
			if err != nil || n < 1 {
				return fmt.Errorf("invalid step count %q", args[1])
			}
			steps = n
		}
		return database.MigrateDown(cfg.DatabaseURL, steps)
	default:
		return fmt.Errorf("unknown migrate direction %q", args[0])
	}
}

func serve(ctx context.Context, logger *slog.Logger, cfg config.Config) error {
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	a, err := app.New(app.Options{Config: cfg, Pool: pool, Logger: logger, SessionCleanup: 30 * time.Minute})
	if err != nil {
		return err
	}
	go a.RunBackground(ctx)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           a.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.HTTPAddr, "env", cfg.Env)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
