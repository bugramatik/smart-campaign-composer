package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func quietMock() MockConfig {
	return MockConfig{RateLimitPerSecond: 1000, HangFor: time.Second, Seed: 7}
}

func TestMockReplaysIdempotentRequests(t *testing.T) {
	m := NewMock(quietMock())
	srv := httptest.NewServer(m.Handler())
	defer srv.Close()
	c := NewClient(srv.URL, time.Second)
	msg := Message{Reference: "c1:u1:sms", To: "+905550000001", Text: "hi"}

	first, err := c.Send(context.Background(), "sms", "scc-delivery-1", msg)
	if err != nil || first.Replayed {
		t.Fatalf("first send: %+v %v", first, err)
	}
	again, err := c.Send(context.Background(), "sms", "scc-delivery-1", msg)
	if err != nil || !again.Replayed || again.MessageID != first.MessageID {
		t.Fatalf("retry: %+v %v, want replay of %s", again, err, first.MessageID)
	}
	st := m.Stats()
	if st.Channels["sms"].Accepted != 1 || st.Channels["sms"].Replayed != 1 || st.DuplicateDeliveries != 0 {
		t.Fatalf("stats = %+v", st.Channels["sms"])
	}

	// Same logical delivery under a new key is what a buggy client does.
	if _, err := c.Send(context.Background(), "sms", "other-key", msg); err != nil {
		t.Fatal(err)
	}
	if m.Stats().DuplicateDeliveries != 1 {
		t.Fatal("duplicate delivery not detected")
	}
}

func TestMockEnforcesRateLimit(t *testing.T) {
	cfg := quietMock()
	cfg.RateLimitPerSecond = 3
	srv := httptest.NewServer(NewMock(cfg).Handler())
	defer srv.Close()
	c := NewClient(srv.URL, time.Second)

	var limited int
	for i := range 10 {
		_, err := c.Send(context.Background(), "email", "k"+string(rune('a'+i)), Message{Reference: string(rune('a' + i)), To: "x@example.com", Subject: "s"})
		var pe *Error
		if errors.As(err, &pe) && pe.Status == http.StatusTooManyRequests {
			if !pe.Retryable || pe.RetryAfter <= 0 {
				t.Fatalf("429 must be retryable with Retry-After: %+v", pe)
			}
			limited++
		}
	}
	if limited < 4 { // at most 3 per second window, possibly spanning two windows
		t.Fatalf("only %d of 10 requests were rate limited", limited)
	}
}

func TestClientClassifiesErrors(t *testing.T) {
	cases := []struct {
		status    int
		retryable bool
	}{
		{http.StatusBadRequest, false},
		{http.StatusUnprocessableEntity, false},
		{http.StatusRequestTimeout, true},
		{http.StatusTooManyRequests, true},
		{http.StatusInternalServerError, true},
		{http.StatusBadGateway, true},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "1.5")
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"error":"nope"}`))
		}))
		_, err := NewClient(srv.URL, time.Second).Send(context.Background(), "sms", "k", Message{})
		srv.Close()
		var pe *Error
		if !errors.As(err, &pe) || pe.Status != tc.status || pe.Retryable != tc.retryable || pe.Msg != "nope" {
			t.Errorf("status %d: err = %+v, want retryable=%v", tc.status, err, tc.retryable)
		}
		if tc.status == http.StatusTooManyRequests && pe.RetryAfter != 1500*time.Millisecond {
			t.Errorf("Retry-After parsed as %v", pe.RetryAfter)
		}
	}
}

func TestClientTimeoutIsRetryable(t *testing.T) {
	cfg := quietMock()
	cfg.InjectTimeout = 1
	m := NewMock(cfg)
	srv := httptest.NewServer(m.Handler())
	defer srv.Close()

	sms := Message{Reference: "r1", To: "+905550000001", Text: "hi"}
	_, err := NewClient(srv.URL, 50*time.Millisecond).Send(context.Background(), "sms", "k1", sms)
	var pe *Error
	if !errors.As(err, &pe) || !pe.Retryable || pe.Status != 0 {
		t.Fatalf("err = %v, want retryable transport error", err)
	}
	// The provider did accept it; the retry with the same key is a replay,
	// not a second delivery.
	res, err := NewClient(srv.URL, time.Second).Send(context.Background(), "sms", "k1", sms)
	if err != nil || !res.Replayed || m.Stats().DuplicateDeliveries != 0 {
		t.Fatalf("retry after timeout: %+v %v dupes=%d", res, err, m.Stats().DuplicateDeliveries)
	}
}
