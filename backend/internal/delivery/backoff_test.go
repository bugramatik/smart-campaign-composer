package delivery

import (
	"testing"
	"time"
)

func TestBackoffCeiling(t *testing.T) {
	var bound int64
	b := Backoff{Base: time.Second, Max: 30 * time.Second, rand: func(n int64) int64 { bound = n; return n - 1 }}
	for attempt, want := range map[int]time.Duration{0: time.Second, 1: time.Second, 2: 2 * time.Second, 4: 8 * time.Second, 6: 30 * time.Second, 64: 30 * time.Second} {
		if d := b.Delay(attempt, 0); d != want || bound != int64(want)+1 {
			t.Errorf("attempt %d: delay %v (bound %v), want %v", attempt, d, time.Duration(bound), want)
		}
	}
}

func TestBackoffIsJittered(t *testing.T) {
	b := Backoff{Base: 100 * time.Millisecond, Max: 10 * time.Second}
	seen := map[time.Duration]bool{}
	for range 50 {
		d := b.Delay(5, 0)
		if d < 0 || d > 1600*time.Millisecond {
			t.Fatalf("delay %v outside [0, 1.6s]", d)
		}
		seen[d] = true
	}
	if len(seen) < 10 {
		t.Fatalf("only %d distinct delays in 50 draws", len(seen))
	}
}

func TestBackoffHonoursRetryAfter(t *testing.T) {
	b := Backoff{Base: 100 * time.Millisecond, Max: time.Second, rand: func(int64) int64 { return 0 }}
	if d := b.Delay(1, 3*time.Second); d != 3*time.Second {
		t.Fatalf("delay %v, want the 3s Retry-After floor", d)
	}
}
