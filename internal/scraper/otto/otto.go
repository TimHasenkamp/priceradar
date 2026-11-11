// Package otto implements the scraper for otto.de product pages.
package otto

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"priceradar/internal/scraper"
)

// WaitSelector should be set on the sidecar fetcher used for otto.de: Kasada
// first serves a JS challenge (HTTP 400) that the browser solves and then
// reloads; waiting for the JSON-LD makes the fetch return the real page.
const WaitSelector = `script[type="application/ld+json"]`

// Scraper fetches and parses otto.de product pages.
//
// otto.de is protected by Kasada, so in production the fetcher is the
// Scrapling sidecar (scraper.SidecarFetcher), wrapped in scraper.Polite.
type Scraper struct {
	fetcher scraper.HTMLFetcher
}

var _ scraper.Scraper = (*Scraper)(nil)
var _ scraper.URLNormalizer = (*Scraper)(nil)

// New returns an otto.de scraper using f to download pages.
func New(f scraper.HTMLFetcher) *Scraper { return &Scraper{fetcher: f} }

// Name implements scraper.Scraper.
func (s *Scraper) Name() string { return "otto" }

// Supports accepts otto.de product detail pages (/p/...).
func (s *Scraper) Supports(u *url.URL) bool {
	h := strings.ToLower(u.Hostname())
	return (h == "www.otto.de" || h == "otto.de") && strings.HasPrefix(u.Path, "/p/")
}

// NormalizeURL forces https://www.otto.de and keeps only the variationId
// query parameter, dropping tracking parameters and fragments.
func (s *Scraper) NormalizeURL(u *url.URL) string {
	n := url.URL{Scheme: "https", Host: "www.otto.de", Path: u.Path}
	if v := u.Query().Get("variationId"); v != "" {
		n.RawQuery = url.Values{"variationId": {v}}.Encode()
	}
	return n.String()
}

// Fetch implements scraper.Scraper.
func (s *Scraper) Fetch(ctx context.Context, rawURL string) (scraper.Offer, error) {
	page, err := s.fetcher.Get(ctx, rawURL)
	if err != nil {
		return scraper.Offer{}, fmt.Errorf("otto: fetch: %w", err)
	}
	switch st := page.Status; {
	case st == http.StatusNotFound || st == http.StatusGone:
		return scraper.Offer{}, fmt.Errorf("otto: HTTP %d: %w", st, scraper.ErrNotFound)
	case st == http.StatusBadRequest || st == http.StatusForbidden || st == http.StatusTooManyRequests:
		return scraper.Offer{}, fmt.Errorf("otto: HTTP %d: %w", st, scraper.ErrBlocked)
	case st >= 500:
		return scraper.Offer{}, fmt.Errorf("otto: HTTP %d: %w", st, scraper.ErrTransient)
	case st >= 400:
		return scraper.Offer{}, fmt.Errorf("otto: unexpected HTTP %d: %w", st, scraper.ErrLayoutChanged)
	}
	offer, err := Parse(page.HTML)
	if err != nil {
		return scraper.Offer{}, fmt.Errorf("otto: %w", err)
	}
	return offer, nil
}
