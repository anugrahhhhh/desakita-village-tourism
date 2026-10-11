// Package config loads application settings from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"net/url"
	"os"
	"strconv"
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
	// BaseURL is the public origin used for links in emails (APP_BASE_URL).
	BaseURL string
	Mail    Mail
	Map     Map
}

// Map configures the tile server (SPEC §14). Tiles are cached in TileDir; TileUpstream is
// where missing tiles come from ("off" serves only TileDir).
type Map struct {
	TileUpstream string
	TileDir      string
	// AttributionText overrides the translated default credit (MAP_ATTRIBUTION).
	AttributionText string
	AttributionURL  string
}

// Mail configures outgoing email. Without SMTPHost, messages are written to Dir as .eml
// files (development).
type Mail struct {
	From         *mail.Address
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPTLS      string // starttls | tls | none
	Dir          string
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

	cfg.BaseURL = strings.TrimRight(get("APP_BASE_URL", "http://localhost:8080"), "/")
	if u, err := url.Parse(cfg.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, fmt.Errorf("APP_BASE_URL must be an absolute http(s) URL, got %q", cfg.BaseURL))
	}
	from, err := mail.ParseAddress(get("MAIL_FROM", "DesaKita <noreply@desakita.local>"))
	if err != nil {
		errs = append(errs, fmt.Errorf("MAIL_FROM: %w", err))
	}
	cfg.Mail = Mail{
		From: from, SMTPHost: getenv("SMTP_HOST"), SMTPUsername: getenv("SMTP_USERNAME"),
		SMTPPassword: getenv("SMTP_PASSWORD"), SMTPTLS: get("SMTP_TLS", "starttls"), Dir: get("MAIL_DIR", "./data/mail"),
	}
	port, err := strconv.Atoi(get("SMTP_PORT", "587"))
	if err != nil || port < 1 || port > 65535 {
		errs = append(errs, fmt.Errorf("SMTP_PORT must be a port number"))
	}
	cfg.Mail.SMTPPort = port
	if t := cfg.Mail.SMTPTLS; t != "starttls" && t != "tls" && t != "none" {
		errs = append(errs, fmt.Errorf("SMTP_TLS must be starttls, tls or none, got %q", t))
	}

	cfg.Map = Map{
		TileUpstream:    get("MAP_TILE_URL", "https://tile.openstreetmap.org/{z}/{x}/{y}.png"),
		TileDir:         get("MAP_TILE_DIR", "./data/tiles"),
		AttributionText: getenv("MAP_ATTRIBUTION"),
		AttributionURL:  get("MAP_ATTRIBUTION_URL", "https://www.openstreetmap.org/copyright"),
	}
	if cfg.Map.TileUpstream == "off" {
		cfg.Map.TileUpstream = ""
	} else if u, err := url.Parse(cfg.Map.TileUpstream); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		!strings.Contains(cfg.Map.TileUpstream, "{z}") || !strings.Contains(cfg.Map.TileUpstream, "{x}") || !strings.Contains(cfg.Map.TileUpstream, "{y}") {
		errs = append(errs, fmt.Errorf("MAP_TILE_URL must be an http(s) URL with {z}, {x} and {y}, or off"))
	}
	if u, err := url.Parse(cfg.Map.AttributionURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		errs = append(errs, fmt.Errorf("MAP_ATTRIBUTION_URL must be an http(s) URL"))
	}

	loc, err := time.LoadLocation(get("APP_TIMEZONE", "Asia/Jakarta"))
	if err != nil {
		errs = append(errs, fmt.Errorf("APP_TIMEZONE: %w", err))
	}
	cfg.Location = loc

	return cfg, errors.Join(errs...)
}
