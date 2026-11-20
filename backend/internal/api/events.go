package api

import (
	"encoding/json"
	"net/http"
	"time"

	"scc/internal/campaign"
	"scc/internal/httpx"
)

// campaignEvents streams a snapshot of the campaign (status, per-channel
// delivery counts, queue depth, provider stats) once a second. Postgres is the
// source of truth, so the stream is a poll of it rather than a fan-out of
// worker events: any number of API replicas can serve it, and a reconnecting
// client never misses state. Finished campaigns are polled less often.
func (a *API) campaignEvents(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	// Fail with a normal 404 before switching to SSE.
	if _, err := a.Campaigns.Store.Get(r.Context(), id); err != nil {
		return err
	}
	sse := httpx.NewSSE(w)
	ctx := r.Context()
	for {
		interval := time.Second
		snap, err := a.snapshot(r, id)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			a.Log.WarnContext(ctx, "snapshot failed", "campaign_id", id, "err", err)
			_ = sse.Send("error", map[string]string{"code": "snapshot_failed", "message": "could not load campaign state"})
		} else {
			payload := struct {
				Snapshot
				Provider json.RawMessage `json:"provider,omitempty"`
				At       time.Time       `json:"at"`
			}{snap, a.fetchProviderStats(ctx), time.Now()}
			if err := sse.Send("snapshot", payload); err != nil {
				return nil // client went away
			}
			if s := snap.Campaign.Status; s == campaign.StatusCompleted || s == campaign.StatusCancelled {
				interval = 5 * time.Second
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}
