package delivery

import (
	"math/rand/v2"
	"time"
)

// Backoff is capped exponential backoff with full jitter: the delay for
// attempt n is uniform in [0, min(Max, Base·2^(n-1))]. Full jitter spreads a
// burst of failures (a provider blip hits hundreds of messages at once) so
// the retries do not arrive together and cause the next blip.
type Backoff struct {
	Base time.Duration
	Max  time.Duration
	rand func(n int64) int64
}

func (b Backoff) Delay(attempt int, retryAfter time.Duration) time.Duration {
	ceiling := b.Max
	if attempt < 1 {
		attempt = 1
	}
	if shift := attempt - 1; shift < 32 {
		if d := b.Base << shift; d > 0 && d < ceiling {
			ceiling = d
		}
	}
	rnd := b.rand
	if rnd == nil {
		rnd = rand.Int64N
	}
	d := time.Duration(rnd(int64(ceiling) + 1))
	// The provider's Retry-After is a floor: retrying sooner is pointless.
	if retryAfter > 0 && d < retryAfter {
		d = retryAfter + time.Duration(rnd(int64(b.Base)+1))
	}
	return d
}
