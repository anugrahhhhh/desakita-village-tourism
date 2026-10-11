package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Env != "development" || cfg.HTTPAddr != ":8080" {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if cfg.BookingPendingTTL != 48*time.Hour {
		t.Errorf("BookingPendingTTL = %v, want 48h", cfg.BookingPendingTTL)
	}
	if cfg.Location.String() != "Asia/Jakarta" {
		t.Errorf("Location = %v, want Asia/Jakarta", cfg.Location)
	}
}

func TestLoadRejectsInvalid(t *testing.T) {
	_, err := Load(env(map[string]string{
		"APP_ENV":             "staging",
		"BOOKING_PENDING_TTL": "-1h",
	}))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"APP_ENV", "DATABASE_URL", "BOOKING_PENDING_TTL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestLoadLogLevel(t *testing.T) {
	cfg, err := Load(env(map[string]string{"DATABASE_URL": "x", "LOG_LEVEL": "debug"}))
	if err != nil || cfg.LogLevel != slog.LevelDebug {
		t.Errorf("debug: level=%v err=%v", cfg.LogLevel, err)
	}
	cfg, _ = Load(env(map[string]string{"DATABASE_URL": "x"}))
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("default level = %v, want INFO", cfg.LogLevel)
	}
	if _, err := Load(env(map[string]string{"DATABASE_URL": "x", "LOG_LEVEL": "loud"})); err == nil || !strings.Contains(err.Error(), "LOG_LEVEL") {
		t.Errorf("invalid level accepted: %v", err)
	}
}
