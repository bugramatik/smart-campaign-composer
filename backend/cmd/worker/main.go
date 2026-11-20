// Command worker schedules due campaigns and delivers their messages through
// the provider. Run as many replicas as needed: claims are fenced in Postgres
// and the rate limit is shared through Redis.
package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"scc/internal/app"
	"scc/internal/config"
	"scc/internal/delivery"
	"scc/internal/metrics"
	"scc/internal/provider"
	"scc/internal/segment"
)

func main() {
	log := app.Logger("worker")
	if err := run(log); err != nil {
		log.Error("worker stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := app.SignalContext()
	defer stop()

	pool, err := app.Postgres(ctx, cfg.DatabaseURL, int32(2*cfg.WorkerConcurrency+8), log)
	if err != nil {
		return err
	}
	defer pool.Close()
	rdb, err := app.Redis(ctx, cfg.RedisURL, log)
	if err != nil {
		return err
	}
	defer rdb.Close()

	host, _ := os.Hostname()
	w := delivery.NewWorker(delivery.Config{
		Consumer:          fmt.Sprintf("%s-%d", host, os.Getpid()),
		Channels:          segment.Channels,
		Concurrency:       cfg.WorkerConcurrency,
		MaxAttempts:       cfg.MaxAttempts,
		Backoff:           delivery.Backoff{Base: cfg.RetryBaseDelay, Max: cfg.RetryMaxDelay},
		VisibilityTimeout: cfg.VisibilityTimeout,
		SendTimeout:       cfg.ProviderTimeout,
	},
		delivery.NewStore(pool),
		delivery.NewQueue(rdb),
		delivery.NewLimiter(rdb, cfg.RatePerSecond),
		provider.NewClient(cfg.ProviderBaseURL, cfg.ProviderTimeout),
		segment.NewStore(pool),
		log,
	)

	// Metrics and liveness on a side port.
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	srv := &http.Server{Addr: envOr("METRICS_ADDR", ":9091"), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("metrics server", "err", err)
		}
	}()
	defer srv.Close()

	log.Info("worker started", "rate_per_second", cfg.RatePerSecond, "concurrency", cfg.WorkerConcurrency)
	return w.Run(ctx)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
