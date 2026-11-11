package store

import (
	"errors"
	"time"
)

var (
	// ErrNotFound is returned when a product does not exist.
	ErrNotFound = errors.New("store: not found")
	// ErrDuplicate is returned when a product with the same URL already exists.
	ErrDuplicate = errors.New("store: duplicate")
)

// Product is a tracked product. Money is stored in integer cents.
type Product struct {
	ID                  int64
	Shop                string
	URL                 string
	SKU                 string
	Name                string // name as scraped from the shop
	CustomName          *string
	ImageURL            string
	Currency            string
	TargetPriceCents    *int64
	Paused              bool
	Notify              bool // send notifications for this product
	CreatedAt           time.Time
	UpdatedAt           time.Time
	LastCheckedAt       *time.Time
	LastSuccessAt       *time.Time
	ConsecutiveFailures int
	LastErrorKind       *string
	LastError           *string
}

// DisplayName returns the custom name if set, else the scraped name.
func (p Product) DisplayName() string {
	if p.CustomName != nil && *p.CustomName != "" {
		return *p.CustomName
	}
	return p.Name
}

// NewProduct holds the fields needed to create a product.
type NewProduct struct {
	Shop             string
	URL              string
	SKU              string
	Name             string
	ImageURL         string
	Currency         string
	TargetPriceCents *int64
}

// ProductMeta are scraped fields that may be refreshed on every successful check.
type ProductMeta struct {
	SKU      string
	Name     string
	ImageURL string
	Currency string
}

// ProductPatch describes a partial update. Nil pointer = unchanged;
// the Clear* flags set the nullable column to NULL.
type ProductPatch struct {
	CustomName       *string
	ClearCustomName  bool
	Paused           *bool
	TargetPriceCents *int64
	ClearTargetPrice bool
	Notify           *bool
}

// PriceCheck is one scrape attempt (successful or not).
type PriceCheck struct {
	ID            int64
	ProductID     int64
	CheckedAt     time.Time
	Success       bool
	PriceCents    *int64
	OldPriceCents *int64 // Streichpreis
	RRPCents      *int64 // UVP
	Availability  *string
	ErrorKind     *string
	ErrorMessage  *string
	DurationMS    int64
}

// ProductStats are aggregates over the successful checks of a product.
// See docs/API.md for the exact definitions.
type ProductStats struct {
	CurrentCents  *int64
	PreviousCents *int64
	Avg30Cents    *int64
	MinCents      *int64
	MaxCents      *int64
	OldPriceCents *int64
	RRPCents      *int64
	Availability  *string
}

// ProductWithStats bundles a product with its aggregates.
type ProductWithStats struct {
	Product
	Stats ProductStats
}

// Notification statuses.
const (
	NotificationPending = "pending" // queued for the dispatcher
	NotificationSent    = "sent"
	NotificationFailed  = "failed"
	NotificationSkipped = "skipped" // no notifier configured
)

// Notification is one notification event for a product, whether or not it was sent.
type Notification struct {
	ID        int64
	ProductID int64
	Kind      string
	Title     string
	Message   string // Markdown
	Priority  int
	Status    string
	Error     *string
	// TargetPriceCents is the product's target when the event fired.
	TargetPriceCents *int64
	CreatedAt        time.Time
	SentAt           *time.Time
}

// SuccessSummary aggregates the successful checks of a product.
type SuccessSummary struct {
	Count    int
	FirstAt  *time.Time
	MinCents *int64
	Last     *PriceCheck // latest successful check
}
