// Package app holds process bootstrap shared by the binaries.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"scc/internal/db"
)

// Logger is a JSON logger at LOG_LEVEL (default info).
func Logger(service string) *slog.Logger {
	var level slog.Level
	_ = level.UnmarshalText([]byte(os.Getenv("LOG_LEVEL")))
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})).With("service", service)
}

// SignalContext is cancelled on SIGINT/SIGTERM.
func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// Postgres connects (retrying while the database starts) and migrates.
func Postgres(ctx context.Context, url string, maxConns int32, log *slog.Logger) (*pgxpool.Pool, error) {
	var pool *pgxpool.Pool
	err := retry(ctx, log, "postgres", func(ctx context.Context) (err error) {
		pool, err = db.Connect(ctx, url, maxConns)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func Redis(ctx context.Context, url string, log *slog.Logger) (*redis.Client, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	rdb := redis.NewClient(opt)
	if err := retry(ctx, log, "redis", func(ctx context.Context) error { return rdb.Ping(ctx).Err() }); err != nil {
		_ = rdb.Close()
		return nil, err
	}
	return rdb, nil
}

func retry(ctx context.Context, log *slog.Logger, what string, f func(context.Context) error) error {
	deadline := time.Now().Add(60 * time.Second)
	for wait := 250 * time.Millisecond; ; wait = min(2*wait, 4*time.Second) {
		attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := f(attemptCtx)
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("connect %s: %w", what, err)
		}
		log.Warn("waiting for dependency", "dependency", what, "err", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}
