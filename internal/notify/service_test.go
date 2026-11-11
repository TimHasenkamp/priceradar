package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"priceradar/internal/store"
)

type fakeQueue struct {
	mu  sync.Mutex
	ids []int64
	ok  bool
}

func (q *fakeQueue) Enqueue(id int64, _ Message) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ids = append(q.ids, id)
	return q.ok
}

type svcEnv struct {
	t   *testing.T
	st  *store.Store
	svc *Service
	p   store.Product
	at  time.Time
}

func newSvcEnv(t *testing.T, q Enqueuer, target *int64) *svcEnv {
	t.Helper()
	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p, err := st.CreateProduct(context.Background(), store.NewProduct{Shop: "otto", URL: "https://www.otto.de/p/x/", Name: "Thing", TargetPriceCents: target})
	if err != nil {
		t.Fatal(err)
	}
	r := DefaultRules()
	r.PublicURL = "https://pr.example"
	return &svcEnv{t: t, st: st, svc: NewService(st, r, q, discard), p: p, at: now.Add(-30 * day)}
}

// ok records a successful check one day after the previous one and runs the service.
func (e *svcEnv) ok(price int64) { e.t.Helper(); e.okAfter(day, price) }

func (e *svcEnv) okAfter(d time.Duration, price int64) {
	e.t.Helper()
	e.at = e.at.Add(d)
	c, err := e.st.RecordCheck(context.Background(), store.PriceCheck{ProductID: e.p.ID, CheckedAt: e.at, Success: true, PriceCents: &price, Availability: ptr("in_stock")})
	if err != nil {
		e.t.Fatal(err)
	}
	e.svc.CheckRecorded(context.Background(), c)
}

func (e *svcEnv) fail() {
	e.t.Helper()
	e.at = e.at.Add(time.Hour)
	c, err := e.st.RecordCheck(context.Background(), store.PriceCheck{ProductID: e.p.ID, CheckedAt: e.at, ErrorKind: ptr("blocked"), ErrorMessage: ptr("HTTP 403")})
	if err != nil {
		e.t.Fatal(err)
	}
	e.svc.CheckRecorded(context.Background(), c)
}

func (e *svcEnv) kinds() []string {
	e.t.Helper()
	list, err := e.st.ListNotifications(context.Background(), e.p.ID, 100)
	if err != nil {
		e.t.Fatal(err)
	}
	out := []string{}
	for i := len(list) - 1; i >= 0; i-- { // oldest first
		out = append(out, list[i].Kind)
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestServiceUnconfiguredRecordsSkipped(t *testing.T) {
	e := newSvcEnv(t, nil, ptr(int64(950)))
	e.ok(900)
	list, _ := e.st.ListNotifications(context.Background(), e.p.ID, 10)
	if len(list) != 1 || list[0].Kind != KindTargetReached || list[0].Status != store.NotificationSkipped ||
		list[0].Priority != 8 || list[0].Title != "Zielpreis erreicht: Thing" || list[0].TargetPriceCents == nil ||
		*list[0].TargetPriceCents != 950 || !list[0].CreatedAt.Equal(e.at) {
		t.Fatalf("rows: %+v", list)
	}
}

func TestServiceConfiguredQueuesPending(t *testing.T) {
	q := &fakeQueue{ok: true}
	e := newSvcEnv(t, q, ptr(int64(950)))
	e.ok(900)
	list, _ := e.st.ListNotifications(context.Background(), e.p.ID, 10)
	if len(list) != 1 || list[0].Status != store.NotificationPending || len(q.ids) != 1 || q.ids[0] != list[0].ID {
		t.Fatalf("rows: %+v queued: %v", list, q.ids)
	}
}

func TestServiceDedupeThroughStore(t *testing.T) {
	e := newSvcEnv(t, nil, ptr(int64(9500)))
	e.ok(10000) // first check, above target
	e.ok(9000)  // reached; also a 10 % drop
	e.ok(9000)  // still below: nothing
	e.ok(10000) // above again: re-armed (and a rise)
	e.ok(9400)  // reached again (and a 6 % drop)
	want := []string{KindTargetReached, KindPriceDrop, KindPriceRise, KindTargetReached, KindPriceDrop}
	if got := e.kinds(); !eq(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}

	// target changed while below → notify again on the next check
	if _, err := e.st.UpdateProduct(context.Background(), e.p.ID, store.ProductPatch{TargetPriceCents: ptr(int64(9450))}); err != nil {
		t.Fatal(err)
	}
	e.ok(9400)
	if got := e.kinds(); got[len(got)-1] != KindTargetReached || len(got) != 6 {
		t.Fatalf("after target change: %v", got)
	}
}

func TestServiceFailureStreak(t *testing.T) {
	e := newSvcEnv(t, nil, nil)
	e.ok(1000)
	e.fail()
	e.fail()
	e.fail() // 3rd: alert
	e.fail() // 4th: already alerted
	if got := e.kinds(); !eq(got, []string{KindScrapeFailing}) {
		t.Fatalf("kinds = %v", got)
	}
	e.ok(1000) // re-arms
	e.fail()
	e.fail()
	e.fail()
	if got := e.kinds(); !eq(got, []string{KindScrapeFailing, KindScrapeFailing}) {
		t.Fatalf("kinds = %v", got)
	}
}

func TestServiceTrendCooldownThroughStore(t *testing.T) {
	e := newSvcEnv(t, nil, nil)
	e.ok(800) // outside the window; keeps the later prices from being new lows
	e.okAfter(10*day, 1000)
	for _, p := range []int64{980, 960, 940} {
		e.ok(p)
	}
	e.ok(920) // window: 1000..920 → trend down
	e.ok(900) // still falling, but in cooldown (1 day < 72h)
	got := e.kinds()
	n := 0
	for _, k := range got {
		if k == KindTrendDown {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("trend_down count = %d (%v)", n, got)
	}
}

func TestServiceSwitchesAndErrors(t *testing.T) {
	e := newSvcEnv(t, nil, ptr(int64(5000)))
	if _, err := e.st.UpdateProduct(context.Background(), e.p.ID, store.ProductPatch{Notify: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	e.ok(900)
	if _, err := e.st.UpdateProduct(context.Background(), e.p.ID, store.ProductPatch{Notify: ptr(true), Paused: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	e.ok(800)
	if got := e.kinds(); len(got) != 0 {
		t.Fatalf("kinds = %v", got)
	}
	// unknown product and a closed store must only log
	e.svc.CheckRecorded(context.Background(), store.PriceCheck{ProductID: 999, Success: true})
	e.st.Close()
	e.svc.CheckRecorded(context.Background(), store.PriceCheck{ProductID: e.p.ID, Success: true})
}

// End to end: service → dispatcher → Gotify (httptest) → store.
func TestServiceEndToEndGotify(t *testing.T) {
	var mu sync.Mutex
	var got []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
	}))
	defer srv.Close()

	st, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	d := NewDispatcher(NewGotify(srv.URL, "tok"), st, discard)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()

	r := DefaultRules()
	r.PublicURL = "https://pr.example"
	svc := NewService(st, r, d, discard)
	p, _ := st.CreateProduct(context.Background(), store.NewProduct{Shop: "otto", URL: "https://www.otto.de/p/x/", Name: "Thing", TargetPriceCents: ptr(int64(950))})
	price := int64(900)
	c, _ := st.RecordCheck(context.Background(), store.PriceCheck{ProductID: p.ID, Success: true, PriceCents: &price})
	svc.CheckRecorded(context.Background(), c)

	waitFor(t, func() bool {
		l, _ := st.ListNotifications(context.Background(), p.ID, 1)
		return len(l) == 1 && l[0].Status == store.NotificationSent && l[0].SentAt != nil
	})
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	click := got[0]["extras"].(map[string]any)["client::notification"].(map[string]any)["click"].(map[string]any)["url"]
	if len(got) != 1 || got[0]["priority"] != 8.0 || click != "https://pr.example/products/1" {
		t.Fatalf("gotify got %v", got)
	}
}
