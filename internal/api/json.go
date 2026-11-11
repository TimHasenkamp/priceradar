package api

import (
	"time"

	"priceradar/internal/store"
)

type statsJSON struct {
	CurrentCents  *int64  `json:"current_cents"`
	PreviousCents *int64  `json:"previous_cents"`
	Avg30Cents    *int64  `json:"avg30_cents"`
	MinCents      *int64  `json:"min_cents"`
	MaxCents      *int64  `json:"max_cents"`
	OldPriceCents *int64  `json:"old_price_cents"`
	RRPCents      *int64  `json:"rrp_cents"`
	Availability  *string `json:"availability"`
}

type productJSON struct {
	ID                  int64      `json:"id"`
	Shop                string     `json:"shop"`
	URL                 string     `json:"url"`
	SKU                 string     `json:"sku"`
	Name                string     `json:"name"`
	OriginalName        string     `json:"original_name"`
	CustomName          *string    `json:"custom_name"`
	ImageURL            string     `json:"image_url"`
	Currency            string     `json:"currency"`
	TargetPriceCents    *int64     `json:"target_price_cents"`
	TargetReached       bool       `json:"target_reached"`
	Paused              bool       `json:"paused"`
	Notify              bool       `json:"notify"`
	CreatedAt           time.Time  `json:"created_at"`
	LastCheckedAt       *time.Time `json:"last_checked_at"`
	LastSuccessAt       *time.Time `json:"last_success_at"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	LastErrorKind       *string    `json:"last_error_kind"`
	LastError           *string    `json:"last_error"`
	Stats               statsJSON  `json:"stats"`
}

func toProductJSON(p store.ProductWithStats) productJSON {
	st := p.Stats
	return productJSON{
		ID: p.ID, Shop: p.Shop, URL: p.URL, SKU: p.SKU,
		Name: p.DisplayName(), OriginalName: p.Name, CustomName: p.CustomName,
		ImageURL: p.ImageURL, Currency: p.Currency,
		TargetPriceCents: p.TargetPriceCents,
		TargetReached:    p.TargetPriceCents != nil && st.CurrentCents != nil && *st.CurrentCents <= *p.TargetPriceCents,
		Paused:           p.Paused,
		Notify:           p.Notify,
		CreatedAt:        p.CreatedAt.UTC(), LastCheckedAt: utc(p.LastCheckedAt), LastSuccessAt: utc(p.LastSuccessAt),
		ConsecutiveFailures: p.ConsecutiveFailures, LastErrorKind: p.LastErrorKind, LastError: p.LastError,
		Stats: statsJSON{st.CurrentCents, st.PreviousCents, st.Avg30Cents, st.MinCents, st.MaxCents, st.OldPriceCents, st.RRPCents, st.Availability},
	}
}

type pricePointJSON struct {
	CheckedAt     time.Time `json:"checked_at"`
	PriceCents    int64     `json:"price_cents"`
	OldPriceCents *int64    `json:"old_price_cents"`
	RRPCents      *int64    `json:"rrp_cents"`
	Availability  *string   `json:"availability"`
}

type checkJSON struct {
	ID           int64     `json:"id"`
	CheckedAt    time.Time `json:"checked_at"`
	Success      bool      `json:"success"`
	PriceCents   *int64    `json:"price_cents"`
	ErrorKind    *string   `json:"error_kind"`
	ErrorMessage *string   `json:"error_message"`
	DurationMS   int64     `json:"duration_ms"`
}

func toCheckJSON(c store.PriceCheck) checkJSON {
	return checkJSON{c.ID, c.CheckedAt.UTC(), c.Success, c.PriceCents, c.ErrorKind, c.ErrorMessage, c.DurationMS}
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

type notificationJSON struct {
	ID        int64      `json:"id"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Message   string     `json:"message"`
	Priority  int        `json:"priority"`
	Status    string     `json:"status"`
	Error     *string    `json:"error"`
	CreatedAt time.Time  `json:"created_at"`
	SentAt    *time.Time `json:"sent_at"`
}

func toNotificationJSON(n store.Notification) notificationJSON {
	return notificationJSON{n.ID, n.Kind, n.Title, n.Message, n.Priority, n.Status, n.Error, n.CreatedAt.UTC(), utc(n.SentAt)}
}
