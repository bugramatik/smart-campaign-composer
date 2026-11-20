// Package llm is the boundary to the language model. The rest of the system
// sees one streaming call that returns JSON text conforming to a schema;
// whether it is Claude or a mock is a wiring decision.
package llm

import (
	"context"
	"errors"
	"fmt"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Message struct {
	Role Role
	Text string
}

type Request struct {
	Purpose   string // "segment" | "content", for logs and metrics
	System    string
	Messages  []Message
	Schema    map[string]any // structured-output JSON schema
	MaxTokens int
}

type Response struct {
	Text         string
	StopReason   string
	Model        string
	InputTokens  int
	OutputTokens int
}

// StreamHandler receives text as it is generated. Reset is called when the
// text so far is discarded (a server-side fallback restarted the answer).
type StreamHandler struct {
	Delta func(text string)
	Reset func()
}

type Client interface {
	Stream(ctx context.Context, req Request, h StreamHandler) (Response, error)
	Provider() string
	Model() string
}

var (
	// ErrTruncated means the output hit max_tokens and is incomplete.
	ErrTruncated = errors.New("llm output truncated (max_tokens)")
)

// RefusalError means the model declined the request; retrying the same input
// is pointless, so the composer surfaces it to the marketer instead.
type RefusalError struct {
	Category    string
	Explanation string
}

func (e *RefusalError) Error() string {
	if e.Explanation != "" {
		return fmt.Sprintf("model refused (%s): %s", e.Category, e.Explanation)
	}
	return "model refused the request"
}

func (h StreamHandler) delta(s string) {
	if h.Delta != nil && s != "" {
		h.Delta(s)
	}
}

func (h StreamHandler) reset() {
	if h.Reset != nil {
		h.Reset()
	}
}
