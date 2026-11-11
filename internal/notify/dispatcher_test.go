package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"priceradar/internal/store"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

type result struct {
	status string
	err    *string
	sentAt *time.Time
}

type fakeStatusStore struct {
	mu   sync.Mutex
	rows map[int64]result
	err  error
}

func (f *fakeStatusStore) UpdateNotificationStatus(_ context.Context, id int64, status string, errMsg *string, sentAt *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rows == nil {
		f.rows = map[int64]result{}
	}
	f.rows[id] = result{status, errMsg, sentAt}
	return f.err
}

func (f *fakeStatusStore) get(id int64) (result, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[id]
	return r, ok
}

// fakeNotifier returns errs[i] for the i-th call (nil once exhausted).
type fakeNotifier struct {
	mu    sync.Mutex
	errs  []error
	calls []Message
	block chan struct{} // if set, Send waits for it or ctx
}

func (f *fakeNotifier) Send(ctx context.Context, m Message) error {
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, m)
	if i := len(f.calls) - 1; i < len(f.errs) {
		return f.errs[i]
	}
	return nil
}

func (f *fakeNotifier) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

var (
	errRetry = &SendError{Status: 503, Msg: "unavailable", retryable: true}
	errPerm  = &SendError{Status: 401, Msg: "Unauthorized"}
)

func newTestDispatcher(n Notifier) (*Dispatcher, *fakeStatusStore, *[]time.Duration) {
	st := &fakeStatusStore{}
	d := NewDispatcher(n, st, discard)
	var mu sync.Mutex
	slept := &[]time.Duration{}
	d.sleep = func(ctx context.Context, dur time.Duration) error {
		mu.Lock()
		*slept = append(*slept, dur)
		mu.Unlock()
		return ctx.Err()
	}
	d.now = func() time.Time { return now }
	return d, st, slept
}

// runOnce enqueues the given ids, runs the dispatcher with an already
// cancelled context (so it drains the queue and returns) and waits for it.
func runDrained(d *Dispatcher, ids ...int64) {
	for _, id := range ids {
		d.Enqueue(id, Message{Title: "m"})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.Run(ctx)
}

func TestDispatcherDelivery(t *testing.T) {
	tests := []struct {
		name       string
		errs       []error
		wantCalls  int
		wantStatus string
		wantSleeps int
	}{
		{"sent", nil, 1, store.NotificationSent, 0},
		{"retry then sent", []error{errRetry, errRetry}, 3, store.NotificationSent, 2},
		{"permanent error", []error{errPerm}, 1, store.NotificationFailed, 0},
		{"retries exhausted", []error{errRetry, errRetry, errRetry, errRetry}, 4, store.NotificationFailed, 3},
		{"plain error is permanent", []error{errors.New("boom")}, 1, store.NotificationFailed, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := &fakeNotifier{errs: tt.errs}
			d, st, slept := newTestDispatcher(n)
			// live run: deliver while running, then stop
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { d.Run(ctx); close(done) }()
			if !d.Enqueue(7, Message{Title: "x"}) {
				t.Fatal("enqueue failed")
			}
			waitFor(t, func() bool { _, ok := st.get(7); return ok })
			cancel()
			<-done

			r, _ := st.get(7)
			if n.count() != tt.wantCalls || r.status != tt.wantStatus || len(*slept) != tt.wantSleeps {
				t.Fatalf("calls=%d status=%s sleeps=%v", n.count(), r.status, *slept)
			}
			if tt.wantStatus == store.NotificationSent && (r.sentAt == nil || !r.sentAt.Equal(now) || r.err != nil) {
				t.Fatalf("sent row: %+v", r)
			}
			if tt.wantStatus == store.NotificationFailed && (r.err == nil || *r.err == "" || r.sentAt != nil) {
				t.Fatalf("failed row: %+v", r)
			}
		})
	}
}

func TestDispatcherBackoff(t *testing.T) {
	n := &fakeNotifier{errs: []error{errRetry, errRetry, errRetry, errRetry}}
	d, _, slept := newTestDispatcher(n)
	runDrained(d, 1)
	want := []time.Duration{2 * time.Second, 8 * time.Second, 32 * time.Second}
	if len(*slept) != 3 || (*slept)[0] != want[0] || (*slept)[1] != want[1] || (*slept)[2] != want[2] {
		t.Fatalf("backoff = %v, want %v", *slept, want)
	}
}

func TestDispatcherQueueFullDoesNotBlock(t *testing.T) {
	d, st, _ := newTestDispatcher(&fakeNotifier{})
	d.queue = make(chan item, 2) // not running: nothing is consumed
	start := time.Now()
	ok := []bool{d.Enqueue(1, Message{}), d.Enqueue(2, Message{}), d.Enqueue(3, Message{})}
	if time.Since(start) > time.Second {
		t.Fatal("Enqueue blocked")
	}
	if !ok[0] || !ok[1] || ok[2] {
		t.Fatalf("ok = %v", ok)
	}
	if r, _ := st.get(3); r.status != store.NotificationFailed || r.err == nil {
		t.Fatalf("overflow row: %+v", r)
	}
}

func TestDispatcherShutdown(t *testing.T) {
	t.Run("drains queued messages", func(t *testing.T) {
		n := &fakeNotifier{}
		d, st, _ := newTestDispatcher(n)
		runDrained(d, 1, 2, 3)
		for _, id := range []int64{1, 2, 3} {
			if r, _ := st.get(id); r.status != store.NotificationSent {
				t.Fatalf("id %d: %+v", id, r)
			}
		}
		// after stop: rejected and marked failed
		if d.Enqueue(4, Message{}) {
			t.Fatal("enqueue after stop must fail")
		}
		if r, _ := st.get(4); r.status != store.NotificationFailed {
			t.Fatalf("late row: %+v", r)
		}
	})
	t.Run("drain deadline", func(t *testing.T) {
		n := &fakeNotifier{block: make(chan struct{})} // never answers
		d, st, _ := newTestDispatcher(n)
		d.drainTimeout = 50 * time.Millisecond
		start := time.Now()
		runDrained(d, 1, 2)
		if time.Since(start) > 2*time.Second {
			t.Fatal("drain did not respect its deadline")
		}
		for _, id := range []int64{1, 2} {
			if r, _ := st.get(id); r.status != store.NotificationFailed {
				t.Fatalf("id %d: %+v", id, r)
			}
		}
	})
	t.Run("cancel during backoff", func(t *testing.T) {
		n := &fakeNotifier{errs: []error{errRetry, errRetry, errRetry, errRetry}}
		d, st, _ := newTestDispatcher(n)
		ctx, cancel := context.WithCancel(context.Background())
		d.sleep = func(c context.Context, _ time.Duration) error { cancel(); <-c.Done(); return c.Err() }
		d.Enqueue(1, Message{})
		d.Run(ctx)
		if r, _ := st.get(1); r.status != store.NotificationFailed {
			t.Fatalf("row: %+v", r)
		}
	})
	t.Run("store errors are only logged", func(t *testing.T) {
		d, st, _ := newTestDispatcher(&fakeNotifier{})
		st.err = errors.New("db gone")
		runDrained(d, 1) // must not panic or hang
	})
}

func TestSleepCtx(t *testing.T) {
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepCtx(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
