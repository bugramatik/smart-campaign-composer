package composer

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"scc/internal/content"
	"scc/internal/llm"
	"scc/internal/llm/demo"
	"scc/internal/segment"
)

var cat = segment.NewCatalog([]string{"İstanbul", "Ankara", "İzmir"})

type recorder struct {
	mu    sync.Mutex
	calls []Call
}

func (r *recorder) RecordLLMCall(_ context.Context, c Call) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, c)
	return nil
}

func (r *recorder) outcomes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, c := range r.calls {
		out = append(out, c.Outcome)
	}
	return out
}

type events struct {
	mu  sync.Mutex
	all []Event
}

func (e *events) emit(ev Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.all = append(e.all, ev)
}

func (e *events) count(typ string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, ev := range e.all {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

func newComposer(client llm.Client, audit Auditor, attempts int) *Composer {
	return New(client, audit, attempts, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

const validSegment = `{"rule":{"match":"all","conditions":[{"kind":"event","event":"cart_add","occurred":true,"within_days":30},{"kind":"event","event":"purchase","occurred":false,"within_days":30}]},"channels":["sms","email"],"summary":"Son 30 günde sepete ekleyip satın almayanlar","assumptions":[],"unsupported":[]}`

func TestSegmentRepairsInvalidOutput(t *testing.T) {
	bad := strings.Replace(validSegment, `"cart_add"`, `"add_to_cart"`, 1)
	client := llm.NewScripted(llm.Reply{Text: bad}, llm.Reply{Text: validSegment})
	audit := &recorder{}
	ev := &events{}

	got, err := newComposer(client, audit, 3).Segment(context.Background(), 7, "sepete ekleyip almayanlar", cat, ev.emit)
	if err != nil {
		t.Fatalf("Segment: %v", err)
	}
	if got.Rule.Conditions[0].Event != "cart_add" {
		t.Fatalf("proposal = %+v", got)
	}
	if o := audit.outcomes(); len(o) != 2 || o[0] != "invalid" || o[1] != "ok" {
		t.Fatalf("audit outcomes = %v, want [invalid ok]", o)
	}
	if ev.count("validation") != 1 {
		t.Errorf("want one validation event, got %d", ev.count("validation"))
	}

	// The second request carries the rejected output and the exact issue.
	calls := client.Calls()
	if len(calls) != 2 {
		t.Fatalf("llm calls = %d", len(calls))
	}
	msgs := calls[1].Messages
	if len(msgs) != 3 || msgs[1].Role != llm.RoleAssistant || msgs[1].Text != bad {
		t.Fatalf("repair conversation = %+v", msgs)
	}
	if !strings.Contains(msgs[2].Text, "rule.conditions[0].event") {
		t.Errorf("repair prompt does not name the failing path:\n%s", msgs[2].Text)
	}
	if calls[1].Schema == nil {
		t.Error("repair request dropped the output schema")
	}
}

func TestSegmentGivesUpAfterMaxAttempts(t *testing.T) {
	client := llm.NewScripted(llm.Reply{Text: `{"rule":`}, llm.Reply{Text: `not json`}, llm.Reply{Text: `{}`})
	audit := &recorder{}
	_, err := newComposer(client, audit, 3).Segment(context.Background(), 1, "brief", cat, func(Event) {})

	var invalid *InvalidOutputError[segment.Proposal]
	if !errors.As(err, &invalid) || invalid.Attempts != 3 || len(invalid.Issues) == 0 {
		t.Fatalf("err = %v, want InvalidOutputError after 3 attempts", err)
	}
	if n := len(audit.outcomes()); n != 3 {
		t.Fatalf("audited %d calls, want 3", n)
	}
}

func TestTruncatedOutputRestartsFromScratch(t *testing.T) {
	client := llm.NewScripted(
		llm.Reply{Text: validSegment[:40], StopReason: "max_tokens", Err: llm.ErrTruncated},
		llm.Reply{Text: validSegment},
	)
	if _, err := newComposer(client, nil, 2).Segment(context.Background(), 1, "brief", cat, func(Event) {}); err != nil {
		t.Fatalf("Segment: %v", err)
	}
	if msgs := client.Calls()[1].Messages; len(msgs) != 1 {
		t.Fatalf("a truncated answer must not be fed back; got %d messages", len(msgs))
	}
}

func TestRefusalIsNotRetried(t *testing.T) {
	refusal := &llm.RefusalError{Category: "cyber", Explanation: "no"}
	client := llm.NewScripted(llm.Reply{StopReason: "refusal", Err: refusal}, llm.Reply{Text: validSegment})
	audit := &recorder{}
	_, err := newComposer(client, audit, 3).Segment(context.Background(), 1, "brief", cat, func(Event) {})
	if !errors.As(err, new(*llm.RefusalError)) {
		t.Fatalf("err = %v, want refusal", err)
	}
	if len(client.Calls()) != 1 {
		t.Fatalf("refusal was retried")
	}
	if o := audit.outcomes(); len(o) != 1 || o[0] != "error" {
		t.Fatalf("audit outcomes = %v", o)
	}
}

func TestContentStreamsPartialsAndKeepsBestAttempt(t *testing.T) {
	long := `{"variants":[{"language":"tr","sms":"` + strings.Repeat("Çok güzel bir indirim seni bekliyor! ", 6) + `","email_subject":"Sepetin seni bekliyor","email_preheader":"Bugüne özel","email_body":"Merhaba {{first_name}}"}]}`
	client := llm.NewScripted(llm.Reply{Text: long}, llm.Reply{Text: long})
	ev := &events{}
	in := ContentInput{Brief: "sepet hatırlatma", Languages: []string{"tr"}, MaxLengths: map[string]int{"first_name": 10}}

	_, err := newComposer(client, nil, 2).Content(context.Background(), 1, in, ev.emit)
	var invalid *InvalidOutputError[content.Content]
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want InvalidOutputError", err)
	}
	if len(invalid.Value.Variants) != 1 || invalid.Issues[0].Path != "variants[0].sms" {
		t.Fatalf("best attempt = %+v, issues = %+v", invalid.Value, invalid.Issues)
	}
}

func TestDemoClientOutputPassesValidation(t *testing.T) {
	client := &demo.Client{}
	briefs := []string{
		"Son 30 günde sepete elektronik ürün ekleyip satın almayan İstanbul'daki kullanıcılara %15 indirim",
		"Gold ve platinum müşterilere yeni sezon moda koleksiyonunu duyur, e-posta ile",
		"Win back customers who have not purchased in the last 90 days",
		"25 yaş altı Ankara ve İzmir'deki kullanıcılara spor ürünlerinde kampanya",
	}
	for _, b := range briefs {
		t.Run(b[:20], func(t *testing.T) {
			c := newComposer(client, nil, 1)
			p, err := c.Segment(context.Background(), 1, b, cat, func(Event) {})
			if err != nil {
				t.Fatalf("segment: %v", err)
			}
			in := ContentInput{Brief: b, Summary: p.Summary, Languages: []string{"tr", "en"}, MaxLengths: map[string]int{"first_name": 12, "last_name": 12, "city": 10}}
			if _, err := c.Content(context.Background(), 1, in, func(Event) {}); err != nil {
				t.Fatalf("content: %v", err)
			}
		})
	}
}
