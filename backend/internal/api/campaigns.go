package api

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"scc/internal/campaign"
	"scc/internal/content"
	"scc/internal/delivery"
	"scc/internal/httpx"
	"scc/internal/segment"
	"scc/internal/validation"
)

func (a *API) meta(w http.ResponseWriter, r *http.Request) error {
	cat, err := a.Campaigns.Segments.Catalog(r.Context())
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"llm":      map[string]string{"provider": a.LLM.Provider(), "model": a.LLM.Model()},
		"catalog":  cat,
		"channels": segment.Channels,
		"operators": map[string][]string{
			"text": segment.TextOperators, "number": segment.NumberOperators,
		},
		"variables": content.Variables,
		"limits": map[string]int{
			"sms": content.MaxSMS, "email_subject": content.MaxEmailSubject,
			"email_preheader": content.MaxPreheader, "email_body": content.MaxEmailBody,
		},
	})
	return nil
}

func (a *API) overview(w http.ResponseWriter, r *http.Request) error {
	ov, err := a.Deliveries.Overview(r.Context())
	if err != nil {
		return err
	}
	depth, err := a.Queue.Depth(r.Context(), segment.Channels)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"overview": ov, "queue": depth, "provider": a.fetchProviderStats(r.Context()),
	})
	return nil
}

func (a *API) listCampaigns(w http.ResponseWriter, r *http.Request) error {
	list, err := a.Campaigns.Store.List(r.Context(), queryInt(r, "limit", 50, 1, 200))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"campaigns": list})
	return nil
}

func (a *API) createCampaign(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Brief string `json:"brief"`
	}
	if err := httpx.Decode(w, r, &body); err != nil {
		return err
	}
	brief := strings.TrimSpace(body.Brief)
	if n := utf8.RuneCountInString(brief); n < 10 || n > 2000 {
		return &validation.Error{Issues: []validation.Issue{{Path: "brief", Message: "must be between 10 and 2000 characters"}}}
	}
	c, err := a.Campaigns.Store.Create(r.Context(), brief)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusCreated, c)
	return nil
}

func (a *API) getCampaign(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	c, err := a.Campaigns.Store.Get(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, c)
	return nil
}

func (a *API) previewSegment(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Rule segment.Rule `json:"rule"`
	}
	if err := httpx.Decode(w, r, &body); err != nil {
		return err
	}
	cat, err := a.Campaigns.Segments.Catalog(r.Context())
	if err != nil {
		return err
	}
	rule, err := cat.ValidateRule(body.Rule)
	if err != nil {
		return err
	}
	aud, err := a.Campaigns.Segments.Audience(r.Context(), cat, rule)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rule": rule, "audience": aud})
	return nil
}

func (a *API) updateSegment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var body struct {
		Rule segment.Rule `json:"rule"`
	}
	if err := httpx.Decode(w, r, &body); err != nil {
		return err
	}
	c, err := a.Campaigns.UpdateSegment(r.Context(), id, body.Rule)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, c)
	return nil
}

func (a *API) updateContent(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var body content.Content
	if err := httpx.Decode(w, r, &body); err != nil {
		return err
	}
	c, err := a.Campaigns.UpdateContent(r.Context(), id, body)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, c)
	return nil
}

// checkContent validates content without saving it, for live feedback in the
// editor, and reports SMS encoding and segment counts per variant.
func (a *API) checkContent(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var body content.Content
	if err := httpx.Decode(w, r, &body); err != nil {
		return err
	}
	c, err := a.Campaigns.Store.Get(r.Context(), id)
	if err != nil {
		return err
	}
	issues := []validation.Issue{}
	if err := a.Campaigns.Check(c, body); err != nil {
		ve, ok := err.(*validation.Error)
		if !ok {
			return err
		}
		issues = ve.Issues
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"valid": len(issues) == 0, "issues": issues, "sms": smsInfo(body)})
	return nil
}

func smsInfo(c content.Content) map[string]content.SMSInfo {
	out := map[string]content.SMSInfo{}
	for _, v := range c.Variants {
		out[v.Language] = content.AnalyzeSMS(v.SMS)
	}
	return out
}

func (a *API) approve(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var body campaign.ApproveInput
	if err := httpx.Decode(w, r, &body); err != nil {
		return err
	}
	c, err := a.Campaigns.Approve(r.Context(), id, body)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, c)
	return nil
}

func (a *API) cancel(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	c, err := a.Campaigns.Store.Cancel(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, c)
	return nil
}

func (a *API) campaignStats(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	snap, err := a.snapshot(r, id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, snap)
	return nil
}

func (a *API) listDeliveries(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	status := r.URL.Query().Get("status")
	if status != "" && !validDeliveryStatus(status) {
		return &validation.Error{Issues: []validation.Issue{{Path: "status", Message: "unknown delivery status"}}}
	}
	rows, err := a.Deliveries.List(r.Context(), id, status, queryInt(r, "limit", 50, 1, 500))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"deliveries": rows})
	return nil
}

func validDeliveryStatus(s string) bool {
	switch s {
	case "queued", "sending", "retrying", "sent", "failed", "skipped", "cancelled":
		return true
	}
	return false
}

func (a *API) llmCalls(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	calls, err := a.Campaigns.Store.LLMCalls(r.Context(), id)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"calls": calls})
	return nil
}

// Snapshot is the live campaign state the dashboard renders.
type Snapshot struct {
	Campaign campaign.Campaign         `json:"campaign"`
	Stats    delivery.Stats            `json:"stats"`
	Queue    map[string]delivery.Depth `json:"queue"`
}

func (a *API) snapshot(r *http.Request, id int64) (Snapshot, error) {
	c, err := a.Campaigns.Store.Get(r.Context(), id)
	if err != nil {
		return Snapshot{}, err
	}
	stats, err := a.Deliveries.Stats(r.Context(), id)
	if err != nil {
		return Snapshot{}, err
	}
	depth, err := a.Queue.Depth(r.Context(), segment.Channels)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Campaign: c, Stats: stats, Queue: depth}, nil
}
