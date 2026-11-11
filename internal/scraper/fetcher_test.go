package scraper

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSidecarFetcher(t *testing.T) {
	tests := []struct {
		name       string
		handler    http.HandlerFunc
		wantErr    error
		wantStatus int
		wantHTML   string
	}{
		{"ok", func(w http.ResponseWriter, r *http.Request) {
			var req sidecarRequest
			if r.Method != http.MethodPost || r.URL.Path != "/fetch" || json.NewDecoder(r.Body).Decode(&req) != nil || req.URL != "https://shop.test/p/1" || req.TimeoutMS != 5000 || req.WaitSelector != "main" {
				t.Errorf("bad request: %s %s %+v", r.Method, r.URL.Path, req)
			}
			_ = json.NewEncoder(w).Encode(sidecarResponse{Status: 200, FinalURL: req.URL, HTML: "<html>ok</html>"})
		}, nil, 200, "<html>ok</html>"},
		{"upstream 404 is not an error", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(sidecarResponse{Status: 404})
		}, nil, 404, ""},
		{"sidecar error", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(sidecarResponse{Error: "browser crashed"})
		}, ErrTransient, 0, ""},
		{"garbage", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("not json"))
		}, ErrTransient, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()
			f := &SidecarFetcher{BaseURL: srv.URL + "/", Timeout: 5 * time.Second, WaitSelector: "main"}
			p, err := f.Get(context.Background(), "https://shop.test/p/1")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if p.Status != tt.wantStatus || string(p.HTML) != tt.wantHTML {
				t.Errorf("page = %d %q", p.Status, p.HTML)
			}
		})
	}
}

func TestSidecarFetcher_UnreachableAndPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
		}
	}))
	f := &SidecarFetcher{BaseURL: srv.URL}
	if err := f.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	srv.Close()
	if err := f.Ping(context.Background()); err == nil {
		t.Fatal("Ping on closed server should fail")
	}
	if _, err := f.Get(context.Background(), "https://x.test/"); !errors.Is(err, ErrTransient) {
		t.Fatalf("Get on closed server: %v", err)
	}
}

func TestSidecarFetcher_RejectionNotRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(sidecarResponse{Error: "host not allowed: evil.test"})
	}))
	defer srv.Close()
	_, err := (&SidecarFetcher{BaseURL: srv.URL}).Get(context.Background(), "https://evil.test/")
	if err == nil || Retryable(err) || KindOf(err) != KindInternal {
		t.Fatalf("err = %v (kind %s), want non-retryable internal", err, KindOf(err))
	}
}
