package scraper

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	return nil
}

type countingFetcher struct {
	mu    sync.Mutex
	calls []string
	page  Page
}

func (f *countingFetcher) Get(_ context.Context, u string) (Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, u)
	return f.page, nil
}

type allowAll struct{ err error }

func (a allowAll) Allowed(context.Context, string) error { return a.err }

func TestPolite_SpacesRequestsPerHost(t *testing.T) {
	clk := &fakeClock{now: time.Unix(1000, 0)}
	inner := &countingFetcher{page: Page{Status: 200}}
	p := NewPolite(inner, allowAll{}, PoliteConfig{Delay: 10 * time.Second, Jitter: 5 * time.Second})
	p.clock = clk
	p.jitter = func(max time.Duration) time.Duration { return 2 * time.Second }

	ctx := context.Background()
	for _, u := range []string{"https://a.test/1", "https://a.test/2", "https://b.test/1"} {
		if _, err := p.Get(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	// first request: no wait; second to same host: delay+jitter; other host: no wait
	want := []time.Duration{12 * time.Second}
	if len(clk.sleeps) != len(want) || clk.sleeps[0] != want[0] {
		t.Fatalf("sleeps = %v, want %v", clk.sleeps, want)
	}
	if len(inner.calls) != 3 {
		t.Fatalf("calls = %v", inner.calls)
	}

	// after enough time has passed, no wait is needed
	clk.now = clk.now.Add(time.Minute)
	clk.sleeps = nil
	if _, err := p.Get(ctx, "https://a.test/3"); err != nil {
		t.Fatal(err)
	}
	if len(clk.sleeps) != 0 {
		t.Fatalf("unexpected sleeps %v", clk.sleeps)
	}
}

func TestPolite_RobotsDisallowed(t *testing.T) {
	inner := &countingFetcher{page: Page{Status: 200}}
	p := NewPolite(inner, allowAll{err: ErrRobotsDisallowed}, PoliteConfig{})
	_, err := p.Get(context.Background(), "https://a.test/x")
	if !errors.Is(err, ErrRobotsDisallowed) {
		t.Fatalf("err = %v", err)
	}
	if len(inner.calls) != 0 {
		t.Fatal("inner fetcher must not be called")
	}
}

func TestPolite_SerializesConcurrentRequests(t *testing.T) {
	var mu sync.Mutex
	active, maxActive := 0, 0
	inner := fetcherFunc(func(ctx context.Context, u string) (Page, error) {
		mu.Lock()
		active++
		maxActive = max(maxActive, active)
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		return Page{Status: 200}, nil
	})
	p := NewPolite(inner, allowAll{}, PoliteConfig{})
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = p.Get(context.Background(), "https://a.test/x") }()
	}
	wg.Wait()
	if maxActive != 1 {
		t.Fatalf("max concurrent requests = %d, want 1", maxActive)
	}
}

type fetcherFunc func(context.Context, string) (Page, error)

func (f fetcherFunc) Get(ctx context.Context, u string) (Page, error) { return f(ctx, u) }

func TestRobots(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		hits++
		if ua := r.Header.Get("User-Agent"); ua != "TestBot/1.0" {
			t.Errorf("robots UA = %q", ua)
		}
		_, _ = w.Write([]byte("User-agent: *\nDisallow: /gate/\n\nUser-agent: GPTBot\nDisallow: /\n"))
	}))
	defer srv.Close()

	r := NewRobots("TestBot/1.0", srv.Client(), time.Hour)
	ctx := context.Background()
	if err := r.Allowed(ctx, srv.URL+"/p/product-1/"); err != nil {
		t.Fatalf("product page should be allowed: %v", err)
	}
	if err := r.Allowed(ctx, srv.URL+"/gate/x"); !errors.Is(err, ErrRobotsDisallowed) {
		t.Fatalf("gate should be disallowed, got %v", err)
	}
	if hits != 1 {
		t.Fatalf("robots.txt fetched %d times, want 1 (cached)", hits)
	}
}

func TestRobots_MissingAndServerError(t *testing.T) {
	status := http.StatusNotFound
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	defer srv.Close()
	ctx := context.Background()

	if err := NewRobots("X", srv.Client(), time.Hour).Allowed(ctx, srv.URL+"/a"); err != nil {
		t.Fatalf("404 robots.txt means allow all, got %v", err)
	}
	status = http.StatusServiceUnavailable
	if err := NewRobots("X", srv.Client(), time.Hour).Allowed(ctx, srv.URL+"/a"); !errors.Is(err, ErrTransient) {
		t.Fatalf("5xx robots.txt should be transient, got %v", err)
	}
}

type flakyScraper struct {
	errs  []error
	calls int
}

func (f *flakyScraper) Name() string           { return "flaky" }
func (f *flakyScraper) Supports(*url.URL) bool { return true }
func (f *flakyScraper) Fetch(context.Context, string) (Offer, error) {
	i := f.calls
	f.calls++
	if i < len(f.errs) && f.errs[i] != nil {
		return Offer{}, f.errs[i]
	}
	return Offer{PriceCents: 100}, nil
}

func TestWithRetry(t *testing.T) {
	transient := errors.Join(errors.New("x"), ErrTransient)
	blocked := errors.Join(errors.New("x"), ErrBlocked)
	layout := errors.Join(errors.New("x"), ErrLayoutChanged)
	tests := []struct {
		name       string
		errs       []error
		attempts   int
		wantErr    error
		wantCalls  int
		wantSleeps []time.Duration
	}{
		{"success first try", nil, 3, nil, 1, nil},
		{"transient then ok", []error{transient, transient}, 3, nil, 3, []time.Duration{time.Second, 4 * time.Second}},
		{"blocked exhausted", []error{blocked, blocked, blocked}, 3, ErrBlocked, 3, []time.Duration{time.Second, 4 * time.Second}},
		{"layout not retried", []error{layout}, 3, ErrLayoutChanged, 1, nil},
		{"single attempt", []error{transient}, 1, ErrTransient, 1, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clk := &fakeClock{}
			fs := &flakyScraper{errs: tt.errs}
			r := WithRetry(fs, RetryConfig{Attempts: tt.attempts, BaseBackoff: time.Second, Factor: 4})
			r.(*retrying).sleep = clk.Sleep
			_, err := r.Fetch(context.Background(), "u")
			if tt.wantErr == nil && err != nil || tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if fs.calls != tt.wantCalls {
				t.Errorf("calls = %d, want %d", fs.calls, tt.wantCalls)
			}
			if len(clk.sleeps) != len(tt.wantSleeps) {
				t.Fatalf("sleeps = %v, want %v", clk.sleeps, tt.wantSleeps)
			}
			for i := range clk.sleeps {
				if clk.sleeps[i] != tt.wantSleeps[i] {
					t.Errorf("sleep[%d] = %v, want %v", i, clk.sleeps[i], tt.wantSleeps[i])
				}
			}
		})
	}
}

func TestKindOfAndRegistry(t *testing.T) {
	if KindOf(nil) != "" || KindOf(errors.New("x")) != KindInternal || KindOf(context.DeadlineExceeded) != KindTransient {
		t.Fatal("KindOf basic mapping wrong")
	}
	reg := NewRegistry(&flakyScraper{})
	s, norm, err := reg.Resolve("https://shop.test/p/1#frag")
	if err != nil || s.Name() != "flaky" || norm != "https://shop.test/p/1" {
		t.Fatalf("Resolve = %v %q %v", s, norm, err)
	}
	if _, _, err := reg.Resolve("not a url"); !errors.Is(err, ErrInvalidURL) {
		t.Fatalf("invalid URL err = %v", err)
	}
	if _, ok := reg.ByName("flaky"); !ok {
		t.Fatal("ByName failed")
	}
}

func TestRobots_BotWallIsBlockedNotCached(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	r := NewRobots("X", srv.Client(), time.Hour)
	for range 2 {
		if err := r.Allowed(context.Background(), srv.URL+"/p/1"); !errors.Is(err, ErrBlocked) {
			t.Fatalf("403 robots.txt should be ErrBlocked, got %v", err)
		}
	}
	if hits != 2 {
		t.Fatalf("blocked robots result must not be cached, hits=%d", hits)
	}
}
