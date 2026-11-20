// Package api is the HTTP API used by the web UI.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"scc/internal/campaign"
	"scc/internal/composer"
	"scc/internal/delivery"
	"scc/internal/httpx"
	"scc/internal/llm"
	"scc/internal/metrics"
	"scc/internal/validation"
)

type Deps struct {
	Pool        *pgxpool.Pool
	Redis       *redis.Client
	Campaigns   *campaign.Service
	Deliveries  *delivery.Store
	Queue       *delivery.Queue
	Composer    *composer.Composer
	LLM         llm.Client
	LLMTimeout  time.Duration
	ProviderURL string // mock provider, proxied for the dashboard
	Log         *slog.Logger
}

type API struct {
	Deps
	http *http.Client
}

func New(d Deps) *API {
	return &API{Deps: d, http: &http.Client{Timeout: 2 * time.Second}}
}

// handlerFunc returns an error instead of writing it, so error mapping and
// logging happen in one place.
type handlerFunc func(w http.ResponseWriter, r *http.Request) error

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	handle := func(pattern string, h handlerFunc) {
		mux.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := h(w, r); err != nil {
				a.writeError(w, r, err)
			}
		}))
	}

	handle("GET /api/meta", a.meta)
	handle("GET /api/stats", a.overview)
	handle("GET /api/provider/stats", a.providerProxy("/stats"))
	handle("GET /api/provider/messages", a.providerProxy("/v1/messages"))
	handle("POST /api/segments/preview", a.previewSegment)

	handle("GET /api/campaigns", a.listCampaigns)
	handle("POST /api/campaigns", a.createCampaign)
	handle("GET /api/campaigns/{id}", a.getCampaign)
	handle("POST /api/campaigns/{id}/generate", a.generate)
	handle("PUT /api/campaigns/{id}/segment", a.updateSegment)
	handle("PUT /api/campaigns/{id}/content", a.updateContent)
	handle("POST /api/campaigns/{id}/content/check", a.checkContent)
	handle("POST /api/campaigns/{id}/approve", a.approve)
	handle("POST /api/campaigns/{id}/cancel", a.cancel)
	handle("GET /api/campaigns/{id}/stats", a.campaignStats)
	handle("GET /api/campaigns/{id}/events", a.campaignEvents)
	handle("GET /api/campaigns/{id}/deliveries", a.listDeliveries)
	handle("GET /api/campaigns/{id}/llm-calls", a.llmCalls)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	handle("GET /readyz", a.ready)
	mux.Handle("GET /metrics", metrics.Handler())

	return a.recoverer(requestID(metrics.Instrument(mux)))
}

// ready reports whether the dependencies answer; the orchestrator stops
// routing traffic to an instance that is not ready.
func (a *API) ready(w http.ResponseWriter, r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	checks := map[string]string{"postgres": "ok", "redis": "ok"}
	status := http.StatusOK
	if err := a.Pool.Ping(ctx); err != nil {
		checks["postgres"], status = err.Error(), http.StatusServiceUnavailable
	}
	if err := a.Redis.Ping(ctx).Err(); err != nil {
		checks["redis"], status = err.Error(), http.StatusServiceUnavailable
	}
	httpx.WriteJSON(w, status, map[string]any{"checks": checks})
	return nil
}

func (a *API) writeError(w http.ResponseWriter, r *http.Request, err error) {
	se := toStatusError(err)
	if se.Status >= 500 {
		a.Log.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "err", err, "request_id", w.Header().Get("X-Request-ID"))
	}
	httpx.WriteError(w, se)
}

func toStatusError(err error) *httpx.StatusError {
	var (
		se      *httpx.StatusError
		ve      *validation.Error
		refusal *llm.RefusalError
	)
	switch {
	case errors.As(err, &se):
		return se
	case errors.As(err, &ve):
		return &httpx.StatusError{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "the request did not pass validation", Details: ve.Issues}
	case errors.Is(err, campaign.ErrNotFound):
		return httpx.Errorf(http.StatusNotFound, "not_found", "campaign not found")
	case errors.Is(err, campaign.ErrInvalidState):
		return httpx.Errorf(http.StatusConflict, "invalid_state", "%s", err.Error())
	case errors.As(err, &refusal):
		return &httpx.StatusError{Status: http.StatusUnprocessableEntity, Code: "llm_refused", Message: "Claude declined this brief", Details: refusal}
	case errors.Is(err, context.DeadlineExceeded):
		return httpx.Errorf(http.StatusGatewayTimeout, "timeout", "the operation timed out")
	}
	return httpx.Errorf(http.StatusInternalServerError, "internal", "internal error")
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, httpx.Errorf(http.StatusNotFound, "not_found", "campaign not found")
	}
	return id, nil
}

func queryInt(r *http.Request, key string, def, lo, hi int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return def
	}
	return max(lo, min(hi, v))
}

// providerProxy exposes the mock provider's observability endpoints to the
// dashboard (the browser cannot reach the provider directly).
func (a *API) providerProxy(path string) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, a.ProviderURL+path+"?"+r.URL.RawQuery, nil)
		if err != nil {
			return err
		}
		resp, err := a.http.Do(req)
		if err != nil {
			return httpx.Errorf(http.StatusBadGateway, "provider_unavailable", "provider is unreachable")
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, err = io.Copy(w, io.LimitReader(resp.Body, 1<<20))
		return err
	}
}

func (a *API) fetchProviderStats(ctx context.Context) json.RawMessage {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.ProviderURL+"/stats", nil)
	if err != nil {
		return nil
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil || resp.StatusCode != http.StatusOK || !json.Valid(b) {
		return nil
	}
	return b
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" || len(id) > 64 {
			var b [8]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r)
	})
}

func (a *API) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				a.Log.ErrorContext(r.Context(), "panic", "value", v, "stack", string(debug.Stack()))
				httpx.WriteError(w, httpx.Errorf(http.StatusInternalServerError, "internal", "internal error"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}
