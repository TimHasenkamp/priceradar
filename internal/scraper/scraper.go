// Package scraper defines the shop-independent scraping contract and the
// shared machinery around it (polite fetching, robots.txt, retries).
//
// Adding a new shop means implementing [Scraper] (usually in its own
// sub-package, see scraper/otto) and registering it in the [Registry].
package scraper

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Availability values used across the app (and in the API).
const (
	AvailabilityInStock    = "in_stock"
	AvailabilityOutOfStock = "out_of_stock"
	AvailabilityPreorder   = "preorder"
	AvailabilityLimited    = "limited"
	AvailabilityUnknown    = "unknown"
)

// Offer is what a scraper extracts from a product page. Money is in cents.
type Offer struct {
	Name          string
	ImageURL      string
	SKU           string
	Currency      string
	PriceCents    int64
	OldPriceCents *int64 // Streichpreis ("reduziert von")
	RRPCents      *int64 // UVP
	Availability  string
	// Warnings are non-fatal oddities (e.g. a fallback was used) worth logging.
	Warnings []string
}

// Scraper extracts an [Offer] from a product URL of one specific shop.
type Scraper interface {
	// Name is a short stable identifier, stored with each product (e.g. "otto").
	Name() string
	// Supports reports whether this scraper handles the given URL.
	Supports(u *url.URL) bool
	// Fetch downloads and parses the product page.
	// Errors wrap one of the Err* sentinels of this package.
	Fetch(ctx context.Context, rawURL string) (Offer, error)
}

// URLNormalizer can optionally be implemented by a Scraper to canonicalize
// product URLs (e.g. strip tracking parameters) before they are stored.
type URLNormalizer interface {
	NormalizeURL(u *url.URL) string
}

// Sentinel errors. Use errors.Is, or [KindOf] to get the stable string form.
var (
	ErrBlocked          = errors.New("blocked by bot protection")
	ErrLayoutChanged    = errors.New("page layout not recognized")
	ErrNotFound         = errors.New("product page not found")
	ErrTransient        = errors.New("transient error")
	ErrRobotsDisallowed = errors.New("disallowed by robots.txt")
	ErrUnsupported      = errors.New("no scraper supports this URL")
	ErrInvalidURL       = errors.New("invalid URL")
)

// Error kinds as stored in the database and exposed by the API.
const (
	KindBlocked          = "blocked"
	KindLayoutChanged    = "layout_changed"
	KindNotFound         = "not_found"
	KindTransient        = "transient"
	KindRobotsDisallowed = "robots_disallowed"
	KindInternal         = "internal"
)

// KindOf maps an error to its stable kind string ("" for nil).
func KindOf(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrBlocked):
		return KindBlocked
	case errors.Is(err, ErrLayoutChanged):
		return KindLayoutChanged
	case errors.Is(err, ErrNotFound):
		return KindNotFound
	case errors.Is(err, ErrTransient), errors.Is(err, context.DeadlineExceeded):
		return KindTransient
	case errors.Is(err, ErrRobotsDisallowed):
		return KindRobotsDisallowed
	default:
		return KindInternal
	}
}

// Retryable reports whether an error is worth retrying later.
func Retryable(err error) bool {
	k := KindOf(err)
	return k == KindTransient || k == KindBlocked
}

// Registry picks the scraper responsible for a URL.
type Registry struct {
	scrapers []Scraper
}

// NewRegistry returns a registry consulting the scrapers in order.
func NewRegistry(s ...Scraper) *Registry { return &Registry{scrapers: s} }

// Resolve parses rawURL and returns the matching scraper and the normalized URL.
func (r *Registry) Resolve(rawURL string) (Scraper, string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, "", fmt.Errorf("%q: %w", rawURL, ErrInvalidURL)
	}
	for _, s := range r.scrapers {
		if !s.Supports(u) {
			continue
		}
		if n, ok := s.(URLNormalizer); ok {
			return s, n.NormalizeURL(u), nil
		}
		u.Fragment = ""
		return s, u.String(), nil
	}
	return nil, "", fmt.Errorf("%s: %w", u.Host, ErrUnsupported)
}

// ByName returns the scraper with the given name.
func (r *Registry) ByName(name string) (Scraper, bool) {
	for _, s := range r.scrapers {
		if s.Name() == name {
			return s, true
		}
	}
	return nil, false
}
