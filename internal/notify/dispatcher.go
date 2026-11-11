package notify

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"priceradar/internal/store"
)

// StatusStore persists delivery outcomes (implemented by *store.Store).
type StatusStore interface {
	UpdateNotificationStatus(ctx context.Context, id int64, status string, errMsg *string, sentAt *time.Time) error
}

type item struct {
	id  int64
	msg Message
}

// QueueSize is the dispatcher's buffer. When it is full, new messages are
// marked failed instead of blocking the scrape worker.
const QueueSize = 100

// Dispatcher delivers queued messages from a single goroutine (Run), retrying
// retryable errors with exponential backoff. Enqueue never blocks.
type Dispatcher struct {
	n   Notifier
	st  StatusStore
	log *slog.Logger

	queue  chan item
	mu     sync.Mutex
	closed bool

	backoff      []time.Duration // wait before each retry; len = number of retries
	drainTimeout time.Duration   // budget for delivering what is queued at shutdown
	sleep        func(context.Context, time.Duration) error
	now          func() time.Time
}

// NewDispatcher creates a dispatcher. Call Run to start delivering.
func NewDispatcher(n Notifier, st StatusStore, log *slog.Logger) *Dispatcher {
	return &Dispatcher{
		n: n, st: st, log: log,
		queue:        make(chan item, QueueSize),
		backoff:      []time.Duration{2 * time.Second, 8 * time.Second, 32 * time.Second},
		drainTimeout: 5 * time.Second,
		sleep:        sleepCtx,
		now:          func() time.Time { return time.Now().UTC() },
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Enqueue queues the stored notification id for delivery. It returns false
// (and marks the notification failed) if the queue is full or the dispatcher
// has stopped.
func (d *Dispatcher) Enqueue(id int64, m Message) bool {
	d.mu.Lock()
	reason := ""
	if d.closed {
		reason = "Versand übersprungen: Server wird beendet"
	} else {
		select {
		case d.queue <- item{id, m}:
		default:
			reason = "Versand übersprungen: Warteschlange voll"
		}
	}
	d.mu.Unlock()
	if reason == "" {
		return true
	}
	d.log.Warn("notification not queued", "notification_id", id, "reason", reason)
	d.finish(id, store.NotificationFailed, reason)
	return false
}

// Run delivers messages until ctx is cancelled, then delivers what is still
// queued within a short deadline and returns. Run must be called once.
func (d *Dispatcher) Run(ctx context.Context) {
	for {
		// Cancellation wins over queued items, so that shutdown always takes
		// the deadline-bounded drain path (select alone picks randomly).
		if ctx.Err() != nil {
			d.drain()
			return
		}
		select {
		case <-ctx.Done():
			d.drain()
			return
		case it := <-d.queue:
			// The HTTP request itself is not aborted by shutdown (it has its
			// own timeout); retries are.
			d.deliver(context.WithoutCancel(ctx), ctx, it)
		}
	}
}

func (d *Dispatcher) drain() {
	d.mu.Lock()
	d.closed = true // no more producers can add to the buffer
	d.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), d.drainTimeout)
	defer cancel()
	for {
		select {
		case it := <-d.queue:
			d.deliver(ctx, ctx, it)
		default:
			return
		}
	}
}

// deliver sends one message; sendCtx bounds the requests, waitCtx the
// backoff sleeps between retries.
func (d *Dispatcher) deliver(sendCtx, waitCtx context.Context, it item) {
	var err error
	for attempt := 0; ; attempt++ {
		if err = d.n.Send(sendCtx, it.msg); err == nil {
			d.finish(it.id, store.NotificationSent, "")
			return
		}
		if !IsRetryable(err) || attempt >= len(d.backoff) {
			break
		}
		d.log.Info("notification send failed, retrying", "notification_id", it.id, "attempt", attempt+1, "err", err)
		if d.sleep(waitCtx, d.backoff[attempt]) != nil {
			break
		}
	}
	d.log.Warn("notification send failed", "notification_id", it.id, "kind", it.msg.Kind, "err", err)
	d.finish(it.id, store.NotificationFailed, err.Error())
}

func (d *Dispatcher) finish(id int64, status, errMsg string) {
	// Persist even during shutdown: own short deadline, never the run context.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var e *string
	var sentAt *time.Time
	if errMsg != "" {
		e = &errMsg
	}
	if status == store.NotificationSent {
		t := d.now()
		sentAt = &t
	}
	if err := d.st.UpdateNotificationStatus(ctx, id, status, e, sentAt); err != nil {
		d.log.Error("persist notification status", "notification_id", id, "err", err)
	}
}
