package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestProductNotifyFlag(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	p := mustCreate(t, s, "u")
	if !p.Notify {
		t.Fatal("notify must default to true")
	}
	got, err := s.UpdateProduct(ctx, p.ID, ProductPatch{Notify: ptr(false)})
	if err != nil || got.Notify {
		t.Fatalf("disable: %+v %v", got, err)
	}
	pw, _ := s.GetProductWithStats(ctx, p.ID)
	if pw.Notify {
		t.Fatal("with stats: notify must be false")
	}
	active, _ := s.ListActiveProducts(ctx)
	if len(active) != 1 || active[0].Notify {
		t.Fatalf("active: %+v", active)
	}
	if got, _ = s.UpdateProduct(ctx, p.ID, ProductPatch{Notify: ptr(true)}); !got.Notify {
		t.Fatal("re-enable failed")
	}
}

func TestNotificationsCRUD(t *testing.T) {
	ctx := context.Background()
	s, clk := newTestStore(t)
	p := mustCreate(t, s, "u")
	other := mustCreate(t, s, "o")

	n1, err := s.InsertNotification(ctx, Notification{ProductID: p.ID, Kind: "price_drop", Title: "T1", Message: "M1", Priority: 6, Status: NotificationPending, TargetPriceCents: ptr(int64(500))})
	if err != nil {
		t.Fatal(err)
	}
	if n1.ID == 0 || !n1.CreatedAt.Equal(t0) || n1.SentAt != nil || n1.Error != nil {
		t.Fatalf("insert defaults: %+v", n1)
	}
	clk.advance(time.Minute)
	explicit := t0.Add(-time.Hour)
	n2, err := s.InsertNotification(ctx, Notification{ProductID: p.ID, Kind: "target_reached", Title: "T2", Message: "M2", Priority: 8, Status: NotificationSkipped, CreatedAt: explicit})
	if err != nil || !n2.CreatedAt.Equal(explicit) {
		t.Fatalf("explicit created_at: %+v %v", n2, err)
	}
	n3, _ := s.InsertNotification(ctx, Notification{ProductID: p.ID, Kind: "price_drop", Title: "T3", Message: "M3", Priority: 6, Status: NotificationPending})
	if _, err := s.InsertNotification(ctx, Notification{ProductID: other.ID, Kind: "price_drop", Title: "x", Message: "x", Priority: 1, Status: NotificationSkipped}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.InsertNotification(ctx, Notification{ProductID: p.ID, Kind: "k", Title: "t", Message: "m", Status: "bogus"}); err == nil {
		t.Fatal("invalid status must be rejected")
	}
	if _, err := s.InsertNotification(ctx, Notification{ProductID: 999, Kind: "k", Title: "t", Message: "m", Status: NotificationSkipped}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing product: %v", err)
	}

	sentAt := t0.Add(2 * time.Minute)
	if err := s.UpdateNotificationStatus(ctx, n1.ID, NotificationSent, nil, &sentAt); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateNotificationStatus(ctx, n3.ID, NotificationFailed, ptr("HTTP 500"), nil); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateNotificationStatus(ctx, 999, NotificationSent, nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}

	list, err := s.ListNotifications(ctx, p.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	// newest first: n3 (t0+1m, higher id), n1 (t0), n2 (t0-1h)
	if len(list) != 3 || list[0].ID != n3.ID || list[1].ID != n1.ID || list[2].ID != n2.ID {
		t.Fatalf("order: %+v", list)
	}
	if list[0].Status != NotificationFailed || list[0].Error == nil || *list[0].Error != "HTTP 500" {
		t.Fatalf("failed row: %+v", list[0])
	}
	if list[1].Status != NotificationSent || list[1].SentAt == nil || !list[1].SentAt.Equal(sentAt) ||
		list[1].TargetPriceCents == nil || *list[1].TargetPriceCents != 500 || list[1].Title != "T1" || list[1].Message != "M1" || list[1].Priority != 6 {
		t.Fatalf("sent row: %+v", list[1])
	}
	if l, _ := s.ListNotifications(ctx, p.ID, 1); len(l) != 1 {
		t.Fatalf("limit: %d", len(l))
	}

	last, err := s.LastNotifications(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(last) != 2 || last["price_drop"].ID != n3.ID || last["target_reached"].ID != n2.ID {
		t.Fatalf("last per kind: %+v", last)
	}
	if last, _ := s.LastNotifications(ctx, 999); len(last) != 0 {
		t.Fatalf("unknown product: %+v", last)
	}

	if err := s.DeleteProduct(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.ListNotifications(ctx, p.ID, 10); len(l) != 0 {
		t.Fatalf("cascade: %+v", l)
	}
}

func TestFailPendingNotifications(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	p := mustCreate(t, s, "u")
	pend, _ := s.InsertNotification(ctx, Notification{ProductID: p.ID, Kind: "k", Title: "t", Message: "m", Priority: 1, Status: NotificationPending})
	skip, _ := s.InsertNotification(ctx, Notification{ProductID: p.ID, Kind: "k", Title: "t", Message: "m", Priority: 1, Status: NotificationSkipped})
	n, err := s.FailPendingNotifications(ctx, "restart")
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	list, _ := s.ListNotifications(ctx, p.ID, 10)
	for _, x := range list {
		switch x.ID {
		case pend.ID:
			if x.Status != NotificationFailed || x.Error == nil || *x.Error != "restart" {
				t.Errorf("pending row: %+v", x)
			}
		case skip.ID:
			if x.Status != NotificationSkipped || x.Error != nil {
				t.Errorf("skipped row touched: %+v", x)
			}
		}
	}
}

func TestSuccessSummary(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	p := mustCreate(t, s, "u")

	sum, err := s.SuccessSummary(ctx, p.ID, 0)
	if err != nil || sum.Count != 0 || sum.Last != nil || sum.MinCents != nil || sum.FirstAt != nil {
		t.Fatalf("empty: %+v %v", sum, err)
	}

	c1, _ := s.RecordCheck(ctx, PriceCheck{ProductID: p.ID, CheckedAt: t0, Success: true, PriceCents: ptr(int64(1000)), Availability: ptr("in_stock")})
	s.RecordCheck(ctx, failCheck(p.ID))
	c2, _ := s.RecordCheck(ctx, PriceCheck{ProductID: p.ID, CheckedAt: t0.Add(time.Hour), Success: true, PriceCents: ptr(int64(800)), Availability: ptr("limited")})
	c3, _ := s.RecordCheck(ctx, okCheck(p.ID, t0.Add(2*time.Hour), 900))

	// summary of everything before c3
	sum, err = s.SuccessSummary(ctx, p.ID, c3.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Count != 2 || *sum.MinCents != 800 || !sum.FirstAt.Equal(c1.CheckedAt) || sum.Last == nil || sum.Last.ID != c2.ID ||
		*sum.Last.PriceCents != 800 || *sum.Last.Availability != "limited" {
		t.Fatalf("summary: %+v last=%+v", sum, sum.Last)
	}
	// exclude id 0 → everything
	sum, _ = s.SuccessSummary(ctx, p.ID, 0)
	if sum.Count != 3 || sum.Last.ID != c3.ID {
		t.Fatalf("all: %+v", sum)
	}
}
