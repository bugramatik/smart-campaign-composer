// Package seed fills an empty database with deterministic fake users and
// behavioural events. Every address is @example.com and every phone number is
// in a fictional range, so nothing can reach a real person.
package seed

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"scc/internal/segment"
)

var (
	Cities = []string{"İstanbul", "Ankara", "İzmir", "Bursa", "Antalya", "Adana", "Konya", "Gaziantep", "Kayseri", "Eskişehir", "Trabzon", "Samsun"}
	// Rough population weights so İstanbul dominates as it would in real data.
	cityWeights = []int{30, 12, 10, 7, 6, 5, 5, 4, 4, 3, 3, 3}

	trFirst = []string{"Ayşe", "Fatma", "Zeynep", "Elif", "Emine", "Merve", "Büşra", "Selin", "Deniz", "Ece", "Mehmet", "Mustafa", "Ahmet", "Ali", "Hüseyin", "Emre", "Burak", "Can", "Oğuz", "Kerem", "Çağla", "Gökhan", "İrem", "Şule", "Ömer"}
	trLast  = []string{"Yılmaz", "Kaya", "Demir", "Şahin", "Çelik", "Yıldız", "Yıldırım", "Öztürk", "Aydın", "Özdemir", "Arslan", "Doğan", "Kılıç", "Aslan", "Çetin", "Koç", "Kurt", "Özkan", "Şimşek", "Polat"}
	enFirst = []string{"Emma", "Olivia", "Sophia", "Mia", "Grace", "Liam", "Noah", "James", "Lucas", "Henry", "Jack", "Ella"}
	enLast  = []string{"Smith", "Johnson", "Brown", "Taylor", "Wilson", "Clark", "Walker", "Hall", "Young", "King"}

	tiers       = []string{"bronze", "silver", "gold", "platinum"}
	tierWeights = []int{55, 28, 13, 4}
)

type Options struct {
	Users int
	Days  int // event history length
	Seed  uint64
	Now   time.Time
}

type Result struct {
	Users   int
	Events  int
	Skipped bool // the database already had users
}

// Run seeds the database if the users table is empty. It is safe to run on
// every start: a populated database is left untouched.
func Run(ctx context.Context, pool *pgxpool.Pool, o Options) (Result, error) {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users)`).Scan(&exists); err != nil {
		return Result{}, err
	}
	if exists {
		return Result{Skipped: true}, nil
	}
	rnd := rand.New(rand.NewPCG(o.Seed, o.Seed+1))
	users := Users(rnd, o.Users, o.Now)

	var res Result
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		// Lock out a concurrent seeder; the loser sees the rows after commit.
		if _, err := tx.Exec(ctx, `LOCK TABLE users IN EXCLUSIVE MODE`); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users)`).Scan(&exists); err != nil || exists {
			res.Skipped = exists
			return err
		}
		n, err := tx.CopyFrom(ctx, pgx.Identifier{"users"},
			[]string{"first_name", "last_name", "email", "phone", "city", "language", "birth_date", "loyalty_tier", "sms_opt_in", "email_opt_in", "created_at"},
			pgx.CopyFromSlice(len(users), func(i int) ([]any, error) {
				u := users[i]
				return []any{u.FirstName, u.LastName, u.Email, u.Phone, u.City, u.Language, u.BirthDate, u.Tier, u.SMSOptIn, u.EmailOptIn, u.CreatedAt}, nil
			}))
		if err != nil {
			return fmt.Errorf("copy users: %w", err)
		}
		res.Users = int(n)

		// Identity ids are assigned by the database; map them back by email,
		// which is unique and generated here.
		ids := make(map[string]int64, len(users))
		var (
			id    int64
			email string
		)
		rows, _ := tx.Query(ctx, `SELECT id, email FROM users`)
		if _, err := pgx.ForEachRow(rows, []any{&id, &email}, func() error {
			ids[email] = id
			return nil
		}); err != nil {
			return err
		}

		events := Events(rnd, users, o.Days, o.Now)
		n, err = tx.CopyFrom(ctx, pgx.Identifier{"events"},
			[]string{"user_id", "type", "category", "amount", "occurred_at"},
			pgx.CopyFromSlice(len(events), func(i int) ([]any, error) {
				e := events[i]
				return []any{ids[users[e.User].Email], e.Type, e.Category, e.Amount, e.At}, nil
			}))
		if err != nil {
			return fmt.Errorf("copy events: %w", err)
		}
		res.Events = int(n)

		// Denormalized order stats used by number conditions.
		_, err = tx.Exec(ctx, `
			UPDATE users u SET total_orders = s.n, total_spent = s.total
			FROM (SELECT user_id, count(*) AS n, coalesce(sum(amount), 0) AS total
			      FROM events WHERE type = 'purchase' GROUP BY user_id) s
			WHERE s.user_id = u.id`)
		return err
	})
	if err != nil {
		return Result{}, err
	}
	if !res.Skipped {
		_, err = pool.Exec(ctx, `ANALYZE users; ANALYZE events`)
	}
	return res, err
}

type User struct {
	FirstName, LastName, Email, Phone, City, Language, Tier string
	BirthDate, CreatedAt                                    time.Time
	SMSOptIn, EmailOptIn                                    bool
	activity                                                float64 // events per day
}

func Users(rnd *rand.Rand, n int, now time.Time) []User {
	out := make([]User, n)
	for i := range out {
		u := User{Language: "tr"}
		first, last := trFirst, trLast
		if rnd.IntN(100) < 15 {
			u.Language, first, last = "en", enFirst, enLast
		}
		u.FirstName = first[rnd.IntN(len(first))]
		u.LastName = last[rnd.IntN(len(last))]
		u.Email = fmt.Sprintf("%s.%s.%d@example.com", slug(u.FirstName), slug(u.LastName), i+1)
		// +90 555 0xx xx xx: unique, obviously fake.
		u.Phone = fmt.Sprintf("+90555%07d", i+1)
		u.City = Cities[weighted(rnd, cityWeights)]
		u.Tier = tiers[weighted(rnd, tierWeights)]
		u.BirthDate = now.AddDate(-(18 + rnd.IntN(50)), 0, -rnd.IntN(365)).Truncate(24 * time.Hour)
		u.CreatedAt = now.AddDate(0, 0, -rnd.IntN(720))
		u.SMSOptIn = rnd.IntN(100) < 70
		u.EmailOptIn = rnd.IntN(100) < 80
		// Long-tailed activity: most users are quiet, a few are very active.
		u.activity = 0.02 + rnd.ExpFloat64()*0.12
		out[i] = u
	}
	return out
}

type Event struct {
	User     int // index into the users slice
	Type     string
	Category string
	Amount   *float64
	At       time.Time
}

// Events builds a browse → view → cart → purchase funnel per session, so
// briefs like "sepete ekleyip satın almayanlar" match a realistic share.
func Events(rnd *rand.Rand, users []User, days int, now time.Time) []Event {
	var out []Event
	horizon := time.Duration(days) * 24 * time.Hour
	for ui, u := range users {
		sessions := int(u.activity*float64(days) + rnd.Float64())
		favourite := segment.Categories[rnd.IntN(len(segment.Categories))]
		for range sessions {
			at := now.Add(-time.Duration(rnd.Int64N(int64(horizon))))
			cat := favourite
			if rnd.IntN(100) < 40 {
				cat = segment.Categories[rnd.IntN(len(segment.Categories))]
			}
			add := func(typ string, amount *float64, offset time.Duration) {
				out = append(out, Event{User: ui, Type: typ, Category: cat, Amount: amount, At: at.Add(offset)})
			}
			add("page_view", nil, 0)
			if rnd.IntN(100) >= 60 {
				continue
			}
			add("product_view", nil, time.Minute)
			if rnd.IntN(100) >= 35 {
				continue
			}
			add("cart_add", nil, 3*time.Minute)
			if rnd.IntN(100) >= 45 {
				continue
			}
			amount := float64(50+rnd.IntN(4950)) + float64(rnd.IntN(100))/100
			add("purchase", &amount, 8*time.Minute)
		}
	}
	return out
}

func weighted(rnd *rand.Rand, weights []int) int {
	total := 0
	for _, w := range weights {
		total += w
	}
	n := rnd.IntN(total)
	for i, w := range weights {
		if n < w {
			return i
		}
		n -= w
	}
	return len(weights) - 1
}

var slugger = strings.NewReplacer("ç", "c", "ğ", "g", "ı", "i", "ö", "o", "ş", "s", "ü", "u", "Ç", "c", "Ğ", "g", "İ", "i", "Ö", "o", "Ş", "s", "Ü", "u")

func slug(s string) string { return strings.ToLower(slugger.Replace(s)) }
