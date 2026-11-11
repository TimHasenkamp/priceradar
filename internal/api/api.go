// Package api implements the REST/JSON API described in docs/API.md.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"priceradar/internal/notify"
	"priceradar/internal/scheduler"
	"priceradar/internal/scraper"
	"priceradar/internal/store"
)

// Scrapes is the part of the scheduler the API needs.
type Scrapes interface {
	Add(ctx context.Context, rawURL string, targetCents *int64) (store.Product, error)
	Refresh(ctx context.Context, id int64) (store.PriceCheck, error)
}

// Pinger reports the health of a dependency.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Server holds the API dependencies.
type Server struct {
	Store   *store.Store
	Scrapes Scrapes
	Fetcher Pinger // optional
	Log     *slog.Logger
	// ScrapeTimeout bounds POST /products and POST /refresh.
	ScrapeTimeout time.Duration
	// Notifier sends test notifications; nil means Gotify is not configured.
	Notifier notify.Notifier
	// PublicURL is the web UI base URL (optional), used as the test message's click target.
	PublicURL string
}

// Handler returns the HTTP handler with all routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/products", s.listProducts)
	mux.HandleFunc("POST /api/products", s.createProduct)
	mux.HandleFunc("GET /api/products/{id}", s.getProduct)
	mux.HandleFunc("PATCH /api/products/{id}", s.patchProduct)
	mux.HandleFunc("DELETE /api/products/{id}", s.deleteProduct)
	mux.HandleFunc("GET /api/products/{id}/prices", s.listPrices)
	mux.HandleFunc("GET /api/products/{id}/checks", s.listChecks)
	mux.HandleFunc("POST /api/products/{id}/refresh", s.refresh)
	mux.HandleFunc("GET /api/products/{id}/notifications", s.listNotifications)
	mux.HandleFunc("POST /api/notifications/test", s.testNotification)
	mux.HandleFunc("GET /api/healthz", s.health)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint")
	})
	return s.logRequests(mux)
}

// ---- handlers ----

func (s *Server) listProducts(w http.ResponseWriter, r *http.Request) {
	ps, err := s.Store.ListProducts(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]productJSON, 0, len(ps))
	for _, p := range ps {
		out = append(out, toProductJSON(p))
	}
	writeJSON(w, http.StatusOK, out)
}

type createRequest struct {
	URL              string `json:"url"`
	TargetPriceCents *int64 `json:"target_price_cents"`
}

func (s *Server) createProduct(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.URL) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "url is required")
		return
	}
	if req.TargetPriceCents != nil && *req.TargetPriceCents <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "target_price_cents must be > 0")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.scrapeTimeout())
	defer cancel()
	p, err := s.Scrapes.Add(ctx, req.URL, req.TargetPriceCents)
	switch {
	case err == nil:
	case errors.Is(err, context.Canceled):
		return // client went away; nothing to answer
	case errors.Is(err, scheduler.ErrShuttingDown):
		writeError(w, http.StatusServiceUnavailable, "unavailable", "server is shutting down")
		return
	case errors.Is(err, scraper.ErrInvalidURL):
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	case errors.Is(err, scraper.ErrUnsupported):
		writeError(w, http.StatusUnprocessableEntity, "unsupported_shop", err.Error())
		return
	case errors.Is(err, store.ErrDuplicate):
		writeError(w, http.StatusConflict, "duplicate", "product already exists")
		return
	case errors.Is(err, scheduler.ErrScrape):
		writeError(w, http.StatusBadGateway, "scrape_failed", fmt.Sprintf("%s: %v", scraper.KindOf(err), err))
		return
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "timeout", "fetching the product page took too long")
		return
	default:
		s.internal(w, r, err)
		return
	}
	pw, err := s.Store.GetProductWithStats(r.Context(), p.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toProductJSON(pw))
}

func (s *Server) getProduct(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	s.writeProduct(w, r, id, http.StatusOK)
}

func (s *Server) patchProduct(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var raw map[string]json.RawMessage
	if !decode(w, r, &raw) {
		return
	}
	var patch store.ProductPatch
	for k, v := range raw {
		isNull := string(v) == "null"
		var err error
		switch k {
		case "custom_name":
			if isNull {
				patch.ClearCustomName = true
				continue
			}
			var name string
			if err = json.Unmarshal(v, &name); err == nil {
				if name = strings.TrimSpace(name); name == "" {
					patch.ClearCustomName = true
				} else if len(name) > 300 {
					err = errors.New("too long")
				} else {
					patch.CustomName = &name
				}
			}
		case "paused":
			var b bool
			if isNull {
				err = errors.New("must be true or false")
			} else if err = json.Unmarshal(v, &b); err == nil {
				patch.Paused = &b
			}
		case "notify":
			var b bool
			if isNull {
				err = errors.New("must be true or false")
			} else if err = json.Unmarshal(v, &b); err == nil {
				patch.Notify = &b
			}
		case "target_price_cents":
			if isNull {
				patch.ClearTargetPrice = true
				continue
			}
			var c int64
			if err = json.Unmarshal(v, &c); err == nil && c <= 0 {
				err = errors.New("must be > 0")
			}
			patch.TargetPriceCents = &c
		default:
			err = errors.New("unknown field")
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("%s: %v", k, err))
			return
		}
	}
	if _, err := s.Store.UpdateProduct(r.Context(), id, patch); err != nil {
		s.storeError(w, r, err)
		return
	}
	s.writeProduct(w, r, id, http.StatusOK)
}

func (s *Server) deleteProduct(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteProduct(r.Context(), id); err != nil {
		s.storeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listPrices(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var from, to *time.Time
	for name, dst := range map[string]**time.Time{"from": &from, "to": &to} {
		if v := r.URL.Query().Get(name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid_request", name+" must be RFC3339")
				return
			}
			*dst = &t
		}
	}
	if _, err := s.Store.GetProduct(r.Context(), id); err != nil {
		s.storeError(w, r, err)
		return
	}
	checks, err := s.Store.ListPrices(r.Context(), id, from, to)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]pricePointJSON, 0, len(checks))
	for _, c := range checks {
		out = append(out, pricePointJSON{c.CheckedAt.UTC(), deref(c.PriceCents), c.OldPriceCents, c.RRPCents, c.Availability})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listChecks(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	limit, ok := queryLimit(w, r)
	if !ok {
		return
	}
	if _, err := s.Store.GetProduct(r.Context(), id); err != nil {
		s.storeError(w, r, err)
		return
	}
	checks, err := s.Store.ListChecks(r.Context(), id, limit)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]checkJSON, 0, len(checks))
	for _, c := range checks {
		out = append(out, toCheckJSON(c))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	limit, ok := queryLimit(w, r)
	if !ok {
		return
	}
	if _, err := s.Store.GetProduct(r.Context(), id); err != nil {
		s.storeError(w, r, err)
		return
	}
	list, err := s.Store.ListNotifications(r.Context(), id, limit)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]notificationJSON, 0, len(list))
	for _, n := range list {
		out = append(out, toNotificationJSON(n))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) testNotification(w http.ResponseWriter, r *http.Request) {
	if s.Notifier == nil {
		writeError(w, http.StatusConflict, "not_configured", "Gotify is not configured (set PR_GOTIFY_URL and PR_GOTIFY_TOKEN)")
		return
	}
	if err := s.Notifier.Send(r.Context(), notify.TestMessage(s.PublicURL)); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		s.Log.Warn("test notification failed", "err", err)
		writeError(w, http.StatusBadGateway, "notify_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.scrapeTimeout())
	defer cancel()
	c, err := s.Scrapes.Refresh(ctx, id)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		if errors.Is(err, scheduler.ErrShuttingDown) {
			writeError(w, http.StatusServiceUnavailable, "unavailable", "server is shutting down")
			return
		}
		if errors.Is(err, context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "timeout", "refresh took too long (another scrape may be running)")
			return
		}
		s.storeError(w, r, err)
		return
	}
	p, err := s.Store.GetProductWithStats(r.Context(), id)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"product": toProductJSON(p), "check": toCheckJSON(c)})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	out := map[string]string{"status": "ok", "db": "ok", "fetcher": "ok", "gotify": "not_configured"}
	if s.Notifier != nil {
		out["gotify"] = "configured" // deliberately not pinged
	}
	code := http.StatusOK
	if err := s.Store.Ping(ctx); err != nil {
		out["db"], out["status"], code = "down", "degraded", http.StatusServiceUnavailable
	}
	if s.Fetcher != nil {
		if err := s.Fetcher.Ping(ctx); err != nil {
			out["fetcher"], out["status"] = "down", "degraded"
		}
	}
	writeJSON(w, code, out)
}

// ---- helpers ----

func (s *Server) writeProduct(w http.ResponseWriter, r *http.Request, id int64, code int) {
	p, err := s.Store.GetProductWithStats(r.Context(), id)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	writeJSON(w, code, toProductJSON(p))
}

func (s *Server) scrapeTimeout() time.Duration {
	if s.ScrapeTimeout > 0 {
		return s.ScrapeTimeout
	}
	return 3 * time.Minute
}

func (s *Server) storeError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "product not found")
		return
	}
	s.internal(w, r, err)
}

func (s *Server) internal(w http.ResponseWriter, r *http.Request, err error) {
	s.Log.Error("internal error", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "internal error")
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) { r.status = code; r.ResponseWriter.WriteHeader(code) }

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		level := slog.LevelDebug
		if rec.status >= 500 {
			level = slog.LevelWarn
		}
		s.Log.Log(r.Context(), level, "http", "method", r.Method, "path", r.URL.Path, "status", rec.status, "took_ms", time.Since(start).Milliseconds())
	})
}

// queryLimit parses ?limit= (default 50, 1..500).
func queryLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return 50, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 500 {
		writeError(w, http.StatusBadRequest, "invalid_request", "limit must be 1..500")
		return 0, false
	}
	return n, true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid product id")
		return 0, false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, errCode, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]string{"code": errCode, "message": msg}})
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
