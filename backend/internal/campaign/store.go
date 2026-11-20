// Package campaign owns campaign records and their lifecycle:
//
//	draft ──approve──▶ scheduled ──(due)──▶ sending ──(all deliveries final)──▶ completed
//	  │                    │                   │
//	  └────────────────────┴──── cancel ───────┴──▶ cancelled
//
// Every transition is a guarded UPDATE (WHERE status = ...), so concurrent
// requests and processes cannot move a campaign through an illegal edge.
package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"scc/internal/composer"
	"scc/internal/content"
	"scc/internal/segment"
)

const (
	StatusDraft     = "draft"
	StatusScheduled = "scheduled"
	StatusSending   = "sending"
	StatusCompleted = "completed"
	StatusCancelled = "cancelled"
)

var (
	ErrNotFound     = errors.New("campaign not found")
	ErrInvalidState = errors.New("campaign is not in a state that allows this")
)

// Notes is Claude's explanation of the segment, kept next to the rule.
type Notes struct {
	Summary           string   `json:"summary"`
	Assumptions       []string `json:"assumptions"`
	Unsupported       []string `json:"unsupported"`
	SuggestedChannels []string `json:"suggested_channels"`
}

type Campaign struct {
	ID          int64             `json:"id"`
	Brief       string            `json:"brief"`
	Status      string            `json:"status"`
	Segment     *segment.Rule     `json:"segment"`
	Notes       *Notes            `json:"segment_notes"`
	Audience    *segment.Audience `json:"audience"`
	Content     *content.Content  `json:"content"`
	Channels    []string          `json:"channels"`
	ScheduledAt *time.Time        `json:"scheduled_at"`
	ApprovedAt  *time.Time        `json:"approved_at"`
	StartedAt   *time.Time        `json:"started_at"`
	CompletedAt *time.Time        `json:"completed_at"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const columns = `id, brief, status, segment, segment_notes, audience, content, channels,
	scheduled_at, approved_at, started_at, completed_at, created_at, updated_at`

func scan(row pgx.Row) (Campaign, error) {
	var c Campaign
	err := row.Scan(&c.ID, &c.Brief, &c.Status, &c.Segment, &c.Notes, &c.Audience, &c.Content, &c.Channels,
		&c.ScheduledAt, &c.ApprovedAt, &c.StartedAt, &c.CompletedAt, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

func (s *Store) Create(ctx context.Context, brief string) (Campaign, error) {
	return scan(s.pool.QueryRow(ctx, `INSERT INTO campaigns (brief) VALUES ($1) RETURNING `+columns, brief))
}

func (s *Store) Get(ctx context.Context, id int64) (Campaign, error) {
	return scan(s.pool.QueryRow(ctx, `SELECT `+columns+` FROM campaigns WHERE id = $1`, id))
}

func (s *Store) List(ctx context.Context, limit int) ([]Campaign, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+columns+` FROM campaigns ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list campaigns: %w", err)
	}
	defer rows.Close()
	out := []Campaign{}
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan campaign: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveSegment stores the rule, Claude's notes and the audience snapshot.
// Changing the segment invalidates nothing else: content is re-validated
// against the new audience at approval time.
func (s *Store) SaveSegment(ctx context.Context, id int64, rule segment.Rule, notes *Notes, aud segment.Audience) (Campaign, error) {
	return s.updateDraft(ctx, id, `segment = $2, segment_notes = coalesce($3, segment_notes), audience = $4`, rule, notes, aud)
}

func (s *Store) SaveContent(ctx context.Context, id int64, ct content.Content) (Campaign, error) {
	return s.updateDraft(ctx, id, `content = $2`, ct)
}

func (s *Store) updateDraft(ctx context.Context, id int64, set string, args ...any) (Campaign, error) {
	c, err := scan(s.pool.QueryRow(ctx,
		`UPDATE campaigns SET `+set+`, updated_at = now() WHERE id = $1 AND status = 'draft' RETURNING `+columns,
		append([]any{id}, args...)...))
	if errors.Is(err, ErrNotFound) {
		return c, s.stateError(ctx, id)
	}
	return c, err
}

// Approve moves a draft to scheduled. A nil `at` means "now": the worker's
// scheduler starts it on its next tick, so immediate and scheduled sends
// share one code path.
func (s *Store) Approve(ctx context.Context, id int64, channels []string, at *time.Time) (Campaign, error) {
	c, err := scan(s.pool.QueryRow(ctx, `
		UPDATE campaigns
		SET status = 'scheduled', channels = $2, scheduled_at = coalesce($3, now()), approved_at = now(), updated_at = now()
		WHERE id = $1 AND status = 'draft' AND segment IS NOT NULL AND content IS NOT NULL
		RETURNING `+columns, id, channels, at))
	if errors.Is(err, ErrNotFound) {
		return c, s.stateError(ctx, id)
	}
	return c, err
}

// Cancel stops a scheduled or sending campaign. Deliveries that have not
// been handed to the provider are cancelled in the same transaction; one
// already in flight finishes, and its result is recorded.
func (s *Store) Cancel(ctx context.Context, id int64) (Campaign, error) {
	var c Campaign
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		c, err = scan(tx.QueryRow(ctx, `
			UPDATE campaigns SET status = 'cancelled', completed_at = now(), updated_at = now()
			WHERE id = $1 AND status IN ('scheduled', 'sending') RETURNING `+columns, id))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			UPDATE deliveries SET status = 'cancelled', updated_at = now()
			WHERE campaign_id = $1 AND status IN ('queued', 'retrying')`, id)
		return err
	})
	if errors.Is(err, ErrNotFound) {
		return c, s.stateError(ctx, id)
	}
	return c, err
}

// stateError distinguishes "no such campaign" from "wrong state" after a
// guarded update matched no row.
func (s *Store) stateError(ctx context.Context, id int64) error {
	var status string
	err := s.pool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1`, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w (status is %s)", ErrInvalidState, status)
}

// RecordLLMCall implements composer.Auditor.
func (s *Store) RecordLLMCall(ctx context.Context, c composer.Call) error {
	var campaignID *int64
	if c.CampaignID != 0 {
		campaignID = &c.CampaignID
	}
	var errs []byte
	if len(c.Errors) > 0 {
		errs, _ = json.Marshal(c.Errors)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO llm_calls (campaign_id, purpose, attempt, provider, model, prompt_version, outcome, errors,
		                       raw_output, stop_reason, input_tokens, output_tokens, latency_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		campaignID, c.Purpose, c.Attempt, c.Provider, c.Model, c.PromptVersion, c.Outcome, errs,
		c.RawOutput, c.StopReason, c.InputTokens, c.OutputTokens, c.Latency.Milliseconds())
	return err
}

// LLMCall is the read model of an audit row, shown in the UI's "AI log".
type LLMCall struct {
	ID            int64           `json:"id"`
	Purpose       string          `json:"purpose"`
	Attempt       int             `json:"attempt"`
	Provider      string          `json:"provider"`
	Model         string          `json:"model"`
	PromptVersion string          `json:"prompt_version"`
	Outcome       string          `json:"outcome"`
	Errors        json.RawMessage `json:"errors"`
	StopReason    *string         `json:"stop_reason"`
	InputTokens   *int            `json:"input_tokens"`
	OutputTokens  *int            `json:"output_tokens"`
	LatencyMS     int             `json:"latency_ms"`
	CreatedAt     time.Time       `json:"created_at"`
}

func (s *Store) LLMCalls(ctx context.Context, campaignID int64) ([]LLMCall, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, purpose, attempt, provider, model, prompt_version, outcome, errors, stop_reason,
		       input_tokens, output_tokens, latency_ms, created_at
		FROM llm_calls WHERE campaign_id = $1 ORDER BY id`, campaignID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[LLMCall])
}
