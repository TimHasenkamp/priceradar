package scraper

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/url"
	"strings"
	"sync"
	"time"
)

// clock abstracts time for tests.
type clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// RobotsChecker decides whether a URL may be fetched.
type RobotsChecker interface {
	Allowed(ctx context.Context, rawURL string) error
}

// PoliteConfig controls the pause between two requests to the same host.
type PoliteConfig struct {
	Delay  time.Duration // minimum pause between requests to one host
	Jitter time.Duration // random extra pause in [0, Jitter)
}

// Polite wraps an HTMLFetcher and enforces, per host: robots.txt, strictly
// sequential requests, and a pause of Delay+random(Jitter) between them.
type Polite struct {
	inner  HTMLFetcher
	robots RobotsChecker
	cfg    PoliteConfig
	clock  clock
	jitter func(max time.Duration) time.Duration

	mu    sync.Mutex
	hosts map[string]*hostState
}

type hostState struct {
	mu   sync.Mutex // held for the whole request => no parallel requests per host
	last time.Time
}

// NewPolite returns a polite fetcher. robots may be nil to skip the check.
func NewPolite(inner HTMLFetcher, robots RobotsChecker, cfg PoliteConfig) *Polite {
	return &Polite{
		inner:  inner,
		robots: robots,
		cfg:    cfg,
		clock:  realClock{},
		jitter: func(max time.Duration) time.Duration {
			if max <= 0 {
				return 0
			}
			return rand.N(max)
		},
		hosts: map[string]*hostState{},
	}
}

// Get implements HTMLFetcher.
func (p *Polite) Get(ctx context.Context, rawURL string) (Page, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return Page{}, fmt.Errorf("parse url: %w", err)
	}
	if p.robots != nil {
		if err := p.robots.Allowed(ctx, rawURL); err != nil {
			return Page{}, err
		}
	}
	hs := p.host(strings.ToLower(u.Host))
	hs.mu.Lock()
	defer hs.mu.Unlock()

	if !hs.last.IsZero() {
		next := hs.last.Add(p.cfg.Delay + p.jitter(p.cfg.Jitter))
		if wait := next.Sub(p.clock.Now()); wait > 0 {
			if err := p.clock.Sleep(ctx, wait); err != nil {
				return Page{}, err
			}
		}
	}
	defer func() { hs.last = p.clock.Now() }()
	return p.inner.Get(ctx, rawURL)
}

func (p *Polite) host(h string) *hostState {
	p.mu.Lock()
	defer p.mu.Unlock()
	hs, ok := p.hosts[h]
	if !ok {
		hs = &hostState{}
		p.hosts[h] = hs
	}
	return hs
}
