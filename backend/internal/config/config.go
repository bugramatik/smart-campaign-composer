// Package config loads process configuration from the environment once, at startup.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	RedisURL    string

	// LLM
	LLMProvider     string // "anthropic" | "mock"
	AnthropicAPIKey string
	AnthropicModel  string
	AnthropicEffort string // low | medium | high | xhigh | max
	LLMFallbacks    bool   // server-side fallback to another model on overload
	LLMMaxAttempts  int
	LLMTimeout      time.Duration

	// Delivery
	ProviderBaseURL   string
	ProviderTimeout   time.Duration
	RatePerSecond     int // per channel
	WorkerConcurrency int // per channel
	MaxAttempts       int
	RetryBaseDelay    time.Duration
	RetryMaxDelay     time.Duration
	VisibilityTimeout time.Duration
}

// Load reads the environment. LLM_PROVIDER defaults to "anthropic" when an
// API key is present and to "mock" otherwise, so the stack runs without a key.
func Load() (Config, error) {
	c := Config{
		HTTPAddr:          env("HTTP_ADDR", ":8080"),
		DatabaseURL:       env("DATABASE_URL", "postgres://scc:scc@localhost:5432/scc?sslmode=disable"),
		RedisURL:          env("REDIS_URL", "redis://localhost:6379/0"),
		AnthropicAPIKey:   os.Getenv("ANTHROPIC_API_KEY"),
		AnthropicModel:    env("ANTHROPIC_MODEL", "claude-opus-5-5"),
		AnthropicEffort:   env("ANTHROPIC_EFFORT", "medium"),
		LLMFallbacks:      env("LLM_FALLBACKS", "true") == "true",
		ProviderBaseURL:   env("PROVIDER_BASE_URL", "http://localhost:8090"),
		LLMProvider:       os.Getenv("LLM_PROVIDER"),
		LLMMaxAttempts:    envInt("LLM_MAX_ATTEMPTS", 3),
		LLMTimeout:        envDuration("LLM_TIMEOUT", 3*time.Minute),
		ProviderTimeout:   envDuration("PROVIDER_TIMEOUT", 3*time.Second),
		RatePerSecond:     envInt("RATE_PER_SECOND", 100),
		WorkerConcurrency: envInt("WORKER_CONCURRENCY", 32),
		MaxAttempts:       envInt("DELIVERY_MAX_ATTEMPTS", 8),
		RetryBaseDelay:    envDuration("RETRY_BASE_DELAY", 500*time.Millisecond),
		RetryMaxDelay:     envDuration("RETRY_MAX_DELAY", 30*time.Second),
		VisibilityTimeout: envDuration("VISIBILITY_TIMEOUT", 2*time.Minute),
	}
	if c.LLMProvider == "" {
		c.LLMProvider = "mock"
		if c.AnthropicAPIKey != "" {
			c.LLMProvider = "anthropic"
		}
	}
	switch c.LLMProvider {
	case "mock":
	case "anthropic":
		if c.AnthropicAPIKey == "" {
			return c, fmt.Errorf("LLM_PROVIDER=anthropic requires ANTHROPIC_API_KEY")
		}
	default:
		return c, fmt.Errorf("unknown LLM_PROVIDER %q (want anthropic or mock)", c.LLMProvider)
	}
	if c.RatePerSecond <= 0 || c.WorkerConcurrency <= 0 || c.MaxAttempts <= 0 {
		return c, fmt.Errorf("RATE_PER_SECOND, WORKER_CONCURRENCY and DELIVERY_MAX_ATTEMPTS must be positive")
	}
	return c, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return v
	}
	return def
}
