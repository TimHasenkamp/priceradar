package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoad_Defaults(t *testing.T) {
	c, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.ScrapeInterval != 6*time.Hour || c.RequestDelay != 15*time.Second || c.MaxRetries != 3 ||
		c.ListenAddr != ":8080" || c.LogLevel != slog.LevelInfo || c.LogFormat != "json" {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestLoad_OverridesAndErrors(t *testing.T) {
	c, err := Load(env(map[string]string{"PR_SCRAPE_INTERVAL": "30m", "PR_MAX_RETRIES": "1", "LOG_LEVEL": "debug", "LOG_FORMAT": "TEXT"}))
	if err != nil || c.ScrapeInterval != 30*time.Minute || c.MaxRetries != 1 || c.LogLevel != slog.LevelDebug || c.LogFormat != "text" {
		t.Fatalf("overrides: %+v %v", c, err)
	}
	tests := map[string]map[string]string{
		"bad duration":   {"PR_REQUEST_DELAY": "soon"},
		"too short":      {"PR_SCRAPE_INTERVAL": "10s"},
		"bad int":        {"PR_MAX_RETRIES": "x"},
		"bad level":      {"LOG_LEVEL": "loud"},
		"bad log format": {"LOG_FORMAT": "xml"},
	}
	for name, m := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(env(m)); err == nil || !strings.HasPrefix(err.Error(), "config:") {
				t.Fatalf("want config error, got %v", err)
			}
		})
	}
}
