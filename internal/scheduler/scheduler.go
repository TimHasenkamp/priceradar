// Package scheduler runs all scrapes through a single serial worker:
// the periodic full run and manual requests (add product, refresh).
// Manual jobs are served between products of a running periodic cycle.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"priceradar/internal/scraper"
	"priceradar/internal/store"
)

// Config for the Service.
type Config struct {
	Interval time.Duration
	// Retry is applied to periodic scrapes only; manual scrapes try once so
	// the HTTP request returns in reasonable time.
	Retry scraper.RetryConfig
	// Observer, if set, is told about every recorded check (notifications).
	Observer CheckObserver
}

// CheckObserver is called on the scrape worker after a check was stored.
// It must be quick and must not block; errors are its own business.
type CheckObserver interface {
	CheckRecorded(ctx context.Context, c store.PriceCheck)
}

// Service owns the scrape worker.
type Service struct {
	store *store.Store
	reg   *scraper.Registry
	cfg   Config
	log   *slog.Logger
	now   func() time.Time

	jobs    chan *job
	stopped chan struct{} // closed when Run returns
}

// ErrShuttingDown is returned by Add/Refresh once the worker has stopped.
var ErrShuttingDown = errors.New("scheduler: shutting down")

type job struct {
	ctx  context.Context
	run  func(context.Context)
	done chan struct{}
	ran  bool // set by the worker before done is closed
}

// New creates the service. Call Run to start the worker.
func New(st *store.Store, reg *scraper.Registry, cfg Config, log *slog.Logger) *Service {
	return &Service{store: st, reg: reg, cfg: cfg, log: log, now: time.Now, jobs: make(chan *job), stopped: make(chan struct{})}
}

// Run processes jobs and the periodic cycle until ctx is cancelled.
// The first periodic cycle starts immediately. Run must be called once.
func (s *Service) Run(ctx context.Context) {
	defer close(s.stopped)
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	s.cycle(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-s.jobs:
			s.exec(j)
		case <-ticker.C:
			s.cycle(ctx)
		}
	}
}

func (s *Service) exec(j *job) {
	defer close(j.done)
	if j.ctx.Err() != nil {
		return // caller gave up while waiting
	}
	j.run(j.ctx)
	j.ran = true
}

// drain runs all manual jobs that are currently waiting.
func (s *Service) drain() {
	for {
		select {
		case j := <-s.jobs:
			s.exec(j)
		default:
			return
		}
	}
}

func (s *Service) cycle(ctx context.Context) {
	products, err := s.store.ListActiveProducts(ctx)
	if err != nil {
		s.log.Error("list active products", "err", err)
		return
	}
	s.log.Info("scrape cycle started", "products", len(products))
	start, failed, skipped := s.now(), 0, 0
	for _, snap := range products {
		s.drain()
		if ctx.Err() != nil {
			return
		}
		// Re-read: a manual job served by drain() may have refreshed,
		// paused or deleted this product in the meantime.
		p, err := s.store.GetProduct(ctx, snap.ID)
		if err != nil || p.Paused {
			skipped++
			continue
		}
		// Skip products checked recently (e.g. right after a restart or a
		// manual refresh). Half the interval keeps regular cycles unaffected.
		if p.LastCheckedAt != nil && s.now().Sub(*p.LastCheckedAt) < s.cfg.Interval/2 {
			skipped++
			continue
		}
		if c := s.check(ctx, p, true); !c.Success {
			failed++
		}
	}
	s.log.Info("scrape cycle finished", "products", len(products), "skipped", skipped, "failed", failed, "took", s.now().Sub(start).Round(time.Second))
}

// submit hands fn to the worker and waits until it has run.
func (s *Service) submit(ctx context.Context, fn func(context.Context)) error {
	j := &job{ctx: ctx, run: fn, done: make(chan struct{})}
	select {
	case s.jobs <- j:
	case <-s.stopped:
		return ErrShuttingDown
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-j.done:
		if j.ran {
			return nil // finished, even if the deadline expired just now
		}
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// fetch calls the scraper, turning a panic (e.g. on hostile HTML) into an error.
func fetch(ctx context.Context, sc scraper.Scraper, u string) (o scraper.Offer, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("scraper panic: %v", r)
		}
	}()
	return sc.Fetch(ctx, u)
}

// check scrapes one product and records the result.
func (s *Service) check(ctx context.Context, p store.Product, withRetry bool) store.PriceCheck {
	log := s.log.With("product_id", p.ID, "url", p.URL)
	sc, ok := s.reg.ByName(p.Shop)
	if !ok {
		return s.record(ctx, log, p.ID, scraper.Offer{}, fmt.Errorf("no scraper %q", p.Shop), 0)
	}
	if withRetry {
		sc = scraper.WithRetry(sc, s.cfg.Retry)
	}
	start := s.now()
	offer, err := fetch(ctx, sc, p.URL)
	if err != nil && errors.Is(err, context.Canceled) {
		// Shutdown or client gone: not a real failure, don't count it.
		log.Info("scrape cancelled", "took_ms", s.now().Sub(start).Milliseconds())
		return store.PriceCheck{ProductID: p.ID}
	}
	return s.record(ctx, log, p.ID, offer, err, s.now().Sub(start))
}

func (s *Service) record(ctx context.Context, log *slog.Logger, id int64, o scraper.Offer, scrapeErr error, took time.Duration) store.PriceCheck {
	c := store.PriceCheck{ProductID: id, CheckedAt: s.now().UTC(), DurationMS: took.Milliseconds()}
	if scrapeErr == nil {
		c.Success = true
		c.PriceCents, c.OldPriceCents, c.RRPCents = &o.PriceCents, o.OldPriceCents, o.RRPCents
		c.Availability = &o.Availability
		for _, w := range o.Warnings {
			log.Warn("parser warning", "msg", w)
		}
		if err := s.store.UpdateProductMeta(ctx, id, store.ProductMeta{SKU: o.SKU, Name: o.Name, ImageURL: o.ImageURL, Currency: o.Currency}); err != nil {
			log.Error("update product meta", "err", err)
		}
		log.Info("scrape ok", "price_cents", o.PriceCents, "availability", o.Availability, "took_ms", c.DurationMS)
	} else {
		kind, msg := scraper.KindOf(scrapeErr), scrapeErr.Error()
		c.ErrorKind, c.ErrorMessage = &kind, &msg
		level := slog.LevelWarn
		if kind == scraper.KindLayoutChanged || kind == scraper.KindInternal {
			level = slog.LevelError
		}
		log.Log(ctx, level, "scrape failed", "kind", kind, "err", scrapeErr, "took_ms", c.DurationMS)
	}
	// Record even if the request context was cancelled after the scrape finished.
	saved, err := s.store.RecordCheck(context.WithoutCancel(ctx), c)
	if err != nil {
		log.Error("record check", "err", err)
		return c
	}
	s.observe(context.WithoutCancel(ctx), log, saved)
	return saved
}

// observe runs the observer; a panic in it must never take down the worker.
func (s *Service) observe(ctx context.Context, log *slog.Logger, c store.PriceCheck) {
	if s.cfg.Observer == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Error("check observer panic", "panic", r)
		}
	}()
	s.cfg.Observer.CheckRecorded(ctx, c)
}

// Refresh scrapes one product now (single attempt) and returns the check.
// A failed scrape is not an error: it is returned as a check with Success=false.
func (s *Service) Refresh(ctx context.Context, id int64) (store.PriceCheck, error) {
	p, err := s.store.GetProduct(ctx, id)
	if err != nil {
		return store.PriceCheck{}, err
	}
	var c store.PriceCheck
	if err := s.submit(ctx, func(ctx context.Context) { c = s.check(ctx, p, false) }); err != nil {
		return store.PriceCheck{}, err
	}
	return c, nil
}

// ErrScrape wraps scraper errors returned by Add.
var ErrScrape = errors.New("scrape failed")

// Add scrapes rawURL and, on success, stores it as a new product together with
// its first price check. Nothing is stored if the scrape fails.
func (s *Service) Add(ctx context.Context, rawURL string, targetCents *int64) (store.Product, error) {
	sc, normURL, err := s.reg.Resolve(rawURL)
	if err != nil {
		return store.Product{}, err
	}
	existing, err := s.store.ListProducts(ctx)
	if err != nil {
		return store.Product{}, err
	}
	for _, p := range existing {
		if p.URL == normURL {
			return store.Product{}, fmt.Errorf("%s: %w", normURL, store.ErrDuplicate)
		}
	}

	var (
		offer    scraper.Offer
		fetchErr error
		took     time.Duration
	)
	if err := s.submit(ctx, func(ctx context.Context) {
		start := s.now()
		offer, fetchErr = fetch(ctx, sc, normURL)
		took = s.now().Sub(start)
	}); err != nil {
		return store.Product{}, err
	}
	if fetchErr != nil && errors.Is(fetchErr, context.Canceled) {
		return store.Product{}, fetchErr
	}
	if fetchErr != nil {
		s.log.Warn("add product: scrape failed", "url", normURL, "kind", scraper.KindOf(fetchErr), "err", fetchErr)
		return store.Product{}, fmt.Errorf("%w: %w", ErrScrape, fetchErr)
	}

	p, err := s.store.CreateProduct(ctx, store.NewProduct{
		Shop: sc.Name(), URL: normURL, SKU: offer.SKU, Name: offer.Name,
		ImageURL: offer.ImageURL, Currency: offer.Currency, TargetPriceCents: targetCents,
	})
	if err != nil {
		return store.Product{}, err
	}
	s.log.Info("product added", "product_id", p.ID, "url", normURL, "name", offer.Name)
	s.record(ctx, s.log.With("product_id", p.ID, "url", p.URL), p.ID, offer, nil, took)
	return s.store.GetProduct(ctx, p.ID)
}
