package provider

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// MockConfig controls fault injection. Rates are probabilities per request.
type MockConfig struct {
	RateLimitPerSecond int     // real per-channel limit; above it: 429
	Inject429          float64 // spurious 429 even under the limit
	Inject500          float64
	InjectTimeout      float64 // accept the message, then never answer in time
	MinLatency         time.Duration
	MaxLatency         time.Duration
	HangFor            time.Duration // how long a "timeout" request hangs
	Seed               uint64
}

func DefaultMockConfig() MockConfig {
	return MockConfig{
		RateLimitPerSecond: 120, Inject429: 0.03, Inject500: 0.03, InjectTimeout: 0.01,
		MinLatency: 20 * time.Millisecond, MaxLatency: 250 * time.Millisecond, HangFor: 10 * time.Second, Seed: 1,
	}
}

// Mock is an in-memory messaging provider. It honours Idempotency-Key: a
// repeated key gets the original response (and Idempotent-Replayed: true)
// without a second delivery. It counts accepted messages per Reference;
// duplicate_deliveries in /stats must stay 0 for a correct client.
type Mock struct {
	cfg MockConfig

	mu       sync.Mutex
	rnd      *rand.Rand
	byKey    map[string]Result
	perRef   map[string]int
	windows  map[string]*window
	recent   []Stored
	counters map[string]*ChannelStats
	dupes    int
	started  time.Time
}

type window struct {
	second int64
	count  int
}

// Stored is an accepted message, as shown by GET /v1/messages.
type Stored struct {
	ID         string    `json:"id"`
	Channel    string    `json:"channel"`
	Message    Message   `json:"message"`
	AcceptedAt time.Time `json:"accepted_at"`
}

type ChannelStats struct {
	Accepted    int `json:"accepted"`
	Replayed    int `json:"replayed"`
	RateLimited int `json:"rate_limited"`
	Injected429 int `json:"injected_429"`
	ServerError int `json:"server_errors"`
	Timeouts    int `json:"timeouts"`
	BadRequest  int `json:"bad_requests"`
}

type Stats struct {
	Channels            map[string]*ChannelStats `json:"channels"`
	DuplicateDeliveries int                      `json:"duplicate_deliveries"`
	UptimeSeconds       int                      `json:"uptime_seconds"`
}

func NewMock(cfg MockConfig) *Mock {
	return &Mock{
		cfg: cfg, rnd: rand.New(rand.NewPCG(cfg.Seed, cfg.Seed^0x9e3779b97f4a7c15)),
		byKey: map[string]Result{}, perRef: map[string]int{}, windows: map[string]*window{},
		counters: map[string]*ChannelStats{"sms": {}, "email": {}}, started: time.Now(),
	}
}

func (m *Mock) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/{channel}", m.send)
	mux.HandleFunc("GET /v1/messages", m.messages)
	mux.HandleFunc("GET /stats", m.stats)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return mux
}

type outcome int

const (
	accept outcome = iota
	replay
	limited
	injected429
	serverError
	acceptThenHang
)

func (m *Mock) send(w http.ResponseWriter, r *http.Request) {
	channel := r.PathValue("channel")
	key := r.Header.Get("Idempotency-Key")
	var msg Message
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&msg); err != nil || key == "" || msg.To == "" || msg.Reference == "" ||
		(channel != "sms" && channel != "email") || (channel == "sms" && msg.Text == "") || (channel == "email" && msg.Subject == "") {
		m.count(channel, func(s *ChannelStats) { s.BadRequest++ })
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	res, out, latency := m.decide(channel, key, msg)
	select {
	case <-time.After(latency):
	case <-r.Context().Done():
		return
	}
	switch out {
	case accept:
		writeJSON(w, http.StatusAccepted, res)
	case replay:
		w.Header().Set("Idempotent-Replayed", "true")
		writeJSON(w, http.StatusAccepted, res)
	case limited, injected429:
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
	case serverError:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "upstream carrier error"})
	case acceptThenHang:
		select {
		case <-time.After(m.cfg.HangFor):
			writeJSON(w, http.StatusAccepted, res)
		case <-r.Context().Done():
		}
	}
}

// decide picks the outcome under the lock; the latency is served outside it.
func (m *Mock) decide(channel, key string, msg Message) (Result, outcome, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.counters[channel]
	latency := m.cfg.MinLatency
	if span := m.cfg.MaxLatency - m.cfg.MinLatency; span > 0 {
		latency += time.Duration(m.rnd.Int64N(int64(span)))
	}

	if res, ok := m.byKey[key]; ok {
		st.Replayed++
		return res, replay, latency
	}
	now := time.Now().Unix()
	win := m.windows[channel]
	if win == nil || win.second != now {
		win = &window{second: now}
		m.windows[channel] = win
	}
	if win.count >= m.cfg.RateLimitPerSecond {
		st.RateLimited++
		return Result{}, limited, latency / 4
	}
	win.count++

	p := m.rnd.Float64()
	switch {
	case p < m.cfg.Inject429:
		st.Injected429++
		return Result{}, injected429, latency / 4
	case p < m.cfg.Inject429+m.cfg.Inject500:
		st.ServerError++
		return Result{}, serverError, latency
	}

	res := Result{MessageID: fmt.Sprintf("msg_%s_%d", channel, len(m.byKey)+1)}
	m.byKey[key] = res
	m.perRef[msg.Reference]++
	if m.perRef[msg.Reference] > 1 {
		m.dupes++
	}
	st.Accepted++
	m.recent = append(m.recent, Stored{ID: res.MessageID, Channel: channel, Message: msg, AcceptedAt: time.Now()})
	if len(m.recent) > 500 {
		m.recent = m.recent[len(m.recent)-500:]
	}
	if p < m.cfg.Inject429+m.cfg.Inject500+m.cfg.InjectTimeout {
		st.Timeouts++
		return res, acceptThenHang, latency
	}
	return res, accept, latency
}

func (m *Mock) count(channel string, f func(*ChannelStats)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.counters[channel]; ok {
		f(st)
	}
}

func (m *Mock) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := Stats{Channels: map[string]*ChannelStats{}, DuplicateDeliveries: m.dupes, UptimeSeconds: int(time.Since(m.started).Seconds())}
	for ch, st := range m.counters {
		c := *st
		out.Channels[ch] = &c
	}
	return out
}

func (m *Mock) stats(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, m.Stats()) }

func (m *Mock) messages(w http.ResponseWriter, r *http.Request) {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 || limit > 500 {
		limit = 50
	}
	m.mu.Lock()
	n := min(limit, len(m.recent))
	out := make([]Stored, n)
	for i := range n {
		out[i] = m.recent[len(m.recent)-1-i] // newest first
	}
	m.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"messages": out})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
