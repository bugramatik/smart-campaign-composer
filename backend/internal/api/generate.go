package api

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"scc/internal/campaign"
	"scc/internal/composer"
	"scc/internal/content"
	"scc/internal/httpx"
	"scc/internal/segment"
	"scc/internal/validation"
)

type generateRequest struct {
	// Stages is "all" (segment, then content) or "content" (keep the segment,
	// rewrite the messages). Empty means "all".
	Stages string `json:"stages"`
	// Feedback is the marketer's instruction for a regeneration ("daha kısa").
	Feedback string `json:"feedback"`
}

// generate runs Claude for a draft campaign and streams progress as SSE:
//
//	status        {stage, state, attempt}       a stage started / restarted
//	validation    {stage, attempt, issues, will_retry}
//	content.partial {attempt, content}          tokens so far, best-effort parsed
//	segment       {campaign, proposal}          rule validated, audience counted, saved
//	content       {campaign, issues}            messages saved (issues: still invalid after repairs)
//	error         {code, message, details}
//	done          {campaign}
//
// Validation is done before the stream starts so plain HTTP errors cover bad
// requests; once streaming, failures arrive as an error event.
func (a *API) generate(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r)
	if err != nil {
		return err
	}
	var req generateRequest
	if r.ContentLength != 0 {
		if err := httpx.Decode(w, r, &req); err != nil {
			return err
		}
	}
	switch req.Stages {
	case "":
		req.Stages = "all"
	case "all", "content":
	default:
		return &validation.Error{Issues: []validation.Issue{{Path: "stages", Message: `must be "all" or "content"`}}}
	}
	if len([]rune(req.Feedback)) > 1000 {
		return &validation.Error{Issues: []validation.Issue{{Path: "feedback", Message: "must be at most 1000 characters"}}}
	}
	c, err := a.Campaigns.Store.Get(r.Context(), id)
	if err != nil {
		return err
	}
	if c.Status != campaign.StatusDraft {
		return httpx.Errorf(http.StatusConflict, "invalid_state", "only draft campaigns can be generated (status is %s)", c.Status)
	}
	if req.Stages == "content" && c.Segment == nil {
		return httpx.Errorf(http.StatusConflict, "invalid_state", "generate a segment before regenerating content")
	}

	ctx, cancel := context.WithTimeout(r.Context(), a.LLMTimeout)
	defer cancel()

	sse := httpx.NewSSE(w)
	var mu sync.Mutex // emit may be called from the LLM stream callback
	send := func(event string, data any) {
		mu.Lock()
		defer mu.Unlock()
		_ = sse.Send(event, data)
	}
	emit := func(e composer.Event) { send(e.Type, e.Data) }
	fail := func(err error) {
		se := toStatusError(err)
		if se.Status >= 500 {
			a.Log.ErrorContext(r.Context(), "generation failed", "campaign_id", id, "err", err)
		}
		send("error", httpx.ErrorDetail{Code: se.Code, Message: se.Message, Details: se.Details})
	}

	if req.Stages == "all" {
		c, err = a.generateSegment(ctx, c, emit, send)
		if err != nil {
			fail(err)
			return nil
		}
	}
	c, err = a.generateContent(ctx, c, req.Feedback, emit, send)
	if err != nil {
		fail(err)
		return nil
	}
	send("done", map[string]any{"campaign": c})
	return nil
}

func (a *API) generateSegment(ctx context.Context, c campaign.Campaign, emit composer.Emit, send func(string, any)) (campaign.Campaign, error) {
	cat, err := a.Campaigns.Segments.Catalog(ctx)
	if err != nil {
		return c, err
	}
	prop, err := a.Composer.Segment(ctx, c.ID, c.Brief, cat, emit)
	if err != nil {
		var invalid *composer.InvalidOutputError[segment.Proposal]
		if errors.As(err, &invalid) {
			// A rule that failed validation is never stored or executed.
			return c, &httpx.StatusError{
				Status: http.StatusUnprocessableEntity, Code: "segment_invalid",
				Message: "Claude could not produce a valid segment for this brief; rephrase it or define the rule by hand",
				Details: invalid.Issues,
			}
		}
		return c, err
	}
	emit(composer.Event{Type: "status", Data: map[string]any{"stage": "audience", "state": "counting"}})
	aud, err := a.Campaigns.Segments.Audience(ctx, cat, prop.Rule)
	if err != nil {
		return c, err
	}
	notes := &campaign.Notes{
		Summary: prop.Summary, Assumptions: prop.Assumptions,
		Unsupported: prop.Unsupported, SuggestedChannels: prop.Channels,
	}
	c, err = a.Campaigns.Store.SaveSegment(context.WithoutCancel(ctx), c.ID, prop.Rule, notes, aud)
	if err != nil {
		return c, err
	}
	send("segment", map[string]any{"campaign": c, "proposal": prop})
	return c, nil
}

func (a *API) generateContent(ctx context.Context, c campaign.Campaign, feedback string, emit composer.Emit, send func(string, any)) (campaign.Campaign, error) {
	spec := a.Campaigns.ContentSpec(c)
	in := composer.ContentInput{
		Brief: c.Brief, Languages: spec.Languages, MaxLengths: spec.MaxLengths, Feedback: feedback,
	}
	if c.Notes != nil {
		in.Summary = c.Notes.Summary
	}
	if feedback != "" && c.Content != nil {
		in.Previous = c.Content
	}

	ct, err := a.Composer.Content(ctx, c.ID, in, emit)
	issues := []validation.Issue{}
	if err != nil {
		var invalid *composer.InvalidOutputError[content.Content]
		if !errors.As(err, &invalid) || len(invalid.Value.Variants) == 0 {
			return c, err
		}
		// Keep the best attempt as a draft: the marketer fixes the flagged
		// fields in the editor, and approval re-validates before sending.
		ct, issues = invalid.Value, invalid.Issues
	}
	c, err = a.Campaigns.Store.SaveContent(context.WithoutCancel(ctx), c.ID, ct)
	if err != nil {
		return c, err
	}
	send("content", map[string]any{"campaign": c, "issues": issues, "sms": smsInfo(ct)})
	return c, nil
}
