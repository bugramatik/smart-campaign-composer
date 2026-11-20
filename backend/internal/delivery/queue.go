// Package delivery moves approved campaigns to the provider.
//
// Postgres is the source of truth for every delivery's state; Redis only
// carries work. A Redis entry holds nothing but a delivery id, so losing,
// duplicating or replaying entries can delay a message but never send it
// twice: the guarded claim in Postgres decides who sends, and the provider
// Idempotency-Key covers the window between "provider accepted" and "we
// recorded it".
package delivery

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	Group     = "workers"
	streamLen = 1_000_000 // approximate cap; entries are tiny
)

// Redis keys, all under the scc: prefix.
func StreamKey(channel string) string { return "scc:stream:" + channel }

const RetryKey = "scc:retry"

func RateKey(channel string) string { return "scc:ratelimit:" + channel }

type Queue struct {
	rdb *redis.Client
}

func NewQueue(rdb *redis.Client) *Queue { return &Queue{rdb: rdb} }

// EnsureGroups creates each channel stream and its consumer group.
func (q *Queue) EnsureGroups(ctx context.Context, channels []string) error {
	for _, ch := range channels {
		err := q.rdb.XGroupCreateMkStream(ctx, StreamKey(ch), Group, "0").Err()
		if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
			return fmt.Errorf("create group for %s: %w", ch, err)
		}
	}
	return nil
}

// Enqueue appends delivery ids to the channel stream in pipelined batches.
func (q *Queue) Enqueue(ctx context.Context, channel string, ids []int64) error {
	const batch = 500
	for start := 0; start < len(ids); start += batch {
		pipe := q.rdb.Pipeline()
		for _, id := range ids[start:min(start+batch, len(ids))] {
			pipe.XAdd(ctx, &redis.XAddArgs{
				Stream: StreamKey(channel), MaxLen: streamLen, Approx: true,
				Values: map[string]any{"id": id},
			})
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("enqueue %s: %w", channel, err)
		}
	}
	return nil
}

// Entry is one stream message.
type Entry struct {
	StreamID   string
	DeliveryID int64
}

// Read blocks up to block for new entries for this consumer.
func (q *Queue) Read(ctx context.Context, channel, consumer string, count int64, block time.Duration) ([]Entry, error) {
	res, err := q.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: Group, Consumer: consumer, Streams: []string{StreamKey(channel), ">"},
		Count: count, Block: block,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, s := range res {
		out = append(out, toEntries(s.Messages)...)
	}
	return out, nil
}

// Claim takes over entries another consumer read but never acknowledged
// (it crashed), once they have been idle for minIdle.
func (q *Queue) Claim(ctx context.Context, channel, consumer string, minIdle time.Duration) ([]Entry, error) {
	var out []Entry
	start := "0-0"
	for {
		msgs, next, err := q.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream: StreamKey(channel), Group: Group, Consumer: consumer,
			MinIdle: minIdle, Start: start, Count: 200,
		}).Result()
		if err != nil {
			return out, err
		}
		out = append(out, toEntries(msgs)...)
		if next == "0-0" || len(msgs) == 0 {
			return out, nil
		}
		start = next
	}
}

func (q *Queue) Ack(ctx context.Context, channel string, streamIDs ...string) error {
	if len(streamIDs) == 0 {
		return nil
	}
	return q.rdb.XAck(ctx, StreamKey(channel), Group, streamIDs...).Err()
}

// ScheduleRetry parks a delivery until due; PromoteDue moves it back.
func (q *Queue) ScheduleRetry(ctx context.Context, channel string, id int64, due time.Time) error {
	return q.rdb.ZAdd(ctx, RetryKey, redis.Z{Score: float64(due.UnixMilli()), Member: channel + ":" + strconv.FormatInt(id, 10)}).Err()
}

// promoteScript atomically moves due retries into their channel streams, so
// a crash between "read due" and "remove" cannot drop or double-promote.
var promoteScript = redis.NewScript(`
local due = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1], 'LIMIT', 0, tonumber(ARGV[2]))
for _, member in ipairs(due) do
  local sep = string.find(member, ':', 1, true)
  local channel = string.sub(member, 1, sep - 1)
  local id = string.sub(member, sep + 1)
  redis.call('XADD', ARGV[3] .. channel, 'MAXLEN', '~', ARGV[4], '*', 'id', id)
  redis.call('ZREM', KEYS[1], member)
end
return #due
`)

// PromoteDue returns how many retries were moved.
func (q *Queue) PromoteDue(ctx context.Context, now time.Time) (int, error) {
	// Stream keys are derived inside the script; this deployment is a single
	// Redis node (no cluster slot constraints).
	n, err := promoteScript.Run(ctx, q.rdb, []string{RetryKey}, now.UnixMilli(), 500, "scc:stream:", streamLen).Int()
	return n, err
}

// Depth reports per-channel backlog: entries not yet delivered to a
// consumer plus entries delivered but not acknowledged, and parked retries.
type Depth struct {
	Pending int64 `json:"pending"`
	Retry   int64 `json:"retry"`
}

func (q *Queue) Depth(ctx context.Context, channels []string) (map[string]Depth, error) {
	out := map[string]Depth{}
	for _, ch := range channels {
		groups, err := q.rdb.XInfoGroups(ctx, StreamKey(ch)).Result()
		if err != nil && !strings.Contains(err.Error(), "no such key") {
			return nil, err
		}
		var d Depth
		for _, g := range groups {
			if g.Name == Group {
				d.Pending = g.Lag + g.Pending
			}
		}
		out[ch] = d
	}
	members, err := q.rdb.ZRange(ctx, RetryKey, 0, -1).Result()
	if err != nil {
		return nil, err
	}
	for _, m := range members {
		ch, _, _ := strings.Cut(m, ":")
		d := out[ch]
		d.Retry++
		out[ch] = d
	}
	return out, nil
}

func toEntries(msgs []redis.XMessage) []Entry {
	out := make([]Entry, 0, len(msgs))
	for _, m := range msgs {
		raw, _ := m.Values["id"].(string)
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			id = 0 // malformed entry: worker acks and drops it
		}
		out = append(out, Entry{StreamID: m.ID, DeliveryID: id})
	}
	return out
}
