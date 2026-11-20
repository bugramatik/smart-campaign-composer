package campaign

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"scc/internal/content"
	"scc/internal/segment"
	"scc/internal/validation"
)

// Service holds the rules for changing a campaign by hand (marketer edits
// and approval). Generated output goes through the same validators.
type Service struct {
	Store    *Store
	Segments *segment.Store
	now      func() time.Time
}

func NewService(store *Store, segments *segment.Store) *Service {
	return &Service{Store: store, Segments: segments, now: time.Now}
}

// UpdateSegment validates a hand-edited rule and refreshes the audience.
func (s *Service) UpdateSegment(ctx context.Context, id int64, rule segment.Rule) (Campaign, error) {
	cat, err := s.Segments.Catalog(ctx)
	if err != nil {
		return Campaign{}, err
	}
	rule, err = cat.ValidateRule(rule)
	if err != nil {
		return Campaign{}, err
	}
	aud, err := s.Segments.Audience(ctx, cat, rule)
	if err != nil {
		return Campaign{}, err
	}
	return s.Store.SaveSegment(ctx, id, rule, nil, aud)
}

// UpdateContent stores hand-edited content. It is validated against the
// campaign's current audience so the editor sees the same limits as Claude.
func (s *Service) UpdateContent(ctx context.Context, id int64, ct content.Content) (Campaign, error) {
	c, err := s.Store.Get(ctx, id)
	if err != nil {
		return Campaign{}, err
	}
	if err := s.Check(c, ct); err != nil {
		return Campaign{}, err
	}
	return s.Store.SaveContent(ctx, id, ct)
}

// Check validates content for c without saving it (live editor feedback).
func (s *Service) Check(c Campaign, ct content.Content) error {
	return content.Validate(ct, s.specFor(c, segment.Channels))
}

// ContentSpec is the requirement set generated content must meet for c.
func (s *Service) ContentSpec(c Campaign) content.Spec { return s.specFor(c, segment.Channels) }

type ApproveInput struct {
	Channels    []string   `json:"channels"`
	ScheduledAt *time.Time `json:"scheduled_at"`
}

// Approve is the human gate. It re-validates everything against the data as
// it is now (the audience may have changed since generation) and refuses
// anything that could send an invalid or empty campaign.
func (s *Service) Approve(ctx context.Context, id int64, in ApproveInput) (Campaign, error) {
	c, err := s.Store.Get(ctx, id)
	if err != nil {
		return Campaign{}, err
	}
	if c.Status != StatusDraft {
		return Campaign{}, fmt.Errorf("%w (status is %s)", ErrInvalidState, c.Status)
	}

	var v validation.Collector
	var channels []string
	for _, ch := range in.Channels {
		ch = strings.ToLower(strings.TrimSpace(ch))
		if !slices.Contains(segment.Channels, ch) {
			v.Add("channels", "unknown channel %q", ch)
		} else if !slices.Contains(channels, ch) {
			channels = append(channels, ch)
		}
	}
	if len(in.Channels) == 0 {
		v.Add("channels", "select at least one channel")
	}
	if in.ScheduledAt != nil && in.ScheduledAt.Before(s.now().Add(-time.Minute)) {
		v.Add("scheduled_at", "must be in the future")
	}
	if c.Segment == nil {
		v.Add("segment", "generate or define a segment first")
	}
	if c.Content == nil {
		v.Add("content", "generate or write content first")
	}
	if err := v.Err(); err != nil {
		return Campaign{}, err
	}

	cat, err := s.Segments.Catalog(ctx)
	if err != nil {
		return Campaign{}, err
	}
	rule, err := cat.ValidateRule(*c.Segment)
	if err != nil {
		return Campaign{}, err
	}
	aud, err := s.Segments.Audience(ctx, cat, rule)
	if err != nil {
		return Campaign{}, err
	}
	c.Audience = &aud
	if err := content.Validate(*c.Content, s.specFor(c, channels)); err != nil {
		return Campaign{}, err
	}
	reachable := 0
	for _, ch := range channels {
		if ch == "sms" {
			reachable += aud.SMSEligible
		} else {
			reachable += aud.EmailEligible
		}
	}
	if reachable == 0 {
		return Campaign{}, &validation.Error{Issues: []validation.Issue{{Path: "segment", Message: "no user in the audience has consent for the selected channels"}}}
	}
	if _, err := s.Store.SaveSegment(ctx, id, rule, nil, aud); err != nil {
		return Campaign{}, err
	}
	return s.Store.Approve(ctx, id, channels, in.ScheduledAt)
}

// specFor builds the content requirements from the campaign's audience.
func (s *Service) specFor(c Campaign, channels []string) content.Spec {
	spec := content.Spec{Languages: []string{"tr"}, Channels: channels, MaxLengths: map[string]int{}}
	if c.Audience != nil {
		if langs := c.Audience.Languages(); len(langs) > 0 {
			spec.Languages = langs
		}
		spec.MaxLengths = c.Audience.MaxLengths
	}
	return spec
}
