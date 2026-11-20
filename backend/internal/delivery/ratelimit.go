package delivery

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// Limiter is a distributed GCRA (generic cell rate algorithm) limiter: each
// call reserves the next free send slot for the channel and returns how long
// to wait for it. Slots are spaced 1/rate apart with no burst, so any one
// second holds at most `rate` sends across every worker process.
type Limiter struct {
	rdb      *redis.Client
	interval time.Duration
}

func NewLimiter(rdb *redis.Client, perSecond int) *Limiter {
	return &Limiter{rdb: rdb, interval: time.Second / time.Duration(perSecond)}
}

// The theoretical arrival time (TAT) is stored in microseconds and read
// against Redis' own clock, so worker clock skew does not matter.
var gcraScript = redis.NewScript(`
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000000 + tonumber(t[2])
local interval = tonumber(ARGV[1])
local tat = tonumber(redis.call('GET', KEYS[1]) or now)
if tat < now then tat = now end
redis.call('SET', KEYS[1], tat + interval, 'PX', math.ceil((tat + interval - now) / 1000) + 1000)
return tat - now
`)

// Reserve returns the delay until the reserved slot.
func (l *Limiter) Reserve(ctx context.Context, channel string) (time.Duration, error) {
	us, err := gcraScript.Run(ctx, l.rdb, []string{RateKey(channel)}, l.interval.Microseconds()).Int64()
	if err != nil {
		return 0, err
	}
	return time.Duration(us) * time.Microsecond, nil
}

// Wait reserves a slot and sleeps until it.
func (l *Limiter) Wait(ctx context.Context, channel string) (time.Duration, error) {
	d, err := l.Reserve(ctx, channel)
	if err != nil || d <= 0 {
		return 0, err
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return d, ctx.Err()
	case <-t.C:
		return d, nil
	}
}
