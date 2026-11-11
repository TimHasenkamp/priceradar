// Package config loads the backend configuration from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
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
	UserAgent      string
	LogLevel       slog.Level
	LogFormat      string // "json" or "text"
}

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
		UserAgent:      l.str("PR_USER_AGENT", "PriceRadar/0.1 (+self-hosted price tracker)"),
		LogFormat:      strings.ToLower(l.str("LOG_FORMAT", "json")),
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

func (l *loader) dur(k string, def time.Duration) time.Duration {
	v := l.str(k, "")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
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
