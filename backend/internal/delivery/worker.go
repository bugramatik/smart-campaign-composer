package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"scc/internal/content"
	"scc/internal/metrics"
	"scc/internal/provider"
	"scc/internal/segment"
)

// Sender is the provider as the worker sees it.
type Sender interface {
	Send(ctx context.Context, channel, idempotencyKey string, m provider.Message) (provider.Result, error)
}

type CatalogSource interface {
	Catalog(ctx context.Context) (segment.Catalog, error)
}

type Config struct {
	Consumer          string // unique per process, e.g. hostname-pid
	Channels          []string
	Concurrency       int // per channel
	MaxAttempts       int
	Backoff           Backoff
	VisibilityTimeout time.Duration
	SendTimeout       time.Duration
}

type Worker struct {
	cfg      Config
	store    *Store
	queue    *Queue
	limiter  *Limiter
	sender   Sender
	catalogs CatalogSource
	log      *slog.Logger
}

func NewWorker(cfg Config, store *Store, queue *Queue, limiter *Limiter, sender Sender, catalogs CatalogSource, log *slog.Logger) *Worker {
	return &Worker{cfg: cfg, store: store, queue: queue, limiter: limiter, sender: sender, catalogs: catalogs, log: log}
}

// Run starts the per-channel consumers and the background loops, and blocks
// until ctx is cancelled and in-flight sends have been recorded.
func (w *Worker) Run(ctx context.Context) error {
	if err := w.queue.EnsureGroups(ctx, w.cfg.Channels); err != nil {
		return err
	}
	var wg sync.WaitGroup
	for _, ch := range w.cfg.Channels {
		jobs := make(chan Entry, w.cfg.Concurrency)
		wg.Go(func() { w.feed(ctx, ch, jobs) })
		for range w.cfg.Concurrency {
			wg.Go(func() {
				for e := range jobs {
					w.handle(ctx, ch, e)
				}
			})
		}
	}
	wg.Go(func() { w.every(ctx, time.Second, "scheduler", w.schedule) })
	wg.Go(func() { w.every(ctx, 250*time.Millisecond, "retry promoter", w.promote) })
	wg.Go(func() { w.every(ctx, 5*time.Second, "reconciler", w.reconcile) })
	wg.Go(func() { w.every(ctx, 2*time.Second, "queue gauge", w.gauge) })
	wg.Wait()
	return nil
}

// feed reads new stream entries, and periodically reclaims entries a dead
// consumer left unacknowledged, into the channel's job queue.
func (w *Worker) feed(ctx context.Context, channel string, jobs chan<- Entry) {
	defer close(jobs)
	lastClaim := time.Time{}
	for ctx.Err() == nil {
		if time.Since(lastClaim) > w.cfg.VisibilityTimeout/4 {
			lastClaim = time.Now()
			claimed, err := w.queue.Claim(ctx, channel, w.cfg.Consumer, w.cfg.VisibilityTimeout)
			if err != nil && ctx.Err() == nil {
				w.log.Warn("reclaim stream entries", "channel", channel, "err", err)
			}
			if !push(ctx, jobs, claimed) {
				return
			}
		}
		entries, err := w.queue.Read(ctx, channel, w.cfg.Consumer, int64(w.cfg.Concurrency), 2*time.Second)
		if err != nil {
			if ctx.Err() == nil {
				w.log.Warn("read stream", "channel", channel, "err", err)
				sleep(ctx, time.Second)
			}
			continue
		}
		if !push(ctx, jobs, entries) {
			return
		}
	}
}

func push(ctx context.Context, jobs chan<- Entry, entries []Entry) bool {
	for _, e := range entries {
		select {
		case jobs <- e:
		case <-ctx.Done():
			return false
		}
	}
	return true
}

// handle processes one stream entry and acknowledges it once its outcome is
// durable in Postgres. On an infrastructure error the entry stays pending
// and is reclaimed after the visibility timeout.
func (w *Worker) handle(ctx context.Context, channel string, e Entry) {
	// Once a send starts it should finish and be recorded even during
	// shutdown; the provider call has its own timeout.
	work := context.WithoutCancel(ctx)
	if e.DeliveryID != 0 {
		if err := w.process(ctx, work, channel, e.DeliveryID); err != nil {
			if !errors.Is(err, context.Canceled) {
				w.log.Error("process delivery", "delivery_id", e.DeliveryID, "channel", channel, "err", err)
			}
			return
		}
	}
	if err := w.queue.Ack(work, channel, e.StreamID); err != nil {
		w.log.Warn("ack", "channel", channel, "err", err)
	}
}

func (w *Worker) process(ctx, work context.Context, channel string, id int64) error {
	if ctx.Err() != nil {
		return ctx.Err() // shutting down: leave the entry pending
	}
	job, ok, err := w.store.Claim(work, id)
	if err != nil || !ok {
		if err == nil {
			// Duplicate entry, already final, or campaign cancelled.
			metrics.Deliveries.WithLabelValues(channel, "not_claimable").Inc()
		}
		return err
	}
	// The slot is reserved after the claim so entries that turn out not to
	// be claimable (a cancelled campaign's backlog) cost no send capacity.
	// The wait is short: at most Concurrency slots are reserved ahead.
	waited, err := w.limiter.Wait(work, channel)
	if err != nil {
		return err
	}
	metrics.RateLimitWait.WithLabelValues(channel).Observe(waited.Seconds())
	log := w.log.With("delivery_id", job.ID, "campaign_id", job.CampaignID, "channel", channel, "attempt", job.Attempt)

	// Consent is re-checked at send time: it may have been withdrawn after
	// the campaign started.
	if (channel == "sms" && !job.SMSOptIn) || (channel == "email" && !job.EmailOptIn) {
		metrics.Deliveries.WithLabelValues(channel, "skipped").Inc()
		return ignoreFenced(w.store.MarkSkipped(work, job, "consent withdrawn before send"))
	}
	msg, err := render(job)
	if err != nil {
		metrics.Deliveries.WithLabelValues(channel, "failed").Inc()
		return ignoreFenced(w.store.MarkFailed(work, job, err.Error()))
	}

	sendCtx, cancel := context.WithTimeout(work, w.cfg.SendTimeout)
	start := time.Now()
	res, sendErr := w.sender.Send(sendCtx, channel, IdempotencyKey(job.ID), msg)
	cancel()
	metrics.ProviderLatency.WithLabelValues(channel, statusClass(sendErr)).Observe(time.Since(start).Seconds())

	if sendErr == nil {
		outcome := "sent"
		if res.Replayed {
			outcome = "sent_replayed" // provider had it already: a retry after a lost response
		}
		metrics.Deliveries.WithLabelValues(channel, outcome).Inc()
		return ignoreFenced(w.store.MarkSent(work, job, res.MessageID))
	}

	var perr *provider.Error
	retryable := !errors.As(sendErr, &perr) || perr.Retryable
	if !retryable || job.Attempt >= w.cfg.MaxAttempts {
		metrics.Deliveries.WithLabelValues(channel, "failed").Inc()
		log.Warn("delivery failed", "err", sendErr)
		return ignoreFenced(w.store.MarkFailed(work, job, fmt.Sprintf("attempt %d: %v", job.Attempt, sendErr)))
	}
	var retryAfter time.Duration
	if perr != nil {
		retryAfter = perr.RetryAfter
	}
	due := time.Now().Add(w.cfg.Backoff.Delay(job.Attempt, retryAfter))
	metrics.Deliveries.WithLabelValues(channel, "retry").Inc()
	if err := w.store.MarkRetry(work, job, due, sendErr.Error()); err != nil {
		return ignoreFenced(err)
	}
	// If this fails the reconciler re-enqueues the row once it is overdue.
	if err := w.queue.ScheduleRetry(work, channel, job.ID, due); err != nil {
		log.Warn("schedule retry", "err", err)
	}
	return nil
}

// IdempotencyKey is stable for a delivery across all of its attempts.
func IdempotencyKey(deliveryID int64) string {
	return "scc-delivery-" + strconv.FormatInt(deliveryID, 10)
}

func render(j Job) (provider.Message, error) {
	v, ok := j.Content.For(j.Language)
	if !ok {
		return provider.Message{}, errors.New("campaign has no content")
	}
	vars := map[string]string{"first_name": j.FirstName, "last_name": j.LastName, "city": j.City}
	ref := fmt.Sprintf("campaign:%d:user:%d:%s", j.CampaignID, j.UserID, j.Channel)
	switch j.Channel {
	case "sms":
		text := content.Render(v.SMS, vars)
		if n := len([]rune(text)); n > content.MaxSMS {
			return provider.Message{}, fmt.Errorf("rendered sms is %d characters (limit %d)", n, content.MaxSMS)
		}
		return provider.Message{Reference: ref, To: j.Phone, Text: text}, nil
	case "email":
		subject := content.Render(v.EmailSubject, vars)
		if n := len([]rune(subject)); n > content.MaxEmailSubject {
			return provider.Message{}, fmt.Errorf("rendered subject is %d characters (limit %d)", n, content.MaxEmailSubject)
		}
		return provider.Message{Reference: ref, To: j.Email, Subject: subject,
			Preheader: content.Render(v.EmailPreheader, vars), Body: content.Render(v.EmailBody, vars)}, nil
	}
	return provider.Message{}, fmt.Errorf("unknown channel %q", j.Channel)
}

func statusClass(err error) string {
	if err == nil {
		return "2xx"
	}
	var perr *provider.Error
	if errors.As(err, &perr) && perr.Status != 0 {
		return strconv.Itoa(perr.Status/100) + "xx"
	}
	return "transport"
}

func ignoreFenced(err error) error {
	if errors.Is(err, ErrFenced) {
		metrics.Deliveries.WithLabelValues("any", "fenced").Inc()
		return nil
	}
	return err
}

func (w *Worker) schedule(ctx context.Context) error {
	cat, err := w.catalogs.Catalog(ctx)
	if err != nil {
		return err
	}
	for ctx.Err() == nil {
		started, ok, err := w.store.StartDue(ctx, cat)
		if err != nil || !ok {
			return err
		}
		total := 0
		for ch, ids := range started.ByChannel {
			total += len(ids)
			// On failure the reconciler enqueues these rows later.
			if err := w.queue.Enqueue(ctx, ch, ids); err != nil {
				w.log.Error("enqueue deliveries", "campaign_id", started.CampaignID, "channel", ch, "err", err)
			}
		}
		w.log.Info("campaign started", "campaign_id", started.CampaignID, "deliveries", total)
	}
	return nil
}

func (w *Worker) promote(ctx context.Context) error {
	_, err := w.queue.PromoteDue(ctx, time.Now())
	return err
}

func (w *Worker) reconcile(ctx context.Context) error {
	stale, err := w.store.Reconcile(ctx, w.cfg.VisibilityTimeout, 2000)
	if err != nil {
		return err
	}
	if len(stale) > 0 {
		by := map[string][]int64{}
		for _, s := range stale {
			by[s.Channel] = append(by[s.Channel], s.ID)
		}
		for ch, ids := range by {
			if err := w.queue.Enqueue(ctx, ch, ids); err != nil {
				return err
			}
		}
		w.log.Warn("re-enqueued stale deliveries", "count", len(stale))
	}
	done, err := w.store.CompleteFinished(ctx)
	for _, id := range done {
		w.log.Info("campaign completed", "campaign_id", id)
	}
	return err
}

func (w *Worker) gauge(ctx context.Context) error {
	depth, err := w.queue.Depth(ctx, w.cfg.Channels)
	if err != nil {
		return err
	}
	for ch, d := range depth {
		metrics.QueueDepth.WithLabelValues(ch, "pending").Set(float64(d.Pending))
		metrics.QueueDepth.WithLabelValues(ch, "retry").Set(float64(d.Retry))
	}
	return nil
}

func (w *Worker) every(ctx context.Context, d time.Duration, name string, fn func(context.Context) error) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		if err := fn(ctx); err != nil && ctx.Err() == nil {
			w.log.Warn(name, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
