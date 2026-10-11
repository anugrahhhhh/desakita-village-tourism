// Package config loads application settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

type Config struct {
	Env               string // "development" or "production"
	HTTPAddr          string
	DatabaseURL       string
	UploadDir         string
	BookingPendingTTL time.Duration
	Location          *time.Location
	// TrustProxy: take the client IP from X-Real-IP/X-Forwarded-For. Enable only behind a
	// reverse proxy that sets these headers itself.
	TrustProxy bool
	// LogLevel is the minimum level written to the JSON log (LOG_LEVEL: debug|info|warn|error).
	LogLevel slog.Level
}

func (c Config) IsProduction() bool { return c.Env == "production" }

// Load reads configuration from the environment. getenv is injectable for tests.
func Load(getenv func(string) string) (Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	get := func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}

	cfg := Config{
		Env:         get("APP_ENV", "development"),
		HTTPAddr:    get("HTTP_ADDR", ":8080"),
		DatabaseURL: getenv("DATABASE_URL"),
		UploadDir:   get("UPLOAD_DIR", "./data/uploads"),
		TrustProxy:  getenv("TRUST_PROXY") == "true",
	}

	var errs []error
	if cfg.Env != "development" && cfg.Env != "production" {
		errs = append(errs, fmt.Errorf("APP_ENV must be development or production, got %q", cfg.Env))
	}
	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}

	ttl, err := time.ParseDuration(get("BOOKING_PENDING_TTL", "48h"))
	switch {
	case err != nil:
		errs = append(errs, fmt.Errorf("BOOKING_PENDING_TTL: %w", err))
	case ttl <= 0:
		errs = append(errs, errors.New("BOOKING_PENDING_TTL must be positive"))
	}
	cfg.BookingPendingTTL = ttl

	if err := cfg.LogLevel.UnmarshalText([]byte(strings.ToUpper(get("LOG_LEVEL", "info")))); err != nil {
		errs = append(errs, fmt.Errorf("LOG_LEVEL: %w", err))
	}

	loc, err := time.LoadLocation(get("APP_TIMEZONE", "Asia/Jakarta"))
	if err != nil {
		errs = append(errs, fmt.Errorf("APP_TIMEZONE: %w", err))
	}
	cfg.Location = loc

	return cfg, errors.Join(errs...)
}
