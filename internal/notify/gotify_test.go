package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGotifySend(t *testing.T) {
	var (
		gotPath, gotKey, gotCT string
		gotBody                map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey, gotCT = r.Method+" "+r.URL.Path, r.Header.Get("X-Gotify-Key"), r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Errorf("bad json: %s", raw)
		}
		w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	g := NewGotify(srv.URL+"/gotify/", "tok123")
	if g.client.Timeout != 10*time.Second {
		t.Fatalf("timeout = %v", g.client.Timeout)
	}
	err := g.Send(context.Background(), Message{Kind: KindPriceDrop, Title: "T", Body: "**B**", Priority: 6, ClickURL: "https://pr.example/products/1"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "POST /gotify/message" || gotKey != "tok123" || !strings.HasPrefix(gotCT, "application/json") {
		t.Fatalf("request: %q key=%q ct=%q", gotPath, gotKey, gotCT)
	}
	if gotBody["title"] != "T" || gotBody["message"] != "**B**" || gotBody["priority"] != 6.0 {
		t.Fatalf("body: %v", gotBody)
	}
	extras := gotBody["extras"].(map[string]any)
	disp := extras["client::display"].(map[string]any)
	click := extras["client::notification"].(map[string]any)["click"].(map[string]any)
	if disp["contentType"] != "text/markdown" || click["url"] != "https://pr.example/products/1" {
		t.Fatalf("extras: %v", extras)
	}

	// no click URL → no client::notification extra
	if err := g.Send(context.Background(), Message{Title: "T", Body: "B"}); err != nil {
		t.Fatal(err)
	}
	extras = gotBody["extras"].(map[string]any)
	if _, ok := extras["client::notification"]; ok {
		t.Fatalf("unexpected click extra: %v", extras)
	}
}

func TestGotifyErrors(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		retryable bool
	}{
		{"unauthorized", 401, false},
		{"bad request", 400, false},
		{"not found", 404, false},
		{"server error", 500, true},
		{"bad gateway", 502, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				w.Write([]byte(`{"error":"Unauthorized","errorDescription":"you need to provide a valid access token"}`))
			}))
			defer srv.Close()
			err := NewGotify(srv.URL, "t").Send(context.Background(), Message{Title: "x"})
			var se *SendError
			if !errors.As(err, &se) || se.Status != tt.status {
				t.Fatalf("err = %v", err)
			}
			if IsRetryable(err) != tt.retryable {
				t.Fatalf("retryable = %v", IsRetryable(err))
			}
			if !strings.Contains(err.Error(), "valid access token") {
				t.Fatalf("message should include the response: %v", err)
			}
		})
	}

	t.Run("network error is retryable", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		u := srv.URL
		srv.Close()
		err := NewGotify(u, "t").Send(context.Background(), Message{})
		if err == nil || !IsRetryable(err) {
			t.Fatalf("err = %v retryable=%v", err, IsRetryable(err))
		}
	})
	t.Run("cancelled context is permanent", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		defer srv.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := NewGotify(srv.URL, "t").Send(ctx, Message{})
		if err == nil || IsRetryable(err) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("bad base url", func(t *testing.T) {
		if err := NewGotify("://bad", "t").Send(context.Background(), Message{}); err == nil || IsRetryable(err) {
			t.Fatalf("err = %v", err)
		}
	})
	if IsRetryable(nil) || IsRetryable(errors.New("plain")) {
		t.Fatal("plain errors are not retryable")
	}
}
