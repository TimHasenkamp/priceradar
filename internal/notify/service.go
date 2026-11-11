package notify

import (
	"context"
	"fmt"
	"log/slog"

	"priceradar/internal/store"
)

// Enqueuer hands a stored notification to the delivery queue (*Dispatcher).
type Enqueuer interface {
	Enqueue(id int64, m Message) bool
}

// Service evaluates notifications after each recorded check. It implements
// scheduler.CheckObserver.
type Service struct {
	store *store.Store
	rules Rules
	queue Enqueuer // nil: no notifier configured, events are stored as skipped
	log   *slog.Logger
}

// NewService creates the service. Pass a nil queue when Gotify is not
// configured; events are then recorded with status "skipped".
func NewService(st *store.Store, rules Rules, queue Enqueuer, log *slog.Logger) *Service {
	return &Service{store: st, rules: rules, queue: queue, log: log}
}

// CheckRecorded evaluates and queues notifications for a just-recorded check.
// It runs on the scrape worker, so it only does a few small queries; sending
// happens on the dispatcher. Errors are logged, never returned.
func (s *Service) CheckRecorded(ctx context.Context, c store.PriceCheck) {
	if err := s.handle(ctx, c); err != nil {
		s.log.Error("evaluate notifications", "product_id", c.ProductID, "check_id", c.ID, "err", err)
	}
}

func (s *Service) handle(ctx context.Context, c store.PriceCheck) error {
	p, err := s.store.GetProduct(ctx, c.ProductID)
	if err != nil {
		return err
	}
	if p.Paused || !p.Notify {
		return nil
	}
	in := Input{Product: p, Check: c}
	if in.Last, err = s.store.LastNotifications(ctx, p.ID); err != nil {
		return err
	}
	if c.Success {
		if in.Before, err = s.store.SuccessSummary(ctx, p.ID, c.ID); err != nil {
			return err
		}
		from := c.CheckedAt.Add(-s.rules.TrendWindow)
		if in.Window, err = s.store.ListPrices(ctx, p.ID, &from, &c.CheckedAt); err != nil {
			return err
		}
		if last, ok := in.Last[KindTargetReached]; ok && p.TargetPriceCents != nil {
			if in.SinceTarget, err = s.store.ListPrices(ctx, p.ID, &last.CreatedAt, nil); err != nil {
				return err
			}
		}
	}
	for _, m := range Evaluate(in, s.rules) {
		status := store.NotificationPending
		if s.queue == nil {
			status = store.NotificationSkipped
		}
		n, err := s.store.InsertNotification(ctx, store.Notification{
			ProductID: p.ID, Kind: m.Kind, Title: m.Title, Message: m.Body, Priority: m.Priority,
			Status: status, TargetPriceCents: p.TargetPriceCents, CreatedAt: c.CheckedAt,
		})
		if err != nil {
			return fmt.Errorf("store %s: %w", m.Kind, err)
		}
		s.log.Info("notification", "product_id", p.ID, "kind", m.Kind, "status", status, "notification_id", n.ID)
		if s.queue != nil {
			s.queue.Enqueue(n.ID, m)
		}
	}
	return nil
}
