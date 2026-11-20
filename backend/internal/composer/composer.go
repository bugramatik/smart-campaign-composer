// Package composer turns a brief into a validated segment proposal and
// validated message content using the LLM, with a validate-and-repair loop:
// the model's output is never trusted until the backend has checked it, and
// rejected output is sent back with the exact issues for another attempt.
package composer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"scc/internal/content"
	"scc/internal/llm"
	"scc/internal/metrics"
	"scc/internal/segment"
	"scc/internal/validation"
)

// Event is a progress notification for the UI (streamed as SSE).
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

type Emit func(Event)

// Call is the audit record of one LLM attempt.
type Call struct {
	CampaignID    int64
	Purpose       string
	Attempt       int
	Provider      string
	Model         string
	PromptVersion string
	Outcome       string // ok | invalid | error
	Errors        []validation.Issue
	RawOutput     string
	StopReason    string
	InputTokens   int
	OutputTokens  int
	Latency       time.Duration
}

type Auditor interface {
	RecordLLMCall(ctx context.Context, c Call) error
}

type Composer struct {
	llm         llm.Client
	audit       Auditor
	maxAttempts int
	log         *slog.Logger
	now         func() time.Time
}

func New(client llm.Client, audit Auditor, maxAttempts int, log *slog.Logger) *Composer {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	return &Composer{llm: client, audit: audit, maxAttempts: maxAttempts, log: log, now: time.Now}
}

// InvalidOutputError is returned when every attempt failed validation. Value
// holds the last (normalized as far as possible) output so the marketer can
// fix it by hand instead of starting over.
type InvalidOutputError[T any] struct {
	Value    T
	Issues   []validation.Issue
	Attempts int
}

func (e *InvalidOutputError[T]) Error() string {
	return fmt.Sprintf("output still invalid after %d attempts: %s", e.Attempts, (&validation.Error{Issues: e.Issues}).Error())
}

// Segment proposes an audience rule for brief.
func (c *Composer) Segment(ctx context.Context, campaignID int64, brief string, cat segment.Catalog, emit Emit) (segment.Proposal, error) {
	req := llm.Request{
		Purpose:   "segment",
		System:    segmentSystem,
		Messages:  []llm.Message{{Role: llm.RoleUser, Text: segmentUserPrompt(brief, cat, c.now())}},
		Schema:    cat.ProposalSchema(),
		MaxTokens: 16000,
	}
	return run(ctx, c, campaignID, SegmentPromptVersion, req, emit, nil, cat.Validate)
}

// Content writes message variants for the audience languages. Partial output
// is emitted as "content.partial" events while the model is still writing.
func (c *Composer) Content(ctx context.Context, campaignID int64, in ContentInput, emit Emit) (content.Content, error) {
	if len(in.Languages) == 0 {
		in.Languages = []string{"tr"}
	}
	spec := content.Spec{Languages: in.Languages, Channels: segment.Channels, MaxLengths: in.MaxLengths}
	req := llm.Request{
		Purpose:   "content",
		System:    contentSystem,
		Messages:  []llm.Message{{Role: llm.RoleUser, Text: contentUserPrompt(in)}},
		Schema:    content.Schema(in.Languages),
		MaxTokens: 16000,
	}
	validate := func(ct content.Content) (content.Content, error) {
		ct = normalizeContent(ct)
		return ct, content.Validate(ct, spec)
	}
	return run(ctx, c, campaignID, ContentPromptVersion, req, emit, partialContent(emit), validate)
}

// partialContent returns a stream observer that parses the growing JSON and
// emits snapshots, at most every 75ms.
func partialContent(emit Emit) func(attempt int) llm.StreamHandler {
	return func(attempt int) llm.StreamHandler {
		var (
			buf  strings.Builder
			last time.Time
		)
		return llm.StreamHandler{
			Delta: func(s string) {
				buf.WriteString(s)
				if time.Since(last) < 75*time.Millisecond {
					return
				}
				last = time.Now()
				if doc, ok := llm.CompleteJSON(buf.String()); ok {
					var ct content.Content
					if json.Unmarshal([]byte(doc), &ct) == nil {
						emit(Event{Type: "content.partial", Data: map[string]any{"attempt": attempt, "content": ct}})
					}
				}
			},
			Reset: func() {
				buf.Reset()
				emit(Event{Type: "status", Data: map[string]any{"stage": "content", "state": "restarted", "attempt": attempt}})
			},
		}
	}
}

func run[T any](
	ctx context.Context, c *Composer, campaignID int64, version string, req llm.Request,
	emit Emit, observer func(attempt int) llm.StreamHandler, validate func(T) (T, error),
) (T, error) {
	var (
		zero       T
		last       T
		lastIssues []validation.Issue
		haveLast   bool
	)
	base := req.Messages
	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		emit(Event{Type: "status", Data: map[string]any{"stage": req.Purpose, "state": "generating", "attempt": attempt}})
		h := llm.StreamHandler{}
		if observer != nil {
			h = observer(attempt)
		}

		start := time.Now()
		resp, err := c.llm.Stream(ctx, req, h)
		call := Call{
			CampaignID: campaignID, Purpose: req.Purpose, Attempt: attempt,
			Provider: c.llm.Provider(), Model: firstNonEmpty(resp.Model, c.llm.Model()), PromptVersion: version,
			RawOutput: resp.Text, StopReason: resp.StopReason, InputTokens: resp.InputTokens, OutputTokens: resp.OutputTokens,
			Latency: time.Since(start),
		}
		metrics.LLMLatency.WithLabelValues(req.Purpose).Observe(call.Latency.Seconds())
		metrics.LLMTokens.WithLabelValues(req.Purpose, "input").Add(float64(resp.InputTokens))
		metrics.LLMTokens.WithLabelValues(req.Purpose, "output").Add(float64(resp.OutputTokens))

		if errors.Is(err, llm.ErrTruncated) {
			// Incomplete JSON can't be repaired by conversation; start over.
			call.Outcome, call.Errors = "invalid", []validation.Issue{{Message: "output truncated at max_tokens"}}
			c.record(ctx, call, "invalid")
			lastIssues = call.Errors
			req.Messages = base
			continue
		}
		if err != nil {
			call.Outcome, call.Errors = "error", []validation.Issue{{Message: err.Error()}}
			outcome := "error"
			var refusal *llm.RefusalError
			if errors.As(err, &refusal) {
				outcome = "refusal"
			}
			c.record(ctx, call, outcome)
			return zero, err
		}

		var parsed T
		var issues []validation.Issue
		if err := json.Unmarshal([]byte(resp.Text), &parsed); err != nil {
			issues = []validation.Issue{{Message: "output is not valid JSON for the schema: " + err.Error()}}
		} else {
			normalized, verr := validate(parsed)
			if verr == nil {
				call.Outcome = "ok"
				c.record(ctx, call, "ok")
				return normalized, nil
			}
			var ve *validation.Error
			if !errors.As(verr, &ve) {
				return zero, verr
			}
			issues = ve.Issues
			last, haveLast = normalized, true
		}

		call.Outcome, call.Errors = "invalid", issues
		c.record(ctx, call, "invalid")
		lastIssues = issues
		emit(Event{Type: "validation", Data: map[string]any{"stage": req.Purpose, "attempt": attempt, "issues": issues, "will_retry": attempt < c.maxAttempts}})
		c.log.InfoContext(ctx, "llm output rejected", "purpose", req.Purpose, "attempt", attempt, "campaign_id", campaignID, "issues", len(issues))

		req.Messages = append(append([]llm.Message{}, base...),
			llm.Message{Role: llm.RoleAssistant, Text: resp.Text},
			llm.Message{Role: llm.RoleUser, Text: repairPrompt(issues)},
		)
	}
	if !haveLast {
		return zero, &InvalidOutputError[T]{Issues: lastIssues, Attempts: c.maxAttempts}
	}
	return last, &InvalidOutputError[T]{Value: last, Issues: lastIssues, Attempts: c.maxAttempts}
}

func (c *Composer) record(ctx context.Context, call Call, outcome string) {
	metrics.LLMRequests.WithLabelValues(call.Purpose, outcome).Inc()
	if c.audit == nil {
		return
	}
	// Audit failures must not fail generation; they are logged.
	if err := c.audit.RecordLLMCall(context.WithoutCancel(ctx), call); err != nil {
		c.log.WarnContext(ctx, "record llm call", "err", err)
	}
}

// normalizeContent trims whitespace the model sometimes leaves around fields.
func normalizeContent(ct content.Content) content.Content {
	out := content.Content{Variants: make([]content.Variant, len(ct.Variants))}
	for i, v := range ct.Variants {
		out.Variants[i] = content.Variant{
			Language:       strings.TrimSpace(v.Language),
			SMS:            strings.TrimSpace(v.SMS),
			EmailSubject:   strings.TrimSpace(v.EmailSubject),
			EmailPreheader: strings.TrimSpace(v.EmailPreheader),
			EmailBody:      strings.TrimSpace(v.EmailBody),
		}
	}
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
