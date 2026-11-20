package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"scc/internal/content"
	"scc/internal/segment"
)

// Store holds the delivery state machine. Every transition is a guarded
// UPDATE whose WHERE clause states the edge it takes, so a stale worker, a
// duplicated queue entry or a cancelled campaign simply matches no row.
//
//	queued ─claim─▶ sending ─2xx─▶ sent
//	   ▲               │ └─permanent─▶ failed
//	   │               └─retryable─▶ retrying ─(due)─claim─▶ sending
//	cancel: queued|retrying ─▶ cancelled      consent revoked: ─▶ skipped
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Started is a campaign that moved to sending, with its new deliveries.
type Started struct {
	CampaignID int64
	ByChannel  map[string][]int64
}

// StartDue takes one due scheduled campaign (SKIP LOCKED, so several
// workers can run the scheduler) and, in the same transaction, marks it
// sending and materializes one delivery per (user, channel) with consent.
// The segment is evaluated now, not at approval, so users who joined or
// withdrew consent in between are handled correctly. ok is false when
// nothing was due.
func (s *Store) StartDue(ctx context.Context, cat segment.Catalog) (Started, bool, error) {
	var out Started
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var (
			id       int64
			rawRule  []byte
			channels []string
		)
		err := tx.QueryRow(ctx, `
			SELECT id, segment, channels FROM campaigns
			WHERE status = 'scheduled' AND scheduled_at <= now()
			ORDER BY scheduled_at LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id, &rawRule, &channels)
		if err != nil {
			return err
		}
		out = Started{CampaignID: id, ByChannel: map[string][]int64{}}

		var rule segment.Rule
		if err := json.Unmarshal(rawRule, &rule); err != nil {
			return fmt.Errorf("campaign %d segment: %w", id, err)
		}
		q, err := cat.Compile(rule, 2)
		if err != nil {
			return fmt.Errorf("campaign %d segment: %w", id, err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE campaigns SET status = 'sending', started_at = now(), updated_at = now() WHERE id = $1`, id); err != nil {
			return err
		}
		// ON CONFLICT makes this idempotent should the campaign ever be
		// started twice: UNIQUE(campaign_id, user_id, channel) holds.
		rows, err := tx.Query(ctx, `
			INSERT INTO deliveries (campaign_id, user_id, channel)
			SELECT $1, u.id, ch.channel
			FROM users u CROSS JOIN unnest($2::text[]) AS ch(channel)
			WHERE `+q.Where+`
			  AND CASE ch.channel WHEN 'sms' THEN u.sms_opt_in WHEN 'email' THEN u.email_opt_in ELSE false END
			ORDER BY u.id
			ON CONFLICT (campaign_id, user_id, channel) DO NOTHING
			RETURNING id, channel`, append([]any{id, channels}, q.Args...)...)
		if err != nil {
			return fmt.Errorf("materialize deliveries: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var did int64
			var ch string
			if err := rows.Scan(&did, &ch); err != nil {
				return err
			}
			out.ByChannel[ch] = append(out.ByChannel[ch], did)
		}
		return rows.Err()
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Started{}, false, nil
	}
	return out, err == nil, err
}

// Job is a claimed delivery with everything needed to send it.
type Job struct {
	ID         int64
	CampaignID int64
	UserID     int64
	Channel    string
	Attempt    int // fencing token: results are recorded only for this attempt
	FirstName  string
	LastName   string
	City       string
	Language   string
	Email      string
	Phone      string
	SMSOptIn   bool
	EmailOptIn bool
	Content    content.Content
}

// Claim moves a delivery to sending and returns it, or ok=false when it is
// not claimable: already final, claimed by someone else, not yet due, or its
// campaign is no longer sending (cancelled). The row lock plus the status
// predicate make this the single point that decides who sends.
func (s *Store) Claim(ctx context.Context, id int64) (Job, bool, error) {
	var j Job
	var rawContent []byte
	err := s.pool.QueryRow(ctx, `
		UPDATE deliveries d
		SET status = 'sending', attempts = d.attempts + 1, updated_at = now()
		FROM campaigns c, users u
		WHERE d.id = $1 AND c.id = d.campaign_id AND u.id = d.user_id
		  AND c.status = 'sending'
		  AND d.status IN ('queued', 'retrying')
		  AND (d.next_attempt_at IS NULL OR d.next_attempt_at <= now() + interval '1 second')
		RETURNING d.id, d.campaign_id, d.user_id, d.channel, d.attempts,
		          u.first_name, u.last_name, u.city, u.language, u.email, u.phone,
		          u.sms_opt_in, u.email_opt_in, c.content`, id).
		Scan(&j.ID, &j.CampaignID, &j.UserID, &j.Channel, &j.Attempt,
			&j.FirstName, &j.LastName, &j.City, &j.Language, &j.Email, &j.Phone,
			&j.SMSOptIn, &j.EmailOptIn, &rawContent)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	if err := json.Unmarshal(rawContent, &j.Content); err != nil {
		return Job{}, false, fmt.Errorf("delivery %d content: %w", id, err)
	}
	return j, true, nil
}

// ErrFenced means the delivery was taken over (reconciled) after this
// attempt claimed it; the newer attempt owns the outcome.
var ErrFenced = errors.New("delivery attempt is no longer current")

func (s *Store) finish(ctx context.Context, j Job, set string, args ...any) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE deliveries SET `+set+`, updated_at = now() WHERE id = $1 AND attempts = $2 AND status = 'sending'`,
		append([]any{j.ID, j.Attempt}, args...)...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrFenced
	}
	return nil
}

func (s *Store) MarkSent(ctx context.Context, j Job, providerID string) error {
	return s.finish(ctx, j, `status = 'sent', sent_at = now(), provider_message_id = $3, last_error = NULL, next_attempt_at = NULL`, providerID)
}

func (s *Store) MarkRetry(ctx context.Context, j Job, at time.Time, reason string) error {
	return s.finish(ctx, j, `status = 'retrying', next_attempt_at = $3, last_error = $4`, at, reason)
}

func (s *Store) MarkFailed(ctx context.Context, j Job, reason string) error {
	return s.finish(ctx, j, `status = 'failed', next_attempt_at = NULL, last_error = $3`, reason)
}

func (s *Store) MarkSkipped(ctx context.Context, j Job, reason string) error {
	return s.finish(ctx, j, `status = 'skipped', next_attempt_at = NULL, last_error = $3`, reason)
}

// Stale is a delivery the reconciler re-enqueues.
type Stale struct {
	ID      int64
	Channel string
}

// Reconcile finds work the queue lost track of and makes it claimable again:
//   - queued rows never enqueued (crash between commit and XADD) or whose
//     stream entry was lost;
//   - retrying rows whose retry-set entry was lost;
//   - sending rows whose worker died mid-send. These go back to retrying;
//     the attempt counter fences out the old worker, and the stable
//     idempotency key makes the resend safe if the provider had accepted.
func (s *Store) Reconcile(ctx context.Context, olderThan time.Duration, limit int) ([]Stale, error) {
	rows, err := s.pool.Query(ctx, `
		WITH stale AS (
			SELECT d.id FROM deliveries d
			JOIN campaigns c ON c.id = d.campaign_id AND c.status = 'sending'
			WHERE d.status IN ('queued', 'retrying', 'sending')
			  AND d.updated_at < now() - make_interval(secs => $1)
			  AND (d.status <> 'retrying' OR d.next_attempt_at < now() - make_interval(secs => $1))
			ORDER BY d.id LIMIT $2
			FOR UPDATE OF d SKIP LOCKED
		)
		UPDATE deliveries d
		SET status = CASE WHEN d.status = 'sending' THEN 'retrying' ELSE d.status END,
		    next_attempt_at = CASE WHEN d.status = 'queued' THEN NULL ELSE now() END,
		    last_error = CASE WHEN d.status = 'sending' THEN 'worker lost while sending; reconciled' ELSE d.last_error END,
		    updated_at = now()
		FROM stale WHERE d.id = stale.id
		RETURNING d.id, d.channel`, olderThan.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Stale])
}

// CompleteFinished marks sending campaigns completed once no delivery is
// left unfinished, and returns their ids.
func (s *Store) CompleteFinished(ctx context.Context) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE campaigns c SET status = 'completed', completed_at = now(), updated_at = now()
		WHERE c.status = 'sending'
		  AND NOT EXISTS (
		      SELECT 1 FROM deliveries d
		      WHERE d.campaign_id = c.id AND d.status IN ('queued', 'sending', 'retrying'))
		RETURNING c.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// Stats is the per-campaign, per-channel delivery breakdown.
type Stats struct {
	Channels map[string]map[string]int `json:"channels"` // channel → status → count
	Attempts int                       `json:"attempts"`
	Retries  int                       `json:"retries"`
}

func (s *Store) Stats(ctx context.Context, campaignID int64) (Stats, error) {
	out := Stats{Channels: map[string]map[string]int{}}
	rows, err := s.pool.Query(ctx, `
		SELECT channel, status, count(*), coalesce(sum(attempts), 0), coalesce(sum(greatest(attempts - 1, 0)), 0)
		FROM deliveries WHERE campaign_id = $1 GROUP BY channel, status`, campaignID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var ch, st string
		var n, attempts, retries int
		if err := rows.Scan(&ch, &st, &n, &attempts, &retries); err != nil {
			return out, err
		}
		if out.Channels[ch] == nil {
			out.Channels[ch] = map[string]int{}
		}
		out.Channels[ch][st] = n
		out.Attempts += attempts
		out.Retries += retries
	}
	return out, rows.Err()
}

// Row is one delivery as listed in the dashboard.
type Row struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"user_id"`
	FirstName  string     `json:"first_name"`
	LastName   string     `json:"last_name"`
	Channel    string     `json:"channel"`
	Status     string     `json:"status"`
	Attempts   int        `json:"attempts"`
	LastError  *string    `json:"last_error"`
	ProviderID *string    `json:"provider_message_id"`
	UpdatedAt  time.Time  `json:"updated_at"`
	SentAt     *time.Time `json:"sent_at"`
}

// List returns deliveries for a campaign, optionally filtered by status,
// most recently changed first.
func (s *Store) List(ctx context.Context, campaignID int64, status string, limit int) ([]Row, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT d.id, d.user_id, u.first_name, u.last_name, d.channel, d.status, d.attempts,
		       d.last_error, d.provider_message_id, d.updated_at, d.sent_at
		FROM deliveries d JOIN users u ON u.id = d.user_id
		WHERE d.campaign_id = $1 AND ($2 = '' OR d.status = $2)
		ORDER BY d.updated_at DESC, d.id DESC LIMIT $3`, campaignID, status, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Row])
}

// Overview is the system-wide delivery picture for the dashboard.
type Overview struct {
	Deliveries     map[string]map[string]int `json:"deliveries"` // channel → status → count
	Campaigns      map[string]int            `json:"campaigns"`  // status → count
	SentLastMinute map[string]int            `json:"sent_last_minute"`
}

func (s *Store) Overview(ctx context.Context) (Overview, error) {
	out := Overview{Deliveries: map[string]map[string]int{}, Campaigns: map[string]int{}, SentLastMinute: map[string]int{}}
	rows, err := s.pool.Query(ctx, `SELECT channel, status, count(*) FROM deliveries GROUP BY channel, status`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var ch, st string
		var n int
		if err := rows.Scan(&ch, &st, &n); err != nil {
			rows.Close()
			return out, err
		}
		if out.Deliveries[ch] == nil {
			out.Deliveries[ch] = map[string]int{}
		}
		out.Deliveries[ch][st] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	rows, err = s.pool.Query(ctx, `SELECT status, count(*) FROM campaigns GROUP BY status`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			rows.Close()
			return out, err
		}
		out.Campaigns[st] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	rows, err = s.pool.Query(ctx, `
		SELECT channel, count(*) FROM deliveries
		WHERE status = 'sent' AND sent_at > now() - interval '1 minute' GROUP BY channel`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var ch string
		var n int
		if err := rows.Scan(&ch, &n); err != nil {
			return out, err
		}
		out.SentLastMinute[ch] = n
	}
	return out, rows.Err()
}
