// Command mockprovider is a fake SMS/email provider with injected latency,
// rate limiting, 5xx errors and timeouts. It never delivers anything.
package main

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"time"

	"scc/internal/app"
	"scc/internal/provider"
)

func main() {
	log := app.Logger("mockprovider")
	cfg := provider.DefaultMockConfig()
	cfg.RateLimitPerSecond = envInt("MOCK_RATE_LIMIT", cfg.RateLimitPerSecond)
	cfg.Inject429 = envFloat("MOCK_429_RATE", cfg.Inject429)
	cfg.Inject500 = envFloat("MOCK_500_RATE", cfg.Inject500)
	cfg.InjectTimeout = envFloat("MOCK_TIMEOUT_RATE", cfg.InjectTimeout)

	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8090"
	}
	srv := &http.Server{Addr: addr, Handler: provider.NewMock(cfg).Handler(), ReadHeaderTimeout: 5 * time.Second}

	ctx, stop := app.SignalContext()
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Info("mock provider listening", "addr", addr, "rate_limit", cfg.RateLimitPerSecond,
		"p429", cfg.Inject429, "p500", cfg.Inject500, "ptimeout", cfg.InjectTimeout)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("mock provider stopped", "err", err)
		os.Exit(1)
	}
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

func envFloat(k string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(k), 64); err == nil {
		return v
	}
	return def
}
