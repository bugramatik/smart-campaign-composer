// Command seed migrates the database and fills it with fake users and events
// if it is empty.
package main

import (
	"flag"
	"os"
	"time"

	"scc/internal/app"
	"scc/internal/config"
	"scc/internal/seed"
)

func main() {
	users := flag.Int("users", 4000, "number of fake users")
	days := flag.Int("days", 180, "days of event history")
	rngSeed := flag.Uint64("seed", 42, "random seed (same seed, same data)")
	flag.Parse()

	log := app.Logger("seed")
	cfg, err := config.Load()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	ctx, stop := app.SignalContext()
	defer stop()
	pool, err := app.Postgres(ctx, cfg.DatabaseURL, 4, log)
	if err != nil {
		log.Error("postgres", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	start := time.Now()
	res, err := seed.Run(ctx, pool, seed.Options{Users: *users, Days: *days, Seed: *rngSeed, Now: time.Now()})
	if err != nil {
		log.Error("seed failed", "err", err)
		os.Exit(1)
	}
	if res.Skipped {
		log.Info("database already seeded; nothing to do")
		return
	}
	log.Info("seeded", "users", res.Users, "events", res.Events, "took", time.Since(start).Round(time.Millisecond))
}
