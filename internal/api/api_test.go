package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"priceradar/internal/notify"
	"priceradar/internal/scheduler"
	"priceradar/internal/scraper"
	"priceradar/internal/store"
)

type fakeScrapes struct {
	st         *store.Store
	addErr     error
	refreshErr error
}

func (f *fakeScrapes) Add(ctx context.Context, u string, target *int64) (store.Product, error) {
	if f.addErr != nil {
		return store.Product{}, f.addErr
	}
	p, err := f.st.CreateProduct(ctx, store.NewProduct{Shop: "otto", URL: u, Name: "Thing", Currency: "EUR", TargetPriceCents: target})
	if err != nil {
		return p, err
	}
	price := int64(1000)
	_, err = f.st.RecordCheck(ctx, store.PriceCheck{ProductID: p.ID, Success: true, PriceCents: &price})
	return p, err
}

func (f *fakeScrapes) Refresh(ctx context.Context, id int64) (store.PriceCheck, error) {
	if f.refreshErr != nil {
		return store.PriceCheck{}, f.refreshErr
	}
	if _, err := f.st.GetProduct(ctx, id); err != nil {
		return store.PriceCheck{}, err
	}
	kind, msg := scraper.KindLayoutChanged, "no price"
	return f.st.RecordCheck(ctx, store.PriceCheck{ProductID: id, ErrorKind: &kind, ErrorMessage: &msg})
}

type pinger struct{ err error }

func (p pinger) Ping(context.Context) error { return p.err }

func newTestServer(t *testing.T) (*httptest.Server, *fakeScrapes) {
	t.Helper()
	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fs := &fakeScrapes{st: st}
	s := &Server{Store: st, Scrapes: fs, Fetcher: pinger{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, fs
}

func do(t *testing.T, srv *httptest.Server, method, path, body string) (int, map[string]any, []any) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) == 0 {
		return resp.StatusCode, nil, nil
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("%s %s: content-type %q", method, path, ct)
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil {
		return resp.StatusCode, obj, nil
	}
	var arr []any
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatalf("bad json %s", raw)
	}
	return resp.StatusCode, nil, arr
}

func errCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func TestProductLifecycle(t *testing.T) {
	srv, _ := newTestServer(t)

	code, _, list := do(t, srv, "GET", "/api/products", "")
	if code != 200 || len(list) != 0 {
		t.Fatalf("empty list: %d %v", code, list)
	}

	code, p, _ := do(t, srv, "POST", "/api/products", `{"url":"https://www.otto.de/p/x-CS1/","target_price_cents":1200}`)
	if code != 201 || p["name"] != "Thing" || p["target_reached"] != true {
		t.Fatalf("create: %d %v", code, p)
	}
	stats := p["stats"].(map[string]any)
	if stats["current_cents"] != 1000.0 || stats["previous_cents"] != nil {
		t.Fatalf("stats = %v", stats)
	}
	id := int(p["id"].(float64))
	base := fmt.Sprintf("/api/products/%d", id)

	code, p, _ = do(t, srv, "PATCH", base, `{"custom_name":"Mein Drucker","paused":true,"target_price_cents":null}`)
	if code != 200 || p["name"] != "Mein Drucker" || p["original_name"] != "Thing" || p["paused"] != true || p["target_price_cents"] != nil {
		t.Fatalf("patch: %d %v", code, p)
	}
	code, p, _ = do(t, srv, "PATCH", base, `{"custom_name":null}`)
	if code != 200 || p["name"] != "Thing" || p["paused"] != true {
		t.Fatalf("patch clear: %d %v", code, p)
	}

	code, _, prices := do(t, srv, "GET", base+"/prices", "")
	if code != 200 || len(prices) != 1 {
		t.Fatalf("prices: %d %v", code, prices)
	}
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if code, _, prices = do(t, srv, "GET", base+"/prices?from="+future, ""); code != 200 || len(prices) != 0 {
		t.Fatalf("prices from future: %d %v", code, prices)
	}

	code, r, _ := do(t, srv, "POST", base+"/refresh", "")
	check := r["check"].(map[string]any)
	if code != 200 || check["success"] != false || check["error_kind"] != "layout_changed" {
		t.Fatalf("refresh: %d %v", code, r)
	}
	if prod := r["product"].(map[string]any); prod["consecutive_failures"] != 1.0 {
		t.Fatalf("refresh product: %v", prod)
	}

	code, _, checks := do(t, srv, "GET", base+"/checks?limit=10", "")
	if code != 200 || len(checks) != 2 || checks[0].(map[string]any)["success"] != false {
		t.Fatalf("checks: %d %v", code, checks)
	}

	if code, _, _ = do(t, srv, "DELETE", base, ""); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code, m, _ := do(t, srv, "GET", base, ""); code != 404 || errCode(m) != "not_found" {
		t.Fatalf("get deleted: %d %v", code, m)
	}
}

func TestErrors(t *testing.T) {
	srv, fs := newTestServer(t)
	tests := []struct {
		name, method, path, body string
		setup                    func()
		wantCode                 int
		wantErr                  string
	}{
		{"bad json", "POST", "/api/products", `{`, nil, 400, "invalid_request"},
		{"missing url", "POST", "/api/products", `{}`, nil, 400, "invalid_request"},
		{"bad target", "POST", "/api/products", `{"url":"x","target_price_cents":0}`, nil, 400, "invalid_request"},
		{"invalid url", "POST", "/api/products", `{"url":"ftp://x"}`, func() { fs.addErr = scraper.ErrInvalidURL }, 400, "invalid_request"},
		{"shutting down", "POST", "/api/products", `{"url":"u"}`, func() { fs.addErr = scheduler.ErrShuttingDown }, 503, "unavailable"},
		{"refresh shutting down", "POST", "/api/products/1/refresh", "", func() { fs.refreshErr = scheduler.ErrShuttingDown }, 503, "unavailable"},
		{"patch paused null", "PATCH", "/api/products/1", `{"paused":null}`, nil, 400, "invalid_request"},
		{"unsupported", "POST", "/api/products", `{"url":"https://amazon.de/x"}`, func() { fs.addErr = scraper.ErrUnsupported }, 422, "unsupported_shop"},
		{"duplicate", "POST", "/api/products", `{"url":"u"}`, func() { fs.addErr = store.ErrDuplicate }, 409, "duplicate"},
		{"scrape failed", "POST", "/api/products", `{"url":"u"}`, func() {
			fs.addErr = fmt.Errorf("%w: %w", scheduler.ErrScrape, errors.Join(errors.New("x"), scraper.ErrBlocked))
		}, 502, "scrape_failed"},
		{"add timeout", "POST", "/api/products", `{"url":"u"}`, func() { fs.addErr = context.DeadlineExceeded }, 504, "timeout"},
		{"add internal", "POST", "/api/products", `{"url":"u"}`, func() { fs.addErr = errors.New("disk on fire") }, 500, "internal"},
		{"bad id", "GET", "/api/products/abc", "", nil, 400, "invalid_request"},
		{"missing", "GET", "/api/products/99", "", nil, 404, "not_found"},
		{"patch missing", "PATCH", "/api/products/99", `{"paused":true}`, nil, 404, "not_found"},
		{"patch unknown field", "PATCH", "/api/products/1", `{"color":"red"}`, nil, 400, "invalid_request"},
		{"patch bad type", "PATCH", "/api/products/1", `{"paused":"yes"}`, nil, 400, "invalid_request"},
		{"patch negative target", "PATCH", "/api/products/1", `{"target_price_cents":-1}`, nil, 400, "invalid_request"},
		{"delete missing", "DELETE", "/api/products/99", "", nil, 404, "not_found"},
		{"prices bad from", "GET", "/api/products/1/prices?from=yesterday", "", nil, 400, "invalid_request"},
		{"prices missing", "GET", "/api/products/99/prices", "", nil, 404, "not_found"},
		{"checks bad limit", "GET", "/api/products/1/checks?limit=0", "", nil, 400, "invalid_request"},
		{"checks missing", "GET", "/api/products/99/checks", "", nil, 404, "not_found"},
		{"refresh missing", "POST", "/api/products/99/refresh", "", nil, 404, "not_found"},
		{"refresh timeout", "POST", "/api/products/1/refresh", "", func() { fs.refreshErr = context.DeadlineExceeded }, 504, "timeout"},
		{"unknown route", "GET", "/api/nope", "", nil, 404, "not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs.addErr, fs.refreshErr = nil, nil
			if tt.setup != nil {
				tt.setup()
			}
			code, m, _ := do(t, srv, tt.method, tt.path, tt.body)
			if code != tt.wantCode || errCode(m) != tt.wantErr {
				t.Fatalf("got %d %v, want %d %s", code, m, tt.wantCode, tt.wantErr)
			}
		})
	}
}

func TestHealth(t *testing.T) {
	srv, _ := newTestServer(t)
	code, m, _ := do(t, srv, "GET", "/api/healthz", "")
	if code != 200 || m["status"] != "ok" || m["fetcher"] != "ok" {
		t.Fatalf("health: %d %v", code, m)
	}
}

type fakeNotifier struct {
	err  error
	sent []notify.Message
}

func (f *fakeNotifier) Send(_ context.Context, m notify.Message) error {
	f.sent = append(f.sent, m)
	return f.err
}

func newNotifyServer(t *testing.T, n notify.Notifier) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := &Server{Store: st, Scrapes: &fakeScrapes{st: st}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), PublicURL: "https://pr.example"}
	if n != nil {
		s.Notifier = n
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, st
}

func TestNotifyFlagAndLog(t *testing.T) {
	srv, st := newNotifyServer(t, nil)
	code, p, _ := do(t, srv, "POST", "/api/products", `{"url":"https://www.otto.de/p/x-CS1/"}`)
	if code != 201 || p["notify"] != true {
		t.Fatalf("create: %d %v", code, p)
	}
	id := int64(p["id"].(float64))
	base := fmt.Sprintf("/api/products/%d", id)

	if code, p, _ = do(t, srv, "PATCH", base, `{"notify":false}`); code != 200 || p["notify"] != false {
		t.Fatalf("patch notify: %d %v", code, p)
	}
	if code, m, _ := do(t, srv, "PATCH", base, `{"notify":null}`); code != 400 || errCode(m) != "invalid_request" {
		t.Fatalf("patch null: %d %v", code, m)
	}
	if code, m, _ := do(t, srv, "PATCH", base, `{"notify":"no"}`); code != 400 || errCode(m) != "invalid_request" {
		t.Fatalf("patch string: %d %v", code, m)
	}

	if code, _, list := do(t, srv, "GET", base+"/notifications", ""); code != 200 || list == nil || len(list) != 0 {
		t.Fatalf("empty log: %d %v", code, list)
	}
	ctx := context.Background()
	at := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	st.InsertNotification(ctx, store.Notification{ProductID: id, Kind: "price_drop", Title: "A", Message: "**a**", Priority: 6, Status: "skipped", CreatedAt: at})
	n2, _ := st.InsertNotification(ctx, store.Notification{ProductID: id, Kind: "target_reached", Title: "B", Message: "b", Priority: 8, Status: "pending", CreatedAt: at.Add(time.Hour)})
	st.UpdateNotificationStatus(ctx, n2.ID, "failed", ptrStr("HTTP 401"), nil)

	code, _, list := do(t, srv, "GET", base+"/notifications?limit=500", "")
	if code != 200 || len(list) != 2 {
		t.Fatalf("log: %d %v", code, list)
	}
	first := list[0].(map[string]any)
	if first["kind"] != "target_reached" || first["status"] != "failed" || first["error"] != "HTTP 401" || first["priority"] != 8.0 ||
		first["title"] != "B" || first["message"] != "b" || first["sent_at"] != nil || first["created_at"] != "2026-11-11T13:00:00Z" || first["id"] == nil {
		t.Fatalf("first: %v", first)
	}
	if second := list[1].(map[string]any); second["error"] != nil || second["status"] != "skipped" {
		t.Fatalf("second: %v", second)
	}
	if _, _, list = do(t, srv, "GET", base+"/notifications?limit=1", ""); len(list) != 1 {
		t.Fatalf("limit: %v", list)
	}
	for _, q := range []string{"?limit=0", "?limit=501", "?limit=x"} {
		if code, m, _ := do(t, srv, "GET", base+"/notifications"+q, ""); code != 400 || errCode(m) != "invalid_request" {
			t.Fatalf("%s: %d %v", q, code, m)
		}
	}
	if code, m, _ := do(t, srv, "GET", "/api/products/999/notifications", ""); code != 404 || errCode(m) != "not_found" {
		t.Fatalf("missing: %d %v", code, m)
	}
}

func ptrStr(s string) *string { return &s }

func TestNotificationTestEndpoint(t *testing.T) {
	t.Run("not configured", func(t *testing.T) {
		srv, _ := newNotifyServer(t, nil)
		if code, m, _ := do(t, srv, "POST", "/api/notifications/test", ""); code != 409 || errCode(m) != "not_configured" {
			t.Fatalf("%d %v", code, m)
		}
		if code, m, _ := do(t, srv, "GET", "/api/healthz", ""); code != 200 || m["gotify"] != "not_configured" {
			t.Fatalf("health: %d %v", code, m)
		}
	})
	t.Run("sent", func(t *testing.T) {
		n := &fakeNotifier{}
		srv, _ := newNotifyServer(t, n)
		if code, m, _ := do(t, srv, "POST", "/api/notifications/test", ""); code != 200 || m["status"] != "sent" {
			t.Fatalf("%d %v", code, m)
		}
		if len(n.sent) != 1 || n.sent[0].ClickURL != "https://pr.example" || n.sent[0].Title == "" {
			t.Fatalf("sent %+v", n.sent)
		}
		if code, m, _ := do(t, srv, "GET", "/api/healthz", ""); code != 200 || m["gotify"] != "configured" {
			t.Fatalf("health: %d %v", code, m)
		}
	})
	t.Run("gotify error", func(t *testing.T) {
		srv, _ := newNotifyServer(t, &fakeNotifier{err: errors.New("gotify: HTTP 401: Unauthorized")})
		code, m, _ := do(t, srv, "POST", "/api/notifications/test", "")
		if code != 502 || errCode(m) != "notify_failed" || !strings.Contains(m["error"].(map[string]any)["message"].(string), "401") {
			t.Fatalf("%d %v", code, m)
		}
	})
	t.Run("against real gotify client", func(t *testing.T) {
		var key string
		g := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { key = r.Header.Get("X-Gotify-Key") }))
		defer g.Close()
		srv, _ := newNotifyServer(t, notify.NewGotify(g.URL, "abc"))
		if code, _, _ := do(t, srv, "POST", "/api/notifications/test", ""); code != 200 || key != "abc" {
			t.Fatalf("%d key=%q", code, key)
		}
	})
}
