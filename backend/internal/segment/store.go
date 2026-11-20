package segment

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Audience is what a rule selects right now.
type Audience struct {
	Total         int            `json:"total"`
	SMSEligible   int            `json:"sms_eligible"`
	EmailEligible int            `json:"email_eligible"`
	ByLanguage    map[string]int `json:"by_language"`
	// MaxLengths holds the longest value of each personalization field in the
	// audience, so content can be length-checked against the worst case.
	MaxLengths map[string]int `json:"max_lengths"`
	Sample     []SampleUser   `json:"sample"`
}

// SampleUser is a preview row. Contact details are left out on purpose.
type SampleUser struct {
	ID          int64  `json:"id"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	City        string `json:"city"`
	Language    string `json:"language"`
	SMSOptIn    bool   `json:"sms_opt_in"`
	EmailOptIn  bool   `json:"email_opt_in"`
	TotalOrders int    `json:"total_orders"`
}

// Languages returns the languages present in the audience, in a stable order.
func (a Audience) Languages() []string {
	var out []string
	for _, l := range []string{"tr", "en"} {
		if a.ByLanguage[l] > 0 {
			out = append(out, l)
		}
	}
	return out
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Catalog loads the dynamic parts of the catalog (the city list) from the DB.
func (s *Store) Catalog(ctx context.Context) (Catalog, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT city FROM users ORDER BY city`)
	if err != nil {
		return Catalog{}, fmt.Errorf("load cities: %w", err)
	}
	cities, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return Catalog{}, fmt.Errorf("load cities: %w", err)
	}
	return NewCatalog(cities), nil
}

// Audience counts and samples the users matching r. The query runs in a
// read-only transaction with a statement timeout so a pathological rule
// cannot hold the database.
func (s *Store) Audience(ctx context.Context, cat Catalog, r Rule) (Audience, error) {
	q, err := cat.Compile(r, 0)
	if err != nil {
		return Audience{}, err
	}
	var a Audience
	err = pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = '10s'`); err != nil {
			return err
		}
		var tr, en, fn, ln, city int
		err := tx.QueryRow(ctx, `
			SELECT count(*),
			       count(*) FILTER (WHERE u.sms_opt_in),
			       count(*) FILTER (WHERE u.email_opt_in),
			       count(*) FILTER (WHERE u.language = 'tr'),
			       count(*) FILTER (WHERE u.language = 'en'),
			       coalesce(max(char_length(u.first_name)), 0),
			       coalesce(max(char_length(u.last_name)), 0),
			       coalesce(max(char_length(u.city)), 0)
			FROM users u WHERE `+q.Where, q.Args...).
			Scan(&a.Total, &a.SMSEligible, &a.EmailEligible, &tr, &en, &fn, &ln, &city)
		if err != nil {
			return fmt.Errorf("count audience: %w", err)
		}
		a.ByLanguage = map[string]int{"tr": tr, "en": en}
		a.MaxLengths = map[string]int{"first_name": fn, "last_name": ln, "city": city}

		rows, err := tx.Query(ctx, `
			SELECT u.id, u.first_name, u.last_name, u.city, u.language, u.sms_opt_in, u.email_opt_in, u.total_orders
			FROM users u WHERE `+q.Where+` ORDER BY u.id LIMIT 8`, q.Args...)
		if err != nil {
			return fmt.Errorf("sample audience: %w", err)
		}
		a.Sample, err = pgx.CollectRows(rows, pgx.RowToStructByPos[SampleUser])
		return err
	})
	if a.Sample == nil {
		a.Sample = []SampleUser{}
	}
	return a, err
}
