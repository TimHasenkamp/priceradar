// Command server runs the PriceRadar API and the background scraper.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"priceradar/internal/api"
	"priceradar/internal/config"
	"priceradar/internal/notify"
	"priceradar/internal/scheduler"
	"priceradar/internal/scraper"
	"priceradar/internal/scraper/otto"
	"priceradar/internal/store"
)

func main() {
	// `server healthcheck` is used by the Docker HEALTHCHECK (no curl in distroless).
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(2)
	}
	log := newLogger(cfg)
	if err := run(cfg, log); err != nil {
		log.Error("server stopped with error", "err", err)
		os.Exit(1)
	}
}

func run(cfg config.Config, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	// Messages queued when the process last stopped will never be sent.
	if n, err := st.FailPendingNotifications(ctx, "nicht gesendet: Server wurde beendet"); err != nil {
		return err
	} else if n > 0 {
		log.Warn("marked stale pending notifications as failed", "count", n)
	}

	// Notifications: without Gotify, events are still recorded as "skipped".
	var (
		notifier   notify.Notifier // stays a nil interface when not configured
		queue      notify.Enqueuer
		dispatcher *notify.Dispatcher
	)
	if cfg.GotifyConfigured() {
		g := notify.NewGotify(cfg.GotifyURL, cfg.GotifyToken)
		dispatcher = notify.NewDispatcher(g, st, log.With("component", "notify"))
		notifier, queue = g, dispatcher
	} else if cfg.GotifyURL != "" || cfg.GotifyToken != "" {
		log.Warn("Gotify needs both PR_GOTIFY_URL and PR_GOTIFY_TOKEN; notifications are not sent")
	}
	notifications := notify.NewService(st, cfg.Notify, queue, log.With("component", "notify"))

	sidecar := &scraper.SidecarFetcher{BaseURL: cfg.FetcherURL, Timeout: cfg.FetchTimeout, WaitSelector: otto.WaitSelector, Token: cfg.FetcherToken}
	polite := scraper.NewPolite(sidecar, scraper.NewRobots(cfg.UserAgent, nil, 24*time.Hour),
		scraper.PoliteConfig{Delay: cfg.RequestDelay, Jitter: cfg.RequestJitter})
	// New shops: construct their scraper here and add it to the registry.
	reg := scraper.NewRegistry(otto.New(polite))

	svc := scheduler.New(st, reg, scheduler.Config{
		Interval: cfg.ScrapeInterval,
		Retry:    scraper.RetryConfig{Attempts: cfg.MaxRetries + 1, BaseBackoff: cfg.RetryBackoff, Factor: 4},
		Observer: notifications,
	}, log.With("component", "scheduler"))

	scrapeTimeout := 3 * time.Minute
	srv := &http.Server{
		Addr: cfg.ListenAddr,
		Handler: (&api.Server{
			Store: st, Scrapes: svc, Fetcher: sidecar, Log: log.With("component", "api"), ScrapeTimeout: scrapeTimeout,
			Notifier: notifier, PublicURL: cfg.PublicURL,
		}).Handler(),
		// Request contexts derive from the signal context, so in-flight scrapes
		// (up to 3 min) are cancelled on SIGTERM instead of outliving the DB.
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      scrapeTimeout + 30*time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	// The dispatcher gets its own context: it is stopped only after the
	// scheduler (its producer) has finished, then drains its queue.
	dispCtx, stopDispatcher := context.WithCancel(context.Background())
	defer stopDispatcher()
	dispDone := make(chan struct{})
	if dispatcher != nil {
		go func() { defer close(dispDone); dispatcher.Run(dispCtx) }()
	} else {
		close(dispDone)
	}
	stopNotify := func() { stopDispatcher(); <-dispDone }

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); svc.Run(ctx) }()

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.ListenAddr, "db", cfg.DBPath, "fetcher", cfg.FetcherURL,
			"fetcher_token", cfg.FetcherToken != "", "interval", cfg.ScrapeInterval.String(),
			"gotify", cfg.GotifyConfigured(), "public_url", cfg.PublicURL)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
		close(errc)
	}()

	select {
	case err := <-errc:
		stop()
		wg.Wait()
		stopNotify()
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	wg.Wait()
	stopNotify()
	return err
}

func healthcheck() int {
	addr := os.Getenv("PR_LISTEN_ADDR")
	if addr == "" || addr[0] == ':' {
		addr = "127.0.0.1" + addr
		if addr == "127.0.0.1" {
			addr += ":8080"
		}
	}
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get("http://" + addr + "/api/healthz")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}
