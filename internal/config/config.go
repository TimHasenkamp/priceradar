// Package config loads the backend configuration from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"priceradar/internal/notify"
)

// Config is the complete backend configuration. See .env.example.
type Config struct {
	ListenAddr     string
	DBPath         string
	ScrapeInterval time.Duration
	RequestDelay   time.Duration
	RequestJitter  time.Duration
	MaxRetries     int
	RetryBackoff   time.Duration
	FetcherURL     string
	FetchTimeout   time.Duration
	FetcherToken   string
	UserAgent      string
	LogLevel       slog.Level
	LogFormat      string // "json" or "text"

	// Notifications. Gotify is used only if both URL and token are set.
	GotifyURL   string // without trailing slash
	GotifyToken string
	PublicURL   string // web UI base URL for links, without trailing slash
	Notify      notify.Rules
}

// GotifyConfigured reports whether notifications can be sent.
func (c Config) GotifyConfigured() bool { return c.GotifyURL != "" && c.GotifyToken != "" }

// Load reads the configuration from the environment (getenv is os.Getenv in production).
func Load(getenv func(string) string) (Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	l := loader{getenv: getenv}
	c := Config{
		ListenAddr:     l.str("PR_LISTEN_ADDR", ":8080"),
		DBPath:         l.str("PR_DB_PATH", "priceradar.db"),
		ScrapeInterval: l.dur("PR_SCRAPE_INTERVAL", 6*time.Hour),
		RequestDelay:   l.dur("PR_REQUEST_DELAY", 15*time.Second),
		RequestJitter:  l.dur("PR_REQUEST_JITTER", 10*time.Second),
		MaxRetries:     l.int("PR_MAX_RETRIES", 3),
		RetryBackoff:   l.dur("PR_RETRY_BACKOFF", 30*time.Second),
		FetcherURL:     l.str("PR_FETCHER_URL", "http://localhost:8000"),
		FetchTimeout:   l.dur("PR_FETCH_TIMEOUT", 60*time.Second),
		FetcherToken:   l.str("PR_FETCHER_TOKEN", ""),
		UserAgent:      l.str("PR_USER_AGENT", "PriceRadar/0.1 (+self-hosted price tracker)"),
		LogFormat:      strings.ToLower(l.str("LOG_FORMAT", "json")),
		GotifyURL:      l.httpURL("PR_GOTIFY_URL"),
		GotifyToken:    l.str("PR_GOTIFY_TOKEN", ""),
		PublicURL:      l.httpURL("PR_PUBLIC_URL"),
	}
	def := notify.DefaultRules()
	c.Notify = notify.Rules{
		ChangePct:      l.float("PR_NOTIFY_CHANGE_PCT", def.ChangePct),
		ChangeMinCents: int64(l.int("PR_NOTIFY_CHANGE_MIN_CENTS", int(def.ChangeMinCents))),
		Rises:          l.bool("PR_NOTIFY_RISES", def.Rises),
		LowMinHistory:  l.dur("PR_NOTIFY_LOW_MIN_HISTORY", def.LowMinHistory),
		TrendWindow:    l.dur("PR_NOTIFY_TREND_WINDOW", def.TrendWindow),
		TrendPct:       l.float("PR_NOTIFY_TREND_PCT", def.TrendPct),
		TrendCooldown:  l.dur("PR_NOTIFY_TREND_COOLDOWN", def.TrendCooldown),
		Failures:       l.int("PR_NOTIFY_FAILURES", def.Failures),
		PublicURL:      c.PublicURL,
	}
	n := c.Notify
	if n.ChangePct < 0 || n.ChangePct > 100 {
		l.errs = append(l.errs, "PR_NOTIFY_CHANGE_PCT must be 0..100")
	}
	if n.ChangeMinCents < 0 {
		l.errs = append(l.errs, "PR_NOTIFY_CHANGE_MIN_CENTS must be >= 0")
	}
	if n.TrendWindow < time.Hour {
		l.errs = append(l.errs, "PR_NOTIFY_TREND_WINDOW must be >= 1h")
	}
	if n.TrendPct <= 0 || n.TrendPct > 100 {
		l.errs = append(l.errs, "PR_NOTIFY_TREND_PCT must be > 0 and <= 100")
	}
	if n.Failures < 1 {
		l.errs = append(l.errs, "PR_NOTIFY_FAILURES must be >= 1")
	}
	if err := c.LogLevel.UnmarshalText([]byte(l.str("LOG_LEVEL", "info"))); err != nil {
		l.errs = append(l.errs, fmt.Sprintf("LOG_LEVEL: %v", err))
	}
	switch {
	case c.ScrapeInterval < time.Minute:
		l.errs = append(l.errs, "PR_SCRAPE_INTERVAL must be >= 1m")
	case c.MaxRetries < 0 || c.MaxRetries > 10:
		l.errs = append(l.errs, "PR_MAX_RETRIES must be 0..10")
	case c.LogFormat != "json" && c.LogFormat != "text":
		l.errs = append(l.errs, "LOG_FORMAT must be json or text")
	}
	if len(l.errs) > 0 {
		return Config{}, fmt.Errorf("config: %s", strings.Join(l.errs, "; "))
	}
	return c, nil
}

type loader struct {
	getenv func(string) string
	errs   []string
}

func (l *loader) str(k, def string) string {
	if v := strings.TrimSpace(l.getenv(k)); v != "" {
		return v
	}
	return def
}

// daysRe matches a day component such as "7d" or "1.5d".
var daysRe = regexp.MustCompile(`(\d+(?:\.\d+)?)d`)

// parseDuration is time.ParseDuration plus a "d" (24h) unit, e.g. "7d", "1d12h".
func parseDuration(v string) (time.Duration, error) {
	var convErr error
	v = daysRe.ReplaceAllStringFunc(v, func(m string) string {
		f, err := strconv.ParseFloat(strings.TrimSuffix(m, "d"), 64)
		if err != nil {
			convErr = err
		}
		return strconv.FormatFloat(f*24, 'f', -1, 64) + "h"
	})
	if convErr != nil {
		return 0, convErr
	}
	return time.ParseDuration(v)
}

func (l *loader) dur(k string, def time.Duration) time.Duration {
	v := l.str(k, "")
	if v == "" {
		return def
	}
	d, err := parseDuration(v)
	if err != nil || d < 0 {
		l.errs = append(l.errs, fmt.Sprintf("%s: invalid duration %q", k, v))
		return def
	}
	return d
}

func (l *loader) int(k string, def int) int {
	v := l.str(k, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Sprintf("%s: invalid integer %q", k, v))
		return def
	}
	return n
}

func (l *loader) float(k string, def float64) float64 {
	v := l.str(k, "")
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		l.errs = append(l.errs, fmt.Sprintf("%s: invalid number %q", k, v))
		return def
	}
	return f
}

func (l *loader) bool(k string, def bool) bool {
	v := l.str(k, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Sprintf("%s: invalid boolean %q", k, v))
		return def
	}
	return b
}

// httpURL reads an optional absolute http(s) URL and strips trailing slashes.
func (l *loader) httpURL(k string) string {
	v := strings.TrimRight(l.str(k, ""), "/")
	if v == "" {
		return ""
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		l.errs = append(l.errs, fmt.Sprintf("%s: must be an absolute http(s) URL, got %q", k, v))
		return ""
	}
	return v
}
