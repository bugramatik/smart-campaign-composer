package itest

import (
	"context"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"scc/internal/campaign"
	"scc/internal/delivery"
	"scc/internal/provider"
	"scc/internal/segment"
)

// TestCampaignDeliversExactlyOnce sends a campaign through two competing
// workers to a provider that fails, rate limits and times out, and checks
// that every consenting user gets exactly one message per channel.
func TestCampaignDeliversExactlyOnce(t *testing.T) {
	e := setup(t, 400)
	ctx := context.Background()

	mock := provider.NewMock(provider.MockConfig{
		RateLimitPerSecond: 150, Inject429: 0.08, Inject500: 0.08, InjectTimeout: 0.04,
		MinLatency: time.Millisecond, MaxLatency: 10 * time.Millisecond, HangFor: time.Second, Seed: 11,
	})
	srv := httptest.NewServer(mock.Handler())
	defer srv.Close()

	c := e.draft(t, everyone)
	if _, err := e.campaigns.Approve(ctx, c.ID, campaign.ApproveInput{Channels: []string{"sms", "email"}}); err != nil {
		t.Fatalf("approve: %v", err)
	}

	runCtx, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	store := delivery.NewStore(e.pool)
	queue := delivery.NewQueue(e.rdb)
	for _, name := range []string{"worker-a", "worker-b"} {
		w := delivery.NewWorker(delivery.Config{
			Consumer: name, Channels: segment.Channels, Concurrency: 8, MaxAttempts: 10,
			Backoff:           delivery.Backoff{Base: 20 * time.Millisecond, Max: 300 * time.Millisecond},
			VisibilityTimeout: 3 * time.Second, SendTimeout: 200 * time.Millisecond,
		}, store, queue, delivery.NewLimiter(e.rdb, 100), provider.NewClient(srv.URL, 200*time.Millisecond),
			segment.NewStore(e.pool), e.log)
		wg.Go(func() { _ = w.Run(runCtx) })
	}
	defer func() { stop(); wg.Wait() }()

	deadline := time.Now().Add(90 * time.Second)
	for {
		got, err := e.campaigns.Store.Get(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == campaign.StatusCompleted {
			break
		}
		if time.Now().After(deadline) {
			stats, _ := store.Stats(ctx, c.ID)
			t.Fatalf("campaign still %s after 90s: %+v", got.Status, stats.Channels)
		}
		time.Sleep(250 * time.Millisecond)
	}

	var wantSMS, wantEmail int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE sms_opt_in), count(*) FILTER (WHERE email_opt_in) FROM users`).Scan(&wantSMS, &wantEmail); err != nil {
		t.Fatal(err)
	}
	stats, err := store.Stats(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n := stats.Channels["sms"]["sent"]; n != wantSMS {
		t.Errorf("sms sent = %d, want %d (all opted-in users); stats %+v", n, wantSMS, stats.Channels)
	}
	if n := stats.Channels["email"]["sent"]; n != wantEmail {
		t.Errorf("email sent = %d, want %d; stats %+v", n, wantEmail, stats.Channels)
	}

	ps := mock.Stats()
	if ps.DuplicateDeliveries != 0 {
		t.Errorf("provider saw %d duplicate deliveries", ps.DuplicateDeliveries)
	}
	if a := ps.Channels["sms"].Accepted + ps.Channels["email"].Accepted; a != wantSMS+wantEmail {
		t.Errorf("provider accepted %d messages, want %d", a, wantSMS+wantEmail)
	}
	faults := 0
	for _, ch := range ps.Channels {
		faults += ch.Injected429 + ch.ServerError + ch.Timeouts + ch.RateLimited
	}
	if faults == 0 {
		t.Error("no faults were injected; the test proves nothing")
	}
	t.Logf("provider stats: %+v", ps.Channels)

	// Every sent row records the provider id it got; none is shared.
	var dupIDs int
	if err := e.pool.QueryRow(ctx, `
		SELECT count(*) FROM (SELECT provider_message_id FROM deliveries
		WHERE campaign_id = $1 AND status = 'sent' GROUP BY 1 HAVING count(*) > 1) d`, c.ID).Scan(&dupIDs); err != nil {
		t.Fatal(err)
	}
	if dupIDs != 0 {
		t.Errorf("%d provider message ids are shared by several deliveries", dupIDs)
	}
}

func TestCancelStopsPendingDeliveries(t *testing.T) {
	e := setup(t, 200)
	ctx := context.Background()
	c := e.draft(t, everyone)
	future := time.Now().Add(time.Hour)
	if _, err := e.campaigns.Approve(ctx, c.ID, campaign.ApproveInput{Channels: []string{"email"}, ScheduledAt: &future}); err != nil {
		t.Fatal(err)
	}
	got, err := e.campaigns.Store.Cancel(ctx, c.ID)
	if err != nil || got.Status != campaign.StatusCancelled {
		t.Fatalf("cancel: %+v %v", got.Status, err)
	}
	if _, ok, err := delivery.NewStore(e.pool).StartDue(ctx, segment.NewCatalog(nil)); err != nil || ok {
		t.Fatalf("a cancelled campaign was started: ok=%v err=%v", ok, err)
	}
	if _, err := e.campaigns.Store.Cancel(ctx, c.ID); err == nil {
		t.Fatal("cancelling twice must fail")
	}
}

// TestLimiterSpacesSendsAcrossWorkers reserves slots from many goroutines
// (standing in for worker processes) and checks no one-second window holds
// more than the limit.
func TestLimiterSpacesSendsAcrossWorkers(t *testing.T) {
	e := setup(t, 0)
	ctx := context.Background()
	const rate, n = 100, 300

	var (
		mu    sync.Mutex
		slots []time.Time
		wg    sync.WaitGroup
	)
	start := time.Now()
	for range 10 {
		l := delivery.NewLimiter(e.rdb, rate)
		wg.Go(func() {
			for range n / 10 {
				d, err := l.Reserve(ctx, "sms")
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				slots = append(slots, time.Now().Add(d))
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	slices.SortFunc(slots, func(a, b time.Time) int { return a.Compare(b) })

	// Allow a little slack for the time between Redis computing the slot
	// and this goroutine reading the clock.
	const slack = 15 * time.Millisecond
	for i := 0; i+rate < len(slots); i++ {
		if gap := slots[i+rate].Sub(slots[i]); gap < time.Second-slack {
			t.Fatalf("slots %d and %d are %v apart: more than %d sends in one second", i, i+rate, gap, rate)
		}
	}
	if span := slots[len(slots)-1].Sub(start); span < 2900*time.Millisecond {
		t.Fatalf("%d slots span only %v", n, span)
	}

	// Channels are limited independently.
	d, err := delivery.NewLimiter(e.rdb, rate).Reserve(ctx, "email")
	if err != nil || d > 0 {
		t.Fatalf("email was throttled by sms traffic: %v %v", d, err)
	}
}
