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

func TestLoad_NotifyDefaults(t *testing.T) {
	c, err := Load(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	n := c.Notify
	if n.ChangePct != 5 || n.ChangeMinCents != 100 || !n.Rises || n.LowMinHistory != 7*24*time.Hour ||
		n.TrendWindow != 7*24*time.Hour || n.TrendPct != 8 || n.TrendCooldown != 72*time.Hour || n.Failures != 3 {
		t.Fatalf("notify defaults: %+v", n)
	}
	if c.GotifyURL != "" || c.GotifyToken != "" || c.PublicURL != "" || c.GotifyConfigured() {
		t.Fatalf("gotify defaults: %+v", c)
	}
}

func TestLoad_NotifyOverrides(t *testing.T) {
	c, err := Load(env(map[string]string{
		"PR_GOTIFY_URL": "https://gotify.example/", "PR_GOTIFY_TOKEN": "Axyz", "PR_PUBLIC_URL": "https://pr.example/",
		"PR_NOTIFY_CHANGE_PCT": "2.5", "PR_NOTIFY_CHANGE_MIN_CENTS": "0", "PR_NOTIFY_RISES": "false",
		"PR_NOTIFY_LOW_MIN_HISTORY": "14d", "PR_NOTIFY_TREND_WINDOW": "1d12h", "PR_NOTIFY_TREND_PCT": "10",
		"PR_NOTIFY_TREND_COOLDOWN": "48h", "PR_NOTIFY_FAILURES": "5",
	}))
	if err != nil {
		t.Fatal(err)
	}
	n := c.Notify
	if n.ChangePct != 2.5 || n.ChangeMinCents != 0 || n.Rises || n.LowMinHistory != 14*24*time.Hour ||
		n.TrendWindow != 36*time.Hour || n.TrendPct != 10 || n.TrendCooldown != 48*time.Hour || n.Failures != 5 {
		t.Fatalf("notify: %+v", n)
	}
	if c.GotifyURL != "https://gotify.example" || c.PublicURL != "https://pr.example" || !c.GotifyConfigured() || n.PublicURL != c.PublicURL {
		t.Fatalf("urls: %+v", c)
	}
	if c, _ := Load(env(map[string]string{"PR_GOTIFY_URL": "https://g"})); c.GotifyConfigured() {
		t.Fatal("url without token is not configured")
	}
}

func TestLoad_NotifyErrors(t *testing.T) {
	tests := map[string]map[string]string{
		"gotify url not absolute": {"PR_GOTIFY_URL": "gotify.local"},
		"gotify url bad scheme":   {"PR_GOTIFY_URL": "ftp://g"},
		"public url bad":          {"PR_PUBLIC_URL": "/relative"},
		"pct negative":            {"PR_NOTIFY_CHANGE_PCT": "-1"},
		"pct not a number":        {"PR_NOTIFY_CHANGE_PCT": "five"},
		"min cents negative":      {"PR_NOTIFY_CHANGE_MIN_CENTS": "-5"},
		"rises not bool":          {"PR_NOTIFY_RISES": "maybe"},
		"history bad":             {"PR_NOTIFY_LOW_MIN_HISTORY": "a week"},
		"window too short":        {"PR_NOTIFY_TREND_WINDOW": "30m"},
		"trend pct zero":          {"PR_NOTIFY_TREND_PCT": "0"},
		"failures zero":           {"PR_NOTIFY_FAILURES": "0"},
		"bad days":                {"PR_NOTIFY_TREND_COOLDOWN": "xd"},
	}
	for name, m := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(env(m)); err == nil || !strings.HasPrefix(err.Error(), "config:") {
				t.Fatalf("want config error, got %v", err)
			}
		})
	}
}
