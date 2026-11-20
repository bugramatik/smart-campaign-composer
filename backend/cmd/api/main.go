// Command api serves the HTTP API: campaign CRUD, Claude-backed generation
// (SSE), approval, and the live dashboard stream.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"scc/internal/api"
	"scc/internal/app"
	"scc/internal/campaign"
	"scc/internal/composer"
	"scc/internal/config"
	"scc/internal/delivery"
	"scc/internal/llm"
	"scc/internal/llm/demo"
	"scc/internal/segment"
)

func main() {
	log := app.Logger("api")
	if err := run(log); err != nil {
		log.Error("api stopped", "err", err)
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

	pool, err := app.Postgres(ctx, cfg.DatabaseURL, 20, log)
	if err != nil {
		return err
	}
	defer pool.Close()
	rdb, err := app.Redis(ctx, cfg.RedisURL, log)
	if err != nil {
		return err
	}
	defer rdb.Close()

	client := newLLM(cfg)
	log.Info("llm configured", "provider", client.Provider(), "model", client.Model())

	campaigns := campaign.NewStore(pool)
	handler := api.New(api.Deps{
		Pool:        pool,
		Redis:       rdb,
		Campaigns:   campaign.NewService(campaigns, segment.NewStore(pool)),
		Deliveries:  delivery.NewStore(pool),
		Queue:       delivery.NewQueue(rdb),
		Composer:    composer.New(client, campaigns, cfg.LLMMaxAttempts, log),
		LLM:         client,
		LLMTimeout:  cfg.LLMTimeout,
		ProviderURL: cfg.ProviderBaseURL,
		Log:         log,
	}).Handler()

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// No WriteTimeout: SSE responses stay open for minutes. Per-request
		// work is bounded by context timeouts instead.
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", cfg.HTTPAddr)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

func newLLM(cfg config.Config) llm.Client {
	if cfg.LLMProvider == "anthropic" {
		return llm.NewAnthropic(llm.AnthropicConfig{
			APIKey: cfg.AnthropicAPIKey, Model: cfg.AnthropicModel,
			Effort: cfg.AnthropicEffort, Fallbacks: cfg.LLMFallbacks,
		})
	}
	return demo.New()
}
