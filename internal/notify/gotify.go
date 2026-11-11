package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Gotify sends messages to a Gotify server (https://gotify.net) using an
// application token.
type Gotify struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewGotify returns a client for the server at baseURL (e.g.
// "https://gotify.example" or "https://example/gotify").
func NewGotify(baseURL, token string) *Gotify {
	return &Gotify{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// SendError is returned by Gotify.Send.
type SendError struct {
	Status    int // HTTP status, 0 for network errors
	Msg       string
	retryable bool
	err       error
}

func (e *SendError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("gotify: HTTP %d: %s", e.Status, e.Msg)
	}
	return "gotify: " + e.Msg
}

// Retryable reports whether sending again may succeed (5xx, network errors).
func (e *SendError) Retryable() bool { return e.retryable }

func (e *SendError) Unwrap() error { return e.err }

// IsRetryable reports whether err is a temporary delivery error.
func IsRetryable(err error) bool {
	var r interface{ Retryable() bool }
	return errors.As(err, &r) && r.Retryable()
}

type gotifyMessage struct {
	Title    string         `json:"title"`
	Message  string         `json:"message"`
	Priority int            `json:"priority"`
	Extras   map[string]any `json:"extras"`
}

// Send posts m to {base}/message. 4xx responses are permanent errors,
// 5xx and network errors are retryable.
func (g *Gotify) Send(ctx context.Context, m Message) error {
	extras := map[string]any{"client::display": map[string]string{"contentType": "text/markdown"}}
	if m.ClickURL != "" {
		extras["client::notification"] = map[string]any{"click": map[string]string{"url": m.ClickURL}}
	}
	body, err := json.Marshal(gotifyMessage{Title: m.Title, Message: m.Body, Priority: m.Priority, Extras: extras})
	if err != nil {
		return &SendError{Msg: err.Error(), err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL+"/message", bytes.NewReader(body))
	if err != nil {
		return &SendError{Msg: err.Error(), err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", g.token)
	resp, err := g.client.Do(req)
	if err != nil {
		// A cancelled caller is not worth retrying; timeouts and refused
		// connections are.
		return &SendError{Msg: err.Error(), retryable: ctx.Err() == nil, err: err}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	msg := strings.TrimSpace(string(raw))
	var ge struct {
		Error       string `json:"error"`
		Description string `json:"errorDescription"`
	}
	if json.Unmarshal(raw, &ge) == nil && ge.Error != "" {
		msg = strings.TrimSpace(ge.Error + ": " + ge.Description)
	}
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	return &SendError{Status: resp.StatusCode, Msg: msg, retryable: resp.StatusCode >= 500}
}
