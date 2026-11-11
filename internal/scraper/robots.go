package scraper

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/temoto/robotstxt"
)

// Robots checks URLs against the host's robots.txt, cached per host.
type Robots struct {
	userAgent string
	client    *http.Client
	ttl       time.Duration

	mu    sync.Mutex
	cache map[string]robotsEntry
}

type robotsEntry struct {
	group   *robotstxt.Group
	expires time.Time
}

// NewRobots returns a checker identifying itself as userAgent.
func NewRobots(userAgent string, client *http.Client, ttl time.Duration) *Robots {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Robots{userAgent: userAgent, client: client, ttl: ttl, cache: map[string]robotsEntry{}}
}

// Allowed returns nil if the URL may be fetched, an error wrapping
// ErrRobotsDisallowed if not, or ErrTransient if robots.txt is unavailable.
func (r *Robots) Allowed(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("robots: parse url: %w", err)
	}
	g, err := r.group(ctx, u.Scheme+"://"+u.Host)
	if err != nil {
		return err
	}
	path := u.EscapedPath()
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	if !g.Test(path) {
		return fmt.Errorf("robots: %s: %w", path, ErrRobotsDisallowed)
	}
	return nil
}

func (r *Robots) group(ctx context.Context, origin string) (*robotstxt.Group, error) {
	r.mu.Lock()
	defer r.mu.Unlock() // also serializes robots.txt downloads
	if e, ok := r.cache[origin]; ok && time.Now().Before(e.expires) {
		return e.group, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/robots.txt", nil)
	if err != nil {
		return nil, fmt.Errorf("robots: %w", err)
	}
	req.Header.Set("User-Agent", r.userAgent)
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("robots: %v: %w", err, ErrTransient)
	}
	defer resp.Body.Close()
	switch sc := resp.StatusCode; {
	case sc >= 500:
		return nil, fmt.Errorf("robots: HTTP %d: %w", sc, ErrTransient)
	case sc == http.StatusUnauthorized || sc == http.StatusForbidden || sc == http.StatusTooManyRequests:
		// The robotstxt lib would read this as "disallow all" and we'd cache
		// it for a day; for us it means a bot wall in front of robots.txt.
		return nil, fmt.Errorf("robots: HTTP %d: %w", sc, ErrBlocked)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return nil, fmt.Errorf("robots: read: %v: %w", err, ErrTransient)
	}
	data, err := robotstxt.FromStatusAndBytes(resp.StatusCode, body)
	if err != nil {
		return nil, fmt.Errorf("robots: parse: %w", err)
	}
	g := data.FindGroup(r.userAgent)
	r.cache[origin] = robotsEntry{group: g, expires: time.Now().Add(r.ttl)}
	return g, nil
}
