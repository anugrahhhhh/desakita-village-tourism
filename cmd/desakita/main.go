// Command desakita runs the DesaKita web application and its maintenance tasks.
//
//	desakita serve              start the HTTP server (default)
//	desakita migrate up         apply all pending migrations
//	desakita migrate down [N]   roll back N migrations (default 1)
//	desakita seed               load idempotent development seed data
//	desakita promote-admin EMAIL  grant admin rights to an existing account
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
	"desakita/internal/auth"
	"desakita/internal/platform/config"
	"desakita/internal/platform/database"
)

func main() {
	// A local .env is a development convenience; real environments set variables directly.
	_ = godotenv.Load()
	cfg, cfgErr := config.Load(nil)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	if cfgErr != nil {
		logger.Error("invalid configuration", "err", cfgErr)
		os.Exit(1)
	}
	if err := run(logger, cfg, os.Args[1:]); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, cfg config.Config, args []string) error {

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
	case "promote-admin":
		if len(args) != 1 {
			return errors.New("usage: promote-admin EMAIL")
		}
		pool, err := database.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer pool.Close()
		svc, err := auth.NewService(pool)
		if err != nil {
			return err
		}
		if err := svc.PromoteAdmin(ctx, args[0]); err != nil {
			return fmt.Errorf("promote %s: %w", args[0], err)
		}
		logger.Info("promoted to admin", "email", args[0])
		return nil
	default:
		return fmt.Errorf("unknown command %q (want serve, migrate, seed, promote-admin)", cmd)
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
