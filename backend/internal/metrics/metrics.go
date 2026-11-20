// Package metrics declares the Prometheus metrics of every process. They are
// registered on the default registry and served by promhttp at /metrics.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	LLMRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "scc_llm_requests_total", Help: "LLM calls by purpose and outcome (ok, invalid, error, refusal).",
	}, []string{"purpose", "outcome"})
	LLMLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "scc_llm_request_duration_seconds", Help: "LLM call duration.",
		Buckets: []float64{0.5, 1, 2, 5, 10, 20, 40, 80},
	}, []string{"purpose"})
	LLMTokens = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "scc_llm_tokens_total", Help: "LLM tokens by purpose and direction.",
	}, []string{"purpose", "direction"})

	Deliveries = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "scc_deliveries_total", Help: "Delivery attempt outcomes (sent, retry, failed, skipped, duplicate).",
	}, []string{"channel", "outcome"})
	ProviderLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "scc_provider_request_duration_seconds", Help: "Provider call duration by channel and HTTP status class.",
		Buckets: prometheus.DefBuckets,
	}, []string{"channel", "status"})
	RateLimitWait = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "scc_ratelimit_wait_seconds", Help: "Time spent waiting for a send slot.",
		Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2},
	}, []string{"channel"})
	QueueDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "scc_queue_depth", Help: "Messages waiting per channel and stage (pending, retry).",
	}, []string{"channel", "stage"})

	HTTPRequests = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "scc_http_request_duration_seconds", Help: "API request duration by route and status.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route", "status"})
)

func Handler() http.Handler { return promhttp.Handler() }

// Instrument records duration per route pattern (not raw path, to keep
// label cardinality bounded).
func Instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		HTTPRequests.WithLabelValues(route, strconv.Itoa(sw.status)).Observe(time.Since(start).Seconds())
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach Flush on the real writer (SSE).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
