package llm

import (
	"context"
	"sync"
)

// Scripted is a test double that replays canned responses in order and
// records every request it received.
type Scripted struct {
	mu        sync.Mutex
	responses []Reply
	calls     []Request
}

// Reply is one canned reply. If Err is set it is returned after Text has
// been streamed (so partial-output paths can be tested).
type Reply struct {
	Text       string
	StopReason string
	Err        error
}

func NewScripted(responses ...Reply) *Scripted {
	return &Scripted{responses: responses}
}

func (s *Scripted) Provider() string { return "scripted" }
func (s *Scripted) Model() string    { return "scripted" }

func (s *Scripted) Stream(ctx context.Context, req Request, h StreamHandler) (Response, error) {
	s.mu.Lock()
	s.calls = append(s.calls, req)
	if len(s.responses) == 0 {
		s.mu.Unlock()
		return Response{}, errScriptExhausted
	}
	r := s.responses[0]
	s.responses = s.responses[1:]
	s.mu.Unlock()

	for _, chunk := range chunks(r.Text, 7) {
		if err := ctx.Err(); err != nil {
			return Response{}, err
		}
		h.delta(chunk)
	}
	stop := r.StopReason
	if stop == "" {
		stop = "end_turn"
	}
	return Response{Text: r.Text, StopReason: stop, Model: "scripted", InputTokens: len(req.System) / 4, OutputTokens: len(r.Text) / 4}, r.Err
}

// Calls returns the requests received so far.
func (s *Scripted) Calls() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.calls...)
}

type scriptError string

func (e scriptError) Error() string { return string(e) }

const errScriptExhausted = scriptError("scripted llm: no responses left")

// chunks splits s into pieces of at most n runes.
func chunks(s string, n int) []string {
	var out []string
	r := []rune(s)
	for len(r) > 0 {
		k := min(n, len(r))
		out = append(out, string(r[:k]))
		r = r[k:]
	}
	return out
}
