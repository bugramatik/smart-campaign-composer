// Package provider is the messaging provider boundary: an HTTP client used by
// the worker, and an in-process mock provider (see mock.go) that injects
// latency, errors and rate limiting.
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Message is one outbound message. Reference identifies the logical
// delivery (campaign, user, channel) independently of the idempotency key;
// the mock uses it to detect duplicate deliveries.
type Message struct {
	Reference string `json:"reference"`
	To        string `json:"to"`
	Text      string `json:"text,omitempty"`      // sms
	Subject   string `json:"subject,omitempty"`   // email
	Preheader string `json:"preheader,omitempty"` // email
	Body      string `json:"body,omitempty"`      // email
}

type Result struct {
	MessageID string `json:"message_id"`
	Replayed  bool   `json:"-"`
}

// Error is a failed send. Retryable errors are worth another attempt
// (timeouts, 429, 5xx); others are permanent (4xx: the request itself is bad).
type Error struct {
	Status     int // 0 for transport errors
	RetryAfter time.Duration
	Retryable  bool
	Msg        string
}

func (e *Error) Error() string {
	if e.Status == 0 {
		return "provider: " + e.Msg
	}
	return fmt.Sprintf("provider: HTTP %d: %s", e.Status, e.Msg)
}

type Client struct {
	base string
	http *http.Client
}

// NewClient returns a client whose every request is bounded by timeout.
func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{base: baseURL, http: &http.Client{Timeout: timeout}}
}

// Send posts a message. idempotencyKey must be stable across retries of the
// same delivery: a provider that already accepted it replays the original
// response instead of sending again.
func (c *Client) Send(ctx context.Context, channel, idempotencyKey string, m Message) (Result, error) {
	body, err := json.Marshal(m)
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/"+channel, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idempotencyKey)

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Result{}, ctx.Err() // shutting down; not a provider failure
		}
		// Timeouts and connection errors: the provider may or may not have
		// accepted the message, so retrying relies on the idempotency key.
		return Result{}, &Error{Retryable: true, Msg: err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var r Result
		if err := json.Unmarshal(raw, &r); err != nil || r.MessageID == "" {
			return Result{}, &Error{Status: resp.StatusCode, Retryable: true, Msg: "malformed success response"}
		}
		r.Replayed = resp.Header.Get("Idempotent-Replayed") == "true"
		return r, nil
	}
	e := &Error{Status: resp.StatusCode, Msg: errorMessage(raw)}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		e.Retryable = true
		e.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"))
	case resp.StatusCode == http.StatusRequestTimeout, resp.StatusCode >= 500:
		e.Retryable = true
	}
	return Result{}, e
}

func errorMessage(raw []byte) string {
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &body) == nil && body.Error != "" {
		return body.Error
	}
	if len(raw) > 200 {
		raw = raw[:200]
	}
	return string(raw)
}

// parseRetryAfter accepts delta-seconds (fractional allowed) or an HTTP date.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if s, err := strconv.ParseFloat(v, 64); err == nil && s >= 0 {
		return time.Duration(s * float64(time.Second))
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(time.Until(t), 0)
	}
	return 0
}
