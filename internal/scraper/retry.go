package scraper

import (
	"context"
	"net/url"
	"time"
)

// RetryConfig controls exponential backoff for retryable errors.
type RetryConfig struct {
	Attempts    int           // total attempts, >= 1
	BaseBackoff time.Duration // wait before the 2nd attempt
	Factor      int           // multiplier per further attempt
}

type retrying struct {
	inner Scraper
	cfg   RetryConfig
	sleep func(context.Context, time.Duration) error
}

// WithRetry wraps s so that transient and blocked errors are retried with
// exponential backoff (BaseBackoff, BaseBackoff*Factor, ...).
func WithRetry(s Scraper, cfg RetryConfig) Scraper {
	if cfg.Attempts < 1 {
		cfg.Attempts = 1
	}
	if cfg.Factor < 1 {
		cfg.Factor = 1
	}
	return &retrying{inner: s, cfg: cfg, sleep: realClock{}.Sleep}
}

func (r *retrying) Name() string             { return r.inner.Name() }
func (r *retrying) Supports(u *url.URL) bool { return r.inner.Supports(u) }

func (r *retrying) NormalizeURL(u *url.URL) string {
	if n, ok := r.inner.(URLNormalizer); ok {
		return n.NormalizeURL(u)
	}
	c := *u
	c.Fragment = ""
	return c.String()
}

func (r *retrying) Fetch(ctx context.Context, rawURL string) (Offer, error) {
	backoff := r.cfg.BaseBackoff
	var err error
	for attempt := 1; ; attempt++ {
		var o Offer
		o, err = r.inner.Fetch(ctx, rawURL)
		if err == nil || !Retryable(err) || attempt >= r.cfg.Attempts {
			return o, err
		}
		if serr := r.sleep(ctx, backoff); serr != nil {
			return Offer{}, err
		}
		backoff *= time.Duration(r.cfg.Factor)
	}
}
