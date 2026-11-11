package scraper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Page is a downloaded HTML page.
type Page struct {
	Status   int
	FinalURL string
	HTML     []byte
}

// HTMLFetcher downloads a page. Implementations must only return an error for
// transport-level problems; HTTP status codes are reported in Page.Status.
type HTMLFetcher interface {
	Get(ctx context.Context, rawURL string) (Page, error)
}

// SidecarFetcher talks to the Python/Scrapling fetcher service (see fetcher/).
// It is needed for shops behind JavaScript bot protection.
type SidecarFetcher struct {
	BaseURL string
	Client  *http.Client
	// Timeout is forwarded to the sidecar as the browser navigation timeout.
	Timeout time.Duration
	// WaitSelector (optional) makes the browser wait until this CSS selector
	// is attached, e.g. so a JS bot challenge can solve and reload the page.
	WaitSelector string
	// Token (optional) is sent as "Authorization: Bearer <token>"; it must
	// match FETCHER_TOKEN of the sidecar when that is reachable over a network.
	Token string
}

type sidecarRequest struct {
	URL          string `json:"url"`
	TimeoutMS    int64  `json:"timeout_ms"`
	WaitSelector string `json:"wait_selector,omitempty"`
}

type sidecarResponse struct {
	Status   int    `json:"status"`
	FinalURL string `json:"final_url"`
	HTML     string `json:"html"`
	Error    string `json:"error"`
}

// Get asks the sidecar to render rawURL in a stealth browser.
func (f *SidecarFetcher) Get(ctx context.Context, rawURL string) (Page, error) {
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	body, _ := json.Marshal(sidecarRequest{URL: rawURL, TimeoutMS: timeout.Milliseconds(), WaitSelector: f.WaitSelector})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(f.BaseURL, "/")+"/fetch", bytes.NewReader(body))
	if err != nil {
		return Page{}, fmt.Errorf("sidecar request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if f.Token != "" {
		req.Header.Set("Authorization", "Bearer "+f.Token)
	}
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: timeout + 30*time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Page{}, fmt.Errorf("sidecar unreachable: %v: %w", err, ErrTransient)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return Page{}, fmt.Errorf("sidecar read: %v: %w", err, ErrTransient)
	}
	var sr sidecarResponse
	if err := json.Unmarshal(raw, &sr); err != nil {
		return Page{}, fmt.Errorf("sidecar: bad response (HTTP %d): %v: %w", resp.StatusCode, err, ErrTransient)
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		// e.g. "host not allowed": a configuration problem, retrying won't help
		return Page{}, fmt.Errorf("sidecar rejected request: %s (HTTP %d)", sr.Error, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return Page{}, fmt.Errorf("sidecar: %s (HTTP %d): %w", sr.Error, resp.StatusCode, ErrTransient)
	}
	return Page{Status: sr.Status, FinalURL: sr.FinalURL, HTML: []byte(sr.HTML)}, nil
}

// Ping checks whether the sidecar is up.
func (f *SidecarFetcher) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(f.BaseURL, "/")+"/healthz", nil)
	if err != nil {
		return err
	}
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sidecar health: HTTP %d", resp.StatusCode)
	}
	return nil
}
