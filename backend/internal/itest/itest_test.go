// Package itest holds integration tests against real Postgres and Redis.
// They run when TEST_DATABASE_URL and TEST_REDIS_URL are set (make test does
// this with docker-compose.test.yml) and are skipped otherwise. The database
// and Redis DB they point at are wiped.
package itest

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"scc/internal/campaign"
	"scc/internal/content"
	"scc/internal/db"
	"scc/internal/seed"
	"scc/internal/segment"
)

type env struct {
	pool      *pgxpool.Pool
	rdb       *redis.Client
	campaigns *campaign.Service
	log       *slog.Logger
}

// setup returns a migrated, seeded, otherwise empty database and an empty
// Redis. Tests in this package share them and must not run in parallel.
func setup(t *testing.T, users int) *env {
	t.Helper()
	dbURL, redisURL := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_REDIS_URL")
	if dbURL == "" || redisURL == "" {
		t.Skip("TEST_DATABASE_URL / TEST_REDIS_URL not set")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, dbURL, 30)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE users, events, campaigns, deliveries, llm_calls RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Run(ctx, pool, seed.Options{Users: users, Days: 90, Seed: 3, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}

	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(opt)
	t.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	return &env{
		pool: pool, rdb: rdb,
		campaigns: campaign.NewService(campaign.NewStore(pool), segment.NewStore(pool)),
		log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func ptr[T any](v T) *T { return &v }

// everyone matches every seeded user.
var everyone = segment.Rule{Match: segment.MatchAll, Conditions: []segment.Condition{
	{Kind: segment.KindNumber, Field: "age", Operator: "gte", Value: ptr(0.0)},
}}

var messages = content.Content{Variants: []content.Variant{
	{Language: "tr", SMS: "Merhaba {{first_name}}, sepetin seni bekliyor!", EmailSubject: "{{first_name}}, sana özel indirim", EmailPreheader: "Bugün geçerli", EmailBody: "Merhaba {{first_name}},\n\n{{city}} için ücretsiz kargo."},
	{Language: "en", SMS: "Hi {{first_name}}, your cart is waiting!", EmailSubject: "{{first_name}}, a discount for you", EmailPreheader: "Today only", EmailBody: "Hi {{first_name}},\n\nFree shipping to {{city}}."},
}}

// draft creates a campaign with a segment and content, ready to approve.
func (e *env) draft(t *testing.T, rule segment.Rule) campaign.Campaign {
	t.Helper()
	ctx := context.Background()
	c, err := e.campaigns.Store.Create(ctx, "integration test campaign")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.campaigns.UpdateSegment(ctx, c.ID, rule); err != nil {
		t.Fatal(err)
	}
	c, err = e.campaigns.UpdateContent(ctx, c.ID, messages)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
