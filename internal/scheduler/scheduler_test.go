package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"sync"
	"testing"
	"time"

	"priceradar/internal/scraper"
	"priceradar/internal/store"
)

type fakeScraper struct {
	mu    sync.Mutex
	offer scraper.Offer
	err   error
	calls []string
}

func (f *fakeScraper) Name() string             { return "fake" }
func (f *fakeScraper) Supports(u *url.URL) bool { return u.Host == "shop.test" }
func (f *fakeScraper) Fetch(_ context.Context, u string) (scraper.Offer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, u)
	return f.offer, f.err
}
func (f *fakeScraper) set(o scraper.Offer, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.offer, f.err = o, err
}
func (f *fakeScraper) callCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

func setup(t *testing.T) (*Service, *store.Store, *fakeScraper) {
	t.Helper()
	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fs := &fakeScraper{offer: scraper.Offer{Name: "Thing", SKU: "S1", ImageURL: "https://img/1.jpg", Currency: "EUR", PriceCents: 1999, Availability: scraper.AvailabilityInStock}}
	svc := New(st, scraper.NewRegistry(fs), Config{Interval: time.Hour, Retry: scraper.RetryConfig{Attempts: 1}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return svc, st, fs
}

func startWorker(t *testing.T, svc *Service) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { svc.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

func TestAdd(t *testing.T) {
	svc, st, fs := setup(t)
	startWorker(t, svc)
	ctx := context.Background()
	target := int64(1500)

	p, err := svc.Add(ctx, "https://shop.test/p/1#frag", &target)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if p.URL != "https://shop.test/p/1" || p.Name != "Thing" || p.Shop != "fake" || *p.TargetPriceCents != 1500 || p.LastSuccessAt == nil {
		t.Fatalf("unexpected product %+v", p)
	}
	checks, _ := st.ListChecks(ctx, p.ID, 10)
	if len(checks) != 1 || !checks[0].Success || *checks[0].PriceCents != 1999 {
		t.Fatalf("checks = %+v", checks)
	}

	if _, err := svc.Add(ctx, "https://shop.test/p/1", nil); !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := svc.Add(ctx, "https://other.test/p/1", nil); !errors.Is(err, scraper.ErrUnsupported) {
		t.Fatalf("unsupported: %v", err)
	}

	fs.set(scraper.Offer{}, errors.Join(errors.New("x"), scraper.ErrLayoutChanged))
	if _, err := svc.Add(ctx, "https://shop.test/p/2", nil); !errors.Is(err, ErrScrape) || !errors.Is(err, scraper.ErrLayoutChanged) {
		t.Fatalf("scrape failure: %v", err)
	}
	all, _ := st.ListProducts(ctx)
	if len(all) != 1 {
		t.Fatalf("failed add must not store a product, have %d", len(all))
	}
}

func TestRefresh(t *testing.T) {
	svc, st, fs := setup(t)
	startWorker(t, svc)
	ctx := context.Background()
	p, err := svc.Add(ctx, "https://shop.test/p/1", nil)
	if err != nil {
		t.Fatal(err)
	}

	fs.set(scraper.Offer{}, errors.Join(errors.New("challenge"), scraper.ErrBlocked))
	c, err := svc.Refresh(ctx, p.ID)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if c.Success || c.ErrorKind == nil || *c.ErrorKind != scraper.KindBlocked || c.ID == 0 {
		t.Fatalf("check = %+v", c)
	}
	got, _ := st.GetProduct(ctx, p.ID)
	if got.ConsecutiveFailures != 1 || *got.LastErrorKind != scraper.KindBlocked {
		t.Fatalf("product after failure = %+v", got)
	}

	fs.set(scraper.Offer{Name: "Renamed by shop", PriceCents: 1799, Currency: "EUR", Availability: "in_stock"}, nil)
	c, err = svc.Refresh(ctx, p.ID)
	if err != nil || !c.Success || *c.PriceCents != 1799 {
		t.Fatalf("refresh ok: %+v %v", c, err)
	}
	got, _ = st.GetProduct(ctx, p.ID)
	if got.ConsecutiveFailures != 0 || got.Name != "Renamed by shop" {
		t.Fatalf("product after success = %+v", got)
	}

	if _, err := svc.Refresh(ctx, 999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing product: %v", err)
	}
}

func TestRefresh_CallerTimeout(t *testing.T) {
	svc, _, _ := setup(t) // worker NOT started: job can never be accepted
	ctx := context.Background()
	st := svc.store
	p, _ := st.CreateProduct(ctx, store.NewProduct{Shop: "fake", URL: "https://shop.test/p/1", Name: "x"})
	tctx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err := svc.Refresh(tctx, p.ID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestCycle(t *testing.T) {
	svc, st, fs := setup(t)
	ctx := context.Background()
	mk := func(u string) store.Product {
		p, err := st.CreateProduct(ctx, store.NewProduct{Shop: "fake", URL: u, Name: u})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	due := mk("https://shop.test/p/due")
	paused := mk("https://shop.test/p/paused")
	recent := mk("https://shop.test/p/recent")
	unknownShop, _ := st.CreateProduct(ctx, store.NewProduct{Shop: "gone", URL: "https://gone.test/p/1", Name: "g"})
	yes := true
	if _, err := st.UpdateProduct(ctx, paused.ID, store.ProductPatch{Paused: &yes}); err != nil {
		t.Fatal(err)
	}
	pr := int64(1)
	if _, err := st.RecordCheck(ctx, store.PriceCheck{ProductID: recent.ID, Success: true, PriceCents: &pr}); err != nil {
		t.Fatal(err)
	}

	svc.cycle(ctx)

	if fs.callCount() != 1 || fs.calls[0] != due.URL {
		t.Fatalf("scraped %v, want only %s", fs.calls, due.URL)
	}
	g, _ := st.GetProduct(ctx, unknownShop.ID)
	if g.ConsecutiveFailures != 1 || *g.LastErrorKind != scraper.KindInternal {
		t.Fatalf("unknown shop product = %+v", g)
	}
	pp, _ := st.GetProduct(ctx, paused.ID)
	if pp.LastCheckedAt != nil {
		t.Fatal("paused product must not be checked")
	}
}

type panicScraper struct{ fakeScraper }

func (p *panicScraper) Fetch(context.Context, string) (scraper.Offer, error) { panic("bad html") }

func TestCheck_PanicAndCancel(t *testing.T) {
	svc, st, fs := setup(t)
	ctx := context.Background()
	p, _ := st.CreateProduct(ctx, store.NewProduct{Shop: "fake", URL: "https://shop.test/p/1", Name: "x"})

	// cancelled scrape: nothing recorded, failure counter untouched
	fs.set(scraper.Offer{}, context.Canceled)
	if c := svc.check(ctx, p, false); c.ID != 0 {
		t.Fatalf("cancelled scrape was recorded: %+v", c)
	}
	if got, _ := st.GetProduct(ctx, p.ID); got.ConsecutiveFailures != 0 || got.LastCheckedAt != nil {
		t.Fatalf("cancelled scrape changed product: %+v", got)
	}

	// panic inside a scraper becomes an internal failure instead of crashing
	svc.reg = scraper.NewRegistry(&panicScraper{})
	if c := svc.check(ctx, p, false); c.Success || c.ErrorKind == nil || *c.ErrorKind != scraper.KindInternal {
		t.Fatalf("panic check = %+v", c)
	}
}

func TestSubmit_AfterShutdown(t *testing.T) {
	svc, st, _ := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { svc.Run(ctx); close(done) }()
	cancel()
	<-done
	p, _ := st.CreateProduct(context.Background(), store.NewProduct{Shop: "fake", URL: "https://shop.test/p/9", Name: "x"})
	if _, err := svc.Refresh(context.Background(), p.ID); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("err = %v, want ErrShuttingDown", err)
	}
}
